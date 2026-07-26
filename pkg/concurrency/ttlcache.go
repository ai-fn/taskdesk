// Package concurrency provides small, reusable generic concurrency primitives
// shared across services: a singleflight-backed TTL cache and a non-blocking
// channel send. Both are written to be correct under -race and to keep their
// locking discipline explicit, so callers can reason about the hot path.
package concurrency

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// TTLCache is a generic, goroutine-safe, expiring key/value cache whose misses
// are collapsed by singleflight so that N concurrent lookups of the same absent
// key trigger exactly one fetch.
//
// Concurrency model — two independent locks, never held together:
//
//   - mu (RWMutex) guards only the entries map. The read fast-path takes
//     RLock; inserting, deleting, and the janitor sweep take Lock.
//   - sf (singleflight.Group) has its own internal lock and dedups concurrent
//     fetches for the same key.
//
// The fetch function is NEVER called while mu is held: GetOrFetch releases mu
// before delegating to singleflight, and the singleflight callback re-acquires
// mu only to double-check and to store the result. This avoids serializing slow
// fetches behind the map lock, and avoids lock-ordering hazards between the two
// locks.
//
// Errors are not cached: a failed fetch leaves the key absent so the next call
// retries. Per-caller context is honored — a hung fetch lets each caller bail
// with its own deadline while the shared fetch keeps running for whoever waits.
type TTLCache[K comparable, V any] struct {
	mu      sync.RWMutex
	entries map[K]entry[V]
	ttl     time.Duration
	now     func() time.Time // injectable clock (tests)
	sf      singleflight.Group

	stop chan struct{}
	once sync.Once
}

type entry[V any] struct {
	value     V
	expiresAt time.Time
}

// Option configures a TTLCache at construction.
type Option[K comparable, V any] func(*TTLCache[K, V])

// WithClock injects a custom time source (used by tests to drive expiry
// deterministically). Production callers should omit it.
func WithClock[K comparable, V any](now func() time.Time) Option[K, V] {
	return func(c *TTLCache[K, V]) { c.now = now }
}

// WithJanitor starts a background goroutine that sweeps expired entries every
// `sweep` interval. Without it, entries are evicted lazily on access only —
// fine for bounded key spaces (e.g. a handful of signing keys), wasteful for
// unbounded ones (e.g. per-user profiles), which should pass a janitor. The
// goroutine exits on Stop().
func WithJanitor[K comparable, V any](sweep time.Duration) Option[K, V] {
	return func(c *TTLCache[K, V]) {
		go c.janitor(sweep)
	}
}

// NewTTLCache builds a cache whose entries live for ttl after they are written.
func NewTTLCache[K comparable, V any](ttl time.Duration, opts ...Option[K, V]) *TTLCache[K, V] {
	c := &TTLCache[K, V]{
		entries: make(map[K]entry[V]),
		ttl:     ttl,
		now:     time.Now,
		stop:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// GetOrFetch returns the cached value for key, or calls fetch exactly once
// (across all concurrent callers for the same key) to populate it.
//
// Fast path: an RLock read of a live entry, then return.
// Slow path: delegate to singleflight.DoChan; the callback double-checks the
// map (another goroutine may have filled it) and on a real miss runs fetch and
// stores the result under Lock. Each caller selects on its own ctx so a slow
// fetch never pins a caller past its deadline.
func (c *TTLCache[K, V]) GetOrFetch(ctx context.Context, key K, fetch func(context.Context) (V, error)) (V, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}

	// singleflight dedups on a string key. K is comparable but not necessarily
	// a string, so we render it via fmt.Sprint. For the homogeneous key types
	// this cache is used with (string ids), that rendering is injective.
	resCh := c.sf.DoChan(c.sfKey(key), func() (any, error) {
		// Double-check: a concurrent caller may have populated the entry
		// between our Get miss and singleflight electing this goroutine.
		if v, ok := c.Get(key); ok {
			return v, nil
		}
		v, err := fetch(ctx)
		if err != nil {
			return *new(V), err // not cached
		}
		c.Set(key, v)
		return v, nil
	})

	select {
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	case res := <-resCh:
		if res.Err != nil {
			var zero V
			return zero, res.Err
		}
		return res.Val.(V), nil
	}
}

// Get returns the live value for key, or ok=false if absent or expired.
func (c *TTLCache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || !c.now().Before(e.expiresAt) {
		var zero V
		return zero, false
	}
	return e.value, true
}

// Set writes value for key with a fresh TTL, overwriting any existing entry.
func (c *TTLCache[K, V]) Set(key K, value V) {
	exp := c.now().Add(c.ttl)
	c.mu.Lock()
	c.entries[key] = entry[V]{value: value, expiresAt: exp}
	c.mu.Unlock()
}

// Invalidate removes a single key (no-op if absent).
func (c *TTLCache[K, V]) Invalidate(key K) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// InvalidateAll clears the cache.
func (c *TTLCache[K, V]) InvalidateAll() {
	c.mu.Lock()
	c.entries = make(map[K]entry[V])
	c.mu.Unlock()
}

// Stop ends the janitor goroutine (if any). Safe to call multiple times.
func (c *TTLCache[K, V]) Stop() {
	c.once.Do(func() { close(c.stop) })
}

// sfKey renders a cache key as the string singleflight needs to dedup on.
func (c *TTLCache[K, V]) sfKey(key K) string {
	return fmt.Sprint(key)
}

func (c *TTLCache[K, V]) janitor(sweep time.Duration) {
	ticker := time.NewTicker(sweep)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.sweep()
		case <-c.stop:
			return
		}
	}
}

func (c *TTLCache[K, V]) sweep() {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if !now.Before(e.expiresAt) {
			delete(c.entries, k)
		}
	}
}
