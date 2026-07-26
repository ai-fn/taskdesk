package handler

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// TestRateLimiter_AllowsUnderLimit confirms that a key can fire up to burst
// attempts before being blocked.
func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	rl := NewRateLimiter(rate.Limit(100), 3, time.Minute, time.Hour)
	defer rl.Stop()

	for i := range 3 {
		if !rl.Allow("key") {
			t.Fatalf("attempt %d should have been allowed (burst=3)", i+1)
		}
	}
}

// TestRateLimiter_BlocksOverLimit confirms that the (burst+1)th attempt is denied.
func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	// rate nearly zero so the bucket does not refill during the test.
	rl := NewRateLimiter(rate.Limit(0.001), 3, time.Minute, time.Hour)
	defer rl.Stop()

	for range 3 {
		rl.Allow("key") // drain the burst
	}
	if rl.Allow("key") {
		t.Fatal("4th attempt should have been blocked")
	}
}

// TestRateLimiter_IndependentKeys confirms two keys do not share a bucket.
func TestRateLimiter_IndependentKeys(t *testing.T) {
	rl := NewRateLimiter(rate.Limit(0.001), 1, time.Minute, time.Hour)
	defer rl.Stop()

	if !rl.Allow("a") {
		t.Fatal("first call on key 'a' should be allowed")
	}
	if !rl.Allow("b") {
		t.Fatal("first call on key 'b' should be allowed — independent bucket")
	}
	// Both buckets are now empty.
	if rl.Allow("a") {
		t.Fatal("second call on 'a' should be blocked")
	}
}

// TestRateLimiter_Cleanup confirms that idle entries are evicted by cleanup().
// It calls cleanup() directly rather than sleeping through the background
// goroutine's tick interval, making the test deterministic.
func TestRateLimiter_Cleanup(t *testing.T) {
	rl := NewRateLimiter(rate.Limit(100), 5, time.Minute, time.Hour)
	defer rl.Stop()

	rl.Allow("ephemeral")

	// Force lastSeenNs into the past so the entry appears idle.
	rl.mu.RLock()
	e := rl.entries["ephemeral"]
	rl.mu.RUnlock()
	e.lastSeenNs.Store(time.Now().Add(-time.Hour).UnixNano())

	rl.cleanup()

	rl.mu.RLock()
	_, exists := rl.entries["ephemeral"]
	rl.mu.RUnlock()
	if exists {
		t.Fatal("idle entry should have been evicted by cleanup()")
	}
}
