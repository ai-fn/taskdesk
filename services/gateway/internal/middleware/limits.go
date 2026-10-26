package middleware

import (
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
)

// Allower is a keyed rate limiter; handler.RateLimiter satisfies it.
type Allower interface {
	Allow(key string) bool
}

// MaxBody rejects bodies over n bytes: up front via Content-Length (413), and mid-read
// for chunked bodies (the handler's decode then fails).
func MaxBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > n {
				jsonError(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitIP limits by ClientKey; skip lists exact paths that are never limited (probes).
func RateLimitIP(a Allower, skip ...string) func(http.Handler) http.Handler {
	return rateLimit(a, ClientKey, skip)
}

// RateLimitUser limits by authenticated user; mount it after Authenticate.
func RateLimitUser(a Allower) func(http.Handler) http.Handler {
	return rateLimit(a, func(r *http.Request) string { return UserIDFromContext(r.Context()) }, nil)
}

func rateLimit(a Allower, key func(*http.Request) string, skip []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range skip {
				if r.URL.Path == p {
					next.ServeHTTP(w, r)
					return
				}
			}
			if !a.Allow(key(r)) {
				w.Header().Set("Retry-After", "1")
				jsonError(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// OnePerUser allows a single in-flight request per authenticated user (429 otherwise),
// for long, paid streams. Mount it after Authenticate.
func OnePerUser() func(http.Handler) http.Handler {
	var mu sync.Mutex
	inFlight := map[string]struct{}{}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := UserIDFromContext(r.Context())
			mu.Lock()
			if _, busy := inFlight[user]; busy {
				mu.Unlock()
				jsonError(w, "a request is already in progress", http.StatusTooManyRequests)
				return
			}
			inFlight[user] = struct{}{}
			mu.Unlock()
			defer func() {
				mu.Lock()
				delete(inFlight, user)
				mu.Unlock()
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ClientKey is the rate-limit key for the caller: X-Real-IP (Caddy), else the first
// X-Forwarded-For hop, else the socket peer. Headers must parse as IPs and IPv6 is
// masked to its /64, so a client can't mint unlimited buckets by varying the value.
func ClientKey(r *http.Request) string {
	if ip, ok := parseIP(r.Header.Get("X-Real-IP")); ok {
		return limiterKey(ip)
	}
	first, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ",")
	if ip, ok := parseIP(first); ok {
		return limiterKey(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip, ok := parseIP(host); ok {
		return limiterKey(ip)
	}
	return host
}

func parseIP(s string) (netip.Addr, bool) {
	ip, err := netip.ParseAddr(strings.TrimSpace(s))
	return ip.Unmap(), err == nil
}

func limiterKey(ip netip.Addr) string {
	if ip.Is6() {
		if p, err := ip.Prefix(64); err == nil {
			return p.String()
		}
	}
	return ip.String()
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
