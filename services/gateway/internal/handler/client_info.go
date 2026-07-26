package handler

import (
	"net/http"
	"strings"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
)

// clientInfoFromRequest extracts client IP and User-Agent from the HTTP request.
// Caddy forwards the real client IP as X-Real-IP.
func clientInfoFromRequest(r *http.Request) *authv1.ClientInfo {
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		// Fall back to X-Forwarded-For first hop.
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			ip = strings.SplitN(fwd, ",", 2)[0]
			ip = strings.TrimSpace(ip)
		}
	}
	if ip == "" {
		// Last resort: socket address (will be Caddy's IP in k8s, but better than nothing).
		ip, _, _ = strings.Cut(r.RemoteAddr, ":")
	}
	return &authv1.ClientInfo{
		IpAddress: ip,
		UserAgent: r.UserAgent(),
	}
}
