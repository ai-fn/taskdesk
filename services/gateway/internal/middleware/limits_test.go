package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var ok200 = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

// budget allows the first n calls per key.
type budget struct {
	mu   sync.Mutex
	left map[string]int
	n    int
}

func (b *budget) Allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.left == nil {
		b.left = map[string]int{}
	}
	if _, ok := b.left[key]; !ok {
		b.left[key] = b.n
	}
	if b.left[key] == 0 {
		return false
	}
	b.left[key]--
	return true
}

func TestMaxBody_RejectsDeclaredOversize(t *testing.T) {
	h := MaxBody(10)(ok200)
	req := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", 11)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
}

func TestMaxBody_CapsChunkedBodies(t *testing.T) {
	var readErr error
	h := MaxBody(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))
	req := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", 100)))
	req.ContentLength = -1 // unknown length, as with chunked encoding
	h.ServeHTTP(httptest.NewRecorder(), req)
	if readErr == nil {
		t.Error("reading an oversize chunked body must fail")
	}
}

func TestMaxBody_AllowsSmallBodies(t *testing.T) {
	w := httptest.NewRecorder()
	MaxBody(10)(ok200).ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader("hi")))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestRateLimitIP_PerIPAndSkipsProbes(t *testing.T) {
	h := RateLimitIP(&budget{n: 2}, "/healthz")(ok200)
	do := func(path, ip string) int {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-Real-IP", ip)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests && w.Header().Get("Retry-After") == "" {
			t.Error("429 without Retry-After")
		}
		return w.Code
	}
	for i := 0; i < 2; i++ {
		if got := do("/x", "1.1.1.1"); got != 200 {
			t.Fatalf("request %d status = %d, want 200", i+1, got)
		}
	}
	if got := do("/x", "1.1.1.1"); got != http.StatusTooManyRequests {
		t.Errorf("third request status = %d, want 429", got)
	}
	if got := do("/x", "2.2.2.2"); got != 200 {
		t.Errorf("other IP status = %d, want 200", got)
	}
	for i := 0; i < 5; i++ {
		if got := do("/healthz", "1.1.1.1"); got != 200 {
			t.Fatalf("probe limited with status %d", got)
		}
	}
}

func TestRateLimitUser_KeysByUser(t *testing.T) {
	h := RateLimitUser(&budget{n: 1})(ok200)
	do := func(user string) int {
		req := httptest.NewRequest("GET", "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), ctxUserID, user))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if do("a") != 200 || do("a") != http.StatusTooManyRequests || do("b") != 200 {
		t.Error("limit must apply per user: a then a again blocked, b unaffected")
	}
}

func TestOnePerUser_BlocksConcurrentThenReleases(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	h := OnePerUser()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hold") == "1" {
			close(started)
			<-release
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := func(user, hold string) *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("X-Hold", hold)
		return r.WithContext(context.WithValue(r.Context(), ctxUserID, user))
	}

	done := make(chan int)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req("a", "1"))
		done <- w.Code
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never started")
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("a", ""))
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("second concurrent request status = %d, want 429", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req("b", ""))
	if w.Code != http.StatusOK {
		t.Errorf("other user status = %d, want 200", w.Code)
	}

	close(release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("first request status = %d", code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req("a", ""))
	if w.Code != http.StatusOK {
		t.Errorf("after release status = %d, want 200", w.Code)
	}
}

func TestClientKey(t *testing.T) {
	cases := []struct {
		name, realIP, fwd, remote, want string
	}{
		{"x-real-ip wins", "9.9.9.9", "8.8.8.8", "10.0.0.1:1234", "9.9.9.9"},
		{"forwarded first hop", "", "8.8.8.8, 7.7.7.7", "10.0.0.1:1234", "8.8.8.8"},
		{"ipv4 peer", "", "", "10.0.0.1:1234", "10.0.0.1"},
		{"ipv6 peer is masked to /64", "", "", "[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:1234", "2001:db8:1:2::/64"},
		{"same /64, other host", "2001:db8:1:2:1111:2222:3333:4444", "", "10.0.0.1:1", "2001:db8:1:2::/64"},
		{"garbage header falls through", "not-an-ip", "also-bad", "10.0.0.1:1234", "10.0.0.1"},
		{"ipv6 zone id does not mint buckets", "fe80::1%eth0", "", "10.0.0.1:1", "fe80::/64"},
		{"v4-mapped v6 is unmapped", "::ffff:9.9.9.9", "", "10.0.0.1:1234", "9.9.9.9"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		if tc.realIP != "" {
			r.Header.Set("X-Real-IP", tc.realIP)
		}
		if tc.fwd != "" {
			r.Header.Set("X-Forwarded-For", tc.fwd)
		}
		if got := ClientKey(r); got != tc.want {
			t.Errorf("%s: ClientKey = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestOnePerUser_ReleasesSlotAfterPanic(t *testing.T) {
	h := OnePerUser()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Panic") == "1" {
			panic("boom")
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := func(panics string) *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("X-Panic", panics)
		return r.WithContext(context.WithValue(r.Context(), ctxUserID, "a"))
	}

	func() {
		defer func() { _ = recover() }() // stands in for chi's Recoverer
		h.ServeHTTP(httptest.NewRecorder(), req("1"))
	}()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req(""))
	if w.Code != http.StatusOK {
		t.Errorf("status after a panicked request = %d, want 200: the slot must be released", w.Code)
	}
}

func TestRateLimit_RejectsWithJSON(t *testing.T) {
	w := httptest.NewRecorder()
	RateLimitIP(&budget{n: 0})(ok200).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("status=%d content-type=%q, want 429 JSON", w.Code, w.Header().Get("Content-Type"))
	}
}
