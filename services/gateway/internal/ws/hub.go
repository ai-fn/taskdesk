// Package ws implements a per-pod WebSocket hub that fans out real-time
// notifications from Redis pub/sub to connected clients.
//
// Design:
//   - A single owner goroutine (run) owns the client registry. All mutations —
//     register, unregister, message routing, slow-consumer eviction — happen
//     there, so the registry needs no lock and there are no data races on it.
//   - One Redis PubSub per pod. The hub dynamically Subscribes to "user:{id}"
//     when a user's first connection arrives (0→1) and Unsubscribes when the
//     last one leaves (1→0). This is O(1) delivery per message under sticky
//     sessions, versus psubscribe "user:*" which would wake every pod for every
//     message and discard most.
//   - Per-client bounded send channel. A client that can't keep up (buffer
//     full) is evicted rather than allowed to block the owner goroutine or
//     other clients — see registry.route.
package ws

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
)

const channelPrefix = "user:"

// PubSub is the subset of *redis.PubSub the hub depends on. Abstracting it lets
// the hub be tested with a fake — no real Redis required.
type PubSub interface {
	Subscribe(ctx context.Context, channels ...string) error
	Unsubscribe(ctx context.Context, channels ...string) error
	Channel(opts ...redis.ChannelOption) <-chan *redis.Message
	Close() error
}

// inbound is a routed message: a payload destined for one user's connections.
type inbound struct {
	userID  string
	payload []byte
}

// Hub multiplexes one Redis PubSub across many WebSocket clients on this pod.
type Hub struct {
	pubsub PubSub
	log    *slog.Logger
	reg    *registry

	registerCh   chan *client
	unregisterCh chan *client
	inboundCh    chan inbound

	ctx    context.Context // lifetime ctx for Subscribe/Unsubscribe calls
	cancel context.CancelFunc

	quit    chan struct{} // closed by Shutdown to stop run/readRedis
	stopped chan struct{} // closed when run has exited (registry drained)

	mu     sync.Mutex     // guards closed + wg.Add, so no Add races wg.Wait
	closed bool           // set by Shutdown; rejects new registrations
	wg     sync.WaitGroup // tracks live client writePumps, for graceful drain
}

// NewHub builds a hub over the given PubSub. Call Run to start it.
func NewHub(pubsub PubSub, log *slog.Logger) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Hub{
		pubsub:       pubsub,
		log:          log,
		registerCh:   make(chan *client),
		unregisterCh: make(chan *client),
		inboundCh:    make(chan inbound, 256),
		ctx:          ctx,
		cancel:       cancel,
		quit:         make(chan struct{}),
		stopped:      make(chan struct{}),
	}
	h.reg = &registry{
		clients:       make(map[string]map[*client]struct{}),
		log:           log,
		onSubscribe:   h.subscribe,
		onUnsubscribe: h.unsubscribe,
	}
	return h
}

// Run starts the owner goroutine and the Redis reader. Non-blocking.
func (h *Hub) Run() {
	go h.run()
	go h.readRedis()
}

// run is the single owner of the registry: every registry mutation happens here.
func (h *Hub) run() {
	defer close(h.stopped)
	for {
		select {
		case c := <-h.registerCh:
			h.reg.add(c)
		case c := <-h.unregisterCh:
			h.reg.remove(c)
		case m := <-h.inboundCh:
			h.reg.route(m.userID, m.payload)
		case <-h.quit:
			h.reg.closeAll() // signal every writePump to flush + close
			return
		}
	}
}

// readRedis pumps messages from the shared PubSub into the owner goroutine,
// translating channel names ("user:{id}") into routable inbound messages.
func (h *Hub) readRedis() {
	ch := h.pubsub.Channel()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return // PubSub closed
			}
			userID := strings.TrimPrefix(msg.Channel, channelPrefix)
			select {
			case h.inboundCh <- inbound{userID: userID, payload: []byte(msg.Payload)}:
			case <-h.quit:
				return
			}
		case <-h.quit:
			return
		}
	}
}

// Register enrolls a client. It returns false if the hub is shutting down, in
// which case the caller must not start the client's pumps. On success the caller
// owns calling writePump (which performs the matching wg.Done).
func (h *Hub) Register(c *client) bool {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return false
	}
	h.wg.Add(1)
	h.mu.Unlock()

	select {
	case h.registerCh <- c:
		return true
	case <-h.quit:
		h.wg.Done()
		return false
	}
}

// unregister asks the owner goroutine to drop a client. Idempotent: the registry
// ignores clients it no longer holds. A no-op once the hub has stopped (the
// registry was already drained by closeAll).
func (h *Hub) unregister(c *client) {
	select {
	case h.unregisterCh <- c:
	case <-h.quit:
	}
}

// Shutdown stops accepting registrations, tells every client to flush and close,
// and waits for their writePumps to finish or ctx to expire, then tears down the
// PubSub. Hijacked WebSocket connections are not drained by http.Server.Shutdown,
// so this must be sequenced explicitly on shutdown.
func (h *Hub) Shutdown(ctx context.Context) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true // no further wg.Add after this point
	h.mu.Unlock()

	close(h.quit) // stop run/readRedis; run() runs closeAll on its way out
	<-h.stopped   // registry drained: every client's send channel is closed

	// Wait for writePumps to flush buffers + send close frames, bounded by ctx.
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		h.log.Warn("ws hub shutdown: drain deadline exceeded")
	}

	h.cancel()
	_ = h.pubsub.Close()
}

func (h *Hub) subscribe(userID string) {
	if err := h.pubsub.Subscribe(h.ctx, channelPrefix+userID); err != nil {
		h.log.Error("ws redis subscribe", "user_id", userID, "err", err)
	}
}

func (h *Hub) unsubscribe(userID string) {
	if err := h.pubsub.Unsubscribe(h.ctx, channelPrefix+userID); err != nil {
		h.log.Error("ws redis unsubscribe", "user_id", userID, "err", err)
	}
}
