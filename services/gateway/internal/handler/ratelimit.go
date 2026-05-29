package handler

import (
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter is a per-key token-bucket rate limiter backed by a plain Go map.
//
// Concurrency model — two lock modes, one concept:
//
//   - mu.RLock()  is held while reading an existing entry from the map.
//     Many goroutines may hold an RLock simultaneously — no writer is blocked.
//   - mu.Lock()   is held only when inserting or deleting entries.
//     It excludes all readers and all other writers.
//
// The pattern in Allow() is: try a cheap RLock first; if the key is absent,
// upgrade to a full Lock to insert it. This is the idiomatic Go way to avoid
// holding an exclusive lock on the hot (already-populated) path.
//
// Each entry holds a *rate.Limiter from golang.org/x/time/rate. Limiter is
// itself goroutine-safe, so once we retrieve the pointer under RLock the
// caller can call limiter.Allow() without holding mu at all.
//
// lastSeenNs is an atomic.Int64 (Unix nanoseconds) so that Allow() can update
// it after releasing the map lock without racing against cleanupLoop, which
// reads it under mu.Lock(). Using an atomic avoids re-acquiring any lock on
// the hot path.
type RateLimiter struct {
	mu      sync.RWMutex
	entries map[string]*rlEntry
	r       rate.Limit    // tokens refilled per second
	burst   int           // bucket capacity
	idleTTL time.Duration // evict entries idle longer than this
	stop    chan struct{}  // closed by Stop() to end the cleanup goroutine
	once    sync.Once     // ensures stop is closed exactly once
}

type rlEntry struct {
	limiter     *rate.Limiter
	lastSeenNs  atomic.Int64 // time.Now().UnixNano() — written by Allow, read by cleanup
}

// NewRateLimiter creates a RateLimiter and starts a background cleanup goroutine.
// Call Stop() when the limiter is no longer needed (e.g. on server shutdown).
func NewRateLimiter(r rate.Limit, burst int, idle, sweep time.Duration) *RateLimiter {
	rl := &RateLimiter{
		entries: make(map[string]*rlEntry),
		r:       r,
		burst:   burst,
		idleTTL: idle,
		stop:    make(chan struct{}),
	}
	go rl.cleanupLoop(sweep)
	return rl
}

// Allow reports whether key is under its rate limit and consumes one token.
//
// Fast path (key already exists):
//  1. RLock — shared; many goroutines may read concurrently.
//  2. Find the entry, RUnlock immediately.
//  3. Update lastSeenNs atomically and call limiter.Allow() — no lock needed.
//
// Slow path (key missing):
//  1. Lock — exclusive; blocks all readers and other writers.
//  2. Double-check: another goroutine may have inserted the key between our
//     RUnlock and this Lock. If so, use the existing entry.
//  3. Otherwise, create and insert a new entry, then Unlock.
//  4. Call limiter.Allow() outside the lock.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.RLock()
	e, ok := rl.entries[key]
	rl.mu.RUnlock()

	if ok {
		e.lastSeenNs.Store(time.Now().UnixNano())
		return e.limiter.Allow()
	}

	rl.mu.Lock()
	// Double-check after acquiring the write lock: another goroutine may have
	// inserted this key in the window between our RUnlock and this Lock.
	e, ok = rl.entries[key]
	if !ok {
		e = &rlEntry{limiter: rate.NewLimiter(rl.r, rl.burst)}
		e.lastSeenNs.Store(time.Now().UnixNano())
		rl.entries[key] = e
	}
	rl.mu.Unlock()

	return e.limiter.Allow()
}

// Stop signals the cleanup goroutine to exit. Safe to call multiple times —
// sync.Once ensures the underlying channel is closed exactly once.
func (rl *RateLimiter) Stop() {
	rl.once.Do(func() { close(rl.stop) })
}

// cleanupLoop runs on its own goroutine. Every sweep interval it acquires the
// write lock and deletes entries that have not been seen within idleTTL.
// The write lock is required because map deletion is not safe concurrent with
// any other map operation.
func (rl *RateLimiter) cleanupLoop(sweep time.Duration) {
	ticker := time.NewTicker(sweep)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.cleanup()
		case <-rl.stop:
			return
		}
	}
}

func (rl *RateLimiter) cleanup() {
	now := time.Now().UnixNano()
	idleNs := int64(rl.idleTTL)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for key, e := range rl.entries {
		if now-e.lastSeenNs.Load() > idleNs {
			delete(rl.entries, key)
		}
	}
}
