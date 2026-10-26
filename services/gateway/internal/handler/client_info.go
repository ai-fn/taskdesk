package handler

import (
	"net/http"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
)

// clientInfoFromRequest extracts the client IP and User-Agent. Caddy forwards the real
// client IP as X-Real-IP; values that aren't IPs are ignored (the column is INET).
func clientInfoFromRequest(r *http.Request) *authv1.ClientInfo {
	var ip string
	if addr, ok := mw.ClientIP(r); ok {
		ip = addr.String()
	}
	return &authv1.ClientInfo{
		IpAddress: ip,
		UserAgent: r.UserAgent(),
	}
}
