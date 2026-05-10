package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

var upgrader = websocket.Upgrader{
	HandshakeTimeout: 5 * time.Second,
	CheckOrigin: func(r *http.Request) bool {
		// Same-origin enforced by Caddy; allow all here.
		return true
	},
}

// WSHandler upgrades HTTP to WebSocket and forwards Redis pub/sub messages
// for the authenticated user. It expects the user_id to be set in the request
// context by the Authenticate middleware (key: "user_id").
type WSHandler struct {
	redis *redis.Client
	log   *slog.Logger
}

func NewWSHandler(rdb *redis.Client, log *slog.Logger) *WSHandler {
	return &WSHandler{redis: rdb, log: log}
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
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	channel := "user:" + userID
	sub := h.redis.Subscribe(ctx, channel)
	defer sub.Close()

	// Detect client disconnect via a read pump in a goroutine.
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	h.log.Info("ws connected", "user_id", userID, "channel", channel)

	msgCh := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(msg.Payload)); err != nil {
				h.log.Error("ws write", "err", err)
				return
			}
		}
	}
}

