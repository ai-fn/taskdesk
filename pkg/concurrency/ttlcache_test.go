package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetOrFetch_SingleflightDedup(t *testing.T) {
	c := NewTTLCache[string, int](time.Minute)
	defer c.Stop()

	var calls atomic.Int64
	release := make(chan struct{})
	fetch := func(context.Context) (int, error) {
		calls.Add(1)
		<-release // hold all callers inside the fetch until released
		return 42, nil
	}

	const n = 100
	var wg sync.WaitGroup
	results := make([]int, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = c.GetOrFetch(context.Background(), "k", fetch)
		}(i)
	}

	// Give the goroutines time to pile up on the same singleflight key.
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("fetch called %d times, want exactly 1 (singleflight dedup)", got)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d got error: %v", i, errs[i])
		}
		if results[i] != 42 {
			t.Fatalf("caller %d got %d, want 42", i, results[i])
		}
	}
}

func TestGetOrFetch_CachesHit(t *testing.T) {
	c := NewTTLCache[string, int](time.Minute)
	defer c.Stop()

	var calls atomic.Int64
	fetch := func(context.Context) (int, error) {
		calls.Add(1)
		return 7, nil
	}

	for i := 0; i < 5; i++ {
		v, err := c.GetOrFetch(context.Background(), "k", fetch)
		if err != nil || v != 7 {
			t.Fatalf("got (%d,%v), want (7,nil)", v, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("fetch called %d times, want 1 (subsequent calls hit cache)", got)
	}
}

func TestTTLExpiry_WithInjectedClock(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Unix(0, 0).UnixNano())
	clock := func() time.Time { return time.Unix(0, now.Load()) }

	c := NewTTLCache[string, int](10*time.Second, WithClock[string, int](clock))
	defer c.Stop()

	var calls atomic.Int64
	fetch := func(context.Context) (int, error) {
		calls.Add(1)
		return int(calls.Load()), nil
	}

	if v, _ := c.GetOrFetch(context.Background(), "k", fetch); v != 1 {
		t.Fatalf("first fetch got %d, want 1", v)
	}
	// Within TTL → cache hit, no new fetch.
	now.Add(5 * int64(time.Second))
	if v, _ := c.GetOrFetch(context.Background(), "k", fetch); v != 1 {
		t.Fatalf("within TTL got %d, want cached 1", v)
	}
	// Past TTL → refetch.
	now.Add(6 * int64(time.Second))
	if v, _ := c.GetOrFetch(context.Background(), "k", fetch); v != 2 {
		t.Fatalf("after expiry got %d, want refetched 2", v)
	}
}

func TestGetOrFetch_ErrorNotCached(t *testing.T) {
	c := NewTTLCache[string, int](time.Minute)
	defer c.Stop()

	var calls atomic.Int64
	wantErr := errors.New("boom")
	fetch := func(context.Context) (int, error) {
		if calls.Add(1) == 1 {
			return 0, wantErr
		}
		return 99, nil
	}

	if _, err := c.GetOrFetch(context.Background(), "k", fetch); !errors.Is(err, wantErr) {
		t.Fatalf("first call err = %v, want %v", err, wantErr)
	}
	// Error must not be cached: the next call retries and succeeds.
	if v, err := c.GetOrFetch(context.Background(), "k", fetch); err != nil || v != 99 {
		t.Fatalf("retry got (%d,%v), want (99,nil)", v, err)
	}
}

func TestInvalidate(t *testing.T) {
	c := NewTTLCache[string, int](time.Minute)
	defer c.Stop()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Invalidate("a")
	if _, ok := c.Get("a"); ok {
		t.Fatal("a should be gone after Invalidate")
	}
	if v, ok := c.Get("b"); !ok || v != 2 {
		t.Fatal("b should remain")
	}

	c.InvalidateAll()
	if _, ok := c.Get("b"); ok {
		t.Fatal("b should be gone after InvalidateAll")
	}
}

func TestJanitor_EvictsExpired(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Unix(0, 0).UnixNano())
	clock := func() time.Time { return time.Unix(0, now.Load()) }

	c := NewTTLCache[string, int](
		10*time.Millisecond,
		WithClock[string, int](clock),
		WithJanitor[string, int](5*time.Millisecond),
	)
	defer c.Stop()

	c.Set("k", 1)
	now.Add(int64(time.Hour)) // advance well past TTL

	// Wait for the janitor to run a sweep and drop the expired entry from the map.
	deadline := time.After(2 * time.Second)
	for {
		c.mu.RLock()
		_, present := c.entries["k"]
		c.mu.RUnlock()
		if !present {
			return // evicted
		}
		select {
		case <-deadline:
			t.Fatal("janitor did not evict expired entry")
		case <-time.After(2 * time.Millisecond):
		}
	}
}
