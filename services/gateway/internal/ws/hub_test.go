package ws

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestClient builds a client with no socket — enough for registry/routing
// tests, which only ever touch the send channel.
func newTestClient(userID string, cap int) *client {
	return &client{userID: userID, send: make(chan []byte, cap), log: testLogger()}
}

// counter callbacks for registry subscribe transitions.
type subCounter struct {
	sub, unsub map[string]int
}

func newSubCounter() *subCounter {
	return &subCounter{sub: map[string]int{}, unsub: map[string]int{}}
}

func newRegistry(sc *subCounter) *registry {
	return &registry{
		clients:       make(map[string]map[*client]struct{}),
		log:           testLogger(),
		onSubscribe:   func(u string) { sc.sub[u]++ },
		onUnsubscribe: func(u string) { sc.unsub[u]++ },
	}
}

// ── registry: subscribe transitions ─────────────────────────────────────────

func TestRegistry_SubscribeTransitions(t *testing.T) {
	sc := newSubCounter()
	r := newRegistry(sc)

	a1 := newTestClient("a", 4)
	a2 := newTestClient("a", 4)
	b1 := newTestClient("b", 4)

	r.add(a1)
	r.add(a2) // second conn for "a": no new subscribe
	r.add(b1)

	if sc.sub["a"] != 1 {
		t.Errorf("subscribe(a) called %d times, want 1 (only on 0→1)", sc.sub["a"])
	}
	if sc.sub["b"] != 1 {
		t.Errorf("subscribe(b) called %d times, want 1", sc.sub["b"])
	}

	r.remove(a1) // "a" still has a2 → no unsubscribe
	if sc.unsub["a"] != 0 {
		t.Errorf("unsubscribe(a) called %d times, want 0 (a2 still connected)", sc.unsub["a"])
	}
	r.remove(a2) // 1→0 → unsubscribe
	if sc.unsub["a"] != 1 {
		t.Errorf("unsubscribe(a) called %d times, want 1", sc.unsub["a"])
	}
	r.remove(b1)
	if sc.unsub["b"] != 1 {
		t.Errorf("unsubscribe(b) called %d times, want 1", sc.unsub["b"])
	}
}

func TestRegistry_RemoveIdempotent(t *testing.T) {
	sc := newSubCounter()
	r := newRegistry(sc)
	c := newTestClient("a", 4)
	r.add(c)
	r.remove(c)
	r.remove(c) // second remove must be a no-op (no double close panic)
	if sc.unsub["a"] != 1 {
		t.Errorf("unsubscribe(a) = %d, want 1", sc.unsub["a"])
	}
}

// ── registry: routing ───────────────────────────────────────────────────────

func TestRegistry_RoutesToTargetUserOnly(t *testing.T) {
	r := newRegistry(newSubCounter())
	a := newTestClient("a", 4)
	b := newTestClient("b", 4)
	r.add(a)
	r.add(b)

	r.route("a", []byte("hi-a"))

	select {
	case got := <-a.send:
		if string(got) != "hi-a" {
			t.Errorf("a got %q, want hi-a", got)
		}
	default:
		t.Error("a should have received the message")
	}
	select {
	case got := <-b.send:
		t.Errorf("b should not have received anything, got %q", got)
	default:
	}
}

func TestRegistry_FanOutToSameUser(t *testing.T) {
	r := newRegistry(newSubCounter())
	c1 := newTestClient("a", 4)
	c2 := newTestClient("a", 4)
	r.add(c1)
	r.add(c2)

	r.route("a", []byte("hello"))

	for i, c := range []*client{c1, c2} {
		select {
		case got := <-c.send:
			if string(got) != "hello" {
				t.Errorf("client %d got %q, want hello", i, got)
			}
		default:
			t.Errorf("client %d received nothing", i)
		}
	}
}

func TestRegistry_EvictsSlowConsumer(t *testing.T) {
	sc := newSubCounter()
	r := newRegistry(sc)
	slow := newTestClient("a", 1) // tiny buffer
	r.add(slow)

	r.route("a", []byte("first"))  // fills the 1-slot buffer
	r.route("a", []byte("second")) // buffer full → TrySend fails → evict

	if _, ok := r.clients["a"]; ok {
		t.Error("slow client should have been evicted (user removed)")
	}
	if sc.unsub["a"] != 1 {
		t.Errorf("unsubscribe(a) = %d, want 1 after eviction", sc.unsub["a"])
	}
	// send must be closed by remove: drain the buffered value, then closed.
	<-slow.send
	if _, ok := <-slow.send; ok {
		t.Error("evicted client's send channel should be closed")
	}
}

func TestRegistry_CloseAll(t *testing.T) {
	r := newRegistry(newSubCounter())
	a := newTestClient("a", 4)
	b := newTestClient("b", 4)
	r.add(a)
	r.add(b)

	r.closeAll()

	if len(r.clients) != 0 {
		t.Errorf("registry not cleared: %d users remain", len(r.clients))
	}
	for _, c := range []*client{a, b} {
		if _, ok := <-c.send; ok {
			t.Error("send channel should be closed after closeAll")
		}
	}
}

// ── fake PubSub ──────────────────────────────────────────────────────────────

type fakeSubscriber struct {
	mu     sync.Mutex
	subbed map[string]bool
	subN   int
	unsubN int
	ch     chan *redis.Message
	closed bool
}

func newFakeSubscriber() *fakeSubscriber {
	return &fakeSubscriber{subbed: map[string]bool{}, ch: make(chan *redis.Message, 16)}
}

func (f *fakeSubscriber) Subscribe(_ context.Context, channels ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range channels {
		f.subbed[c] = true
		f.subN++
	}
	return nil
}

func (f *fakeSubscriber) Unsubscribe(_ context.Context, channels ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range channels {
		delete(f.subbed, c)
		f.unsubN++
	}
	return nil
}

func (f *fakeSubscriber) Channel(_ ...redis.ChannelOption) <-chan *redis.Message {
	return f.ch
}

func (f *fakeSubscriber) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.ch)
	}
	return nil
}

func (f *fakeSubscriber) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// ── Hub: Redis reader routes to the owner goroutine ─────────────────────────

func TestHub_ReadRedisRoutesByChannel(t *testing.T) {
	fs := newFakeSubscriber()
	h := NewHub(fs, testLogger())
	go h.readRedis()
	defer close(h.quit)

	fs.ch <- &redis.Message{Channel: "user:u42", Payload: "ping"}

	select {
	case in := <-h.inboundCh:
		if in.userID != "u42" || string(in.payload) != "ping" {
			t.Fatalf("got {%q,%q}, want {u42,ping}", in.userID, in.payload)
		}
	case <-time.After(time.Second):
		t.Fatal("readRedis did not route the message")
	}
}

// ── Hub: end-to-end register → subscribe, shutdown → drain ───────────────────

func TestHub_RegisterSubscribesAndShutdownDrains(t *testing.T) {
	fs := newFakeSubscriber()
	h := NewHub(fs, testLogger())
	h.Run()

	// fakePump mimics writePump's wg contract: drain send until closed, Done.
	fakePump := func(c *client) {
		defer h.wg.Done()
		for range c.send {
		}
	}

	users := []string{"u1", "u2", "u3"}
	for _, u := range users {
		c := newClient(nil, u, testLogger())
		if !h.Register(c) {
			t.Fatalf("Register(%s) returned false", u)
		}
		go fakePump(c)
	}

	// Each distinct user's 0→1 should have produced exactly one Subscribe.
	deadline := time.After(time.Second)
	for {
		fs.mu.Lock()
		n := fs.subN
		fs.mu.Unlock()
		if n == len(users) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("subscribe count = %d, want %d", n, len(users))
		case <-time.After(2 * time.Millisecond):
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h.Shutdown(ctx)

	if !fs.isClosed() {
		t.Error("pubsub should be closed after Shutdown")
	}
	if ctx.Err() != nil {
		t.Errorf("Shutdown hit ctx deadline (%v) — pumps did not drain in time", ctx.Err())
	}
	// Registering after shutdown is rejected.
	if h.Register(newClient(nil, "late", testLogger())) {
		t.Error("Register after Shutdown should return false")
	}
}
