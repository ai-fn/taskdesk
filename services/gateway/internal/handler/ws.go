package handler

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/fsociety/todo/services/gateway/internal/ws"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	HandshakeTimeout: 5 * time.Second,
	CheckOrigin: func(r *http.Request) bool {
		// Same-origin enforced by Caddy; allow all here.
		return true
	},
}

// WSHandler upgrades HTTP to WebSocket and hands the connection to the per-pod
// Hub, which fans out Redis pub/sub notifications for the authenticated user.
// It expects user_id in the request context (set by Authenticate).
type WSHandler struct {
	hub *ws.Hub
	log *slog.Logger
}

func NewWSHandler(hub *ws.Hub, log *slog.Logger) *WSHandler {
	return &WSHandler{hub: hub, log: log}
}

func (h *WSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Error("ws upgrade", "err", err)
		return
	}

	h.log.Info("ws connected", "user_id", userID)
	// Serve blocks until the peer disconnects (or returns immediately if the hub
	// is shutting down). It owns conn's lifecycle from here.
	h.hub.Serve(conn, userID, h.log)
}
