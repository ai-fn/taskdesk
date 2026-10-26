package middleware

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// RoutePattern returns the chi pattern that served r; call after the handler ran.
func RoutePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		return rc.RoutePattern()
	}
	return ""
}
