package ws

import (
	"log/slog"

	"github.com/fsociety/todo/pkg/concurrency"
)

// registry is the hub's client bookkeeping, extracted from the goroutine
// plumbing so its state-machine logic (subscribe transitions, routing,
// slow-consumer eviction) is unit-testable without sockets or Redis.
//
// It is NOT safe for concurrent use: the hub's single owner goroutine is its
// only caller. That invariant is what lets it run lock-free.
type registry struct {
	clients map[string]map[*client]struct{}
	log     *slog.Logger

	// onSubscribe fires when a user's connection count goes 0→1, onUnsubscribe
	// when it goes 1→0. The hub wires these to Redis (un)subscribe.
	onSubscribe   func(userID string)
	onUnsubscribe func(userID string)
}

// add records a client, subscribing on the user's first connection.
func (r *registry) add(c *client) {
	set := r.clients[c.userID]
	if set == nil {
		set = make(map[*client]struct{})
		r.clients[c.userID] = set
		r.onSubscribe(c.userID) // 0→1
	}
	set[c] = struct{}{}
}

// remove drops a client, closing its send channel (the writePump stop signal)
// and unsubscribing on the user's last connection. Idempotent.
func (r *registry) remove(c *client) {
	set := r.clients[c.userID]
	if set == nil {
		return
	}
	if _, ok := set[c]; !ok {
		return // already removed (e.g. evicted then unregistered)
	}
	delete(set, c)
	close(c.send)
	if len(set) == 0 {
		delete(r.clients, c.userID)
		r.onUnsubscribe(c.userID) // 1→0
	}
}

// route delivers payload to all of a user's connections. A connection whose
// buffer is full is a slow consumer: it is evicted rather than allowed to block
// the owner goroutine. Deleting during the range is safe for Go maps.
func (r *registry) route(userID string, payload []byte) {
	for c := range r.clients[userID] {
		if !concurrency.TrySend(c.send, payload) {
			r.log.Warn("evicting slow websocket client", "user_id", userID)
			r.remove(c)
		}
	}
}

// closeAll closes every client's send channel and clears the registry. Used on
// shutdown; no per-user unsubscribe because the whole PubSub is being closed.
func (r *registry) closeAll() {
	for userID, set := range r.clients {
		for c := range set {
			close(c.send)
		}
		delete(r.clients, userID)
	}
}
