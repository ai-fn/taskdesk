package ws

import (
	"log/slog"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// writeWait bounds a single write; without it gorilla buffers grow unbounded
	// behind a stuck client.
	writeWait = 10 * time.Second
	// pongWait is how long we wait for a pong before declaring the peer dead.
	pongWait = 60 * time.Second
	// pingInterval must be < pongWait so a ping always precedes the deadline.
	pingInterval = (pongWait * 9) / 10
	// readLimit caps inbound frames; clients send nothing meaningful, so keep it
	// small to bound memory and reject abuse.
	readLimit = 512
	// sendBuffer is per-client outbound capacity. A burst beyond this marks the
	// client as slow and triggers eviction (see registry.route).
	sendBuffer = 32
)

// client is one WebSocket connection. Its send channel is the seam between the
// hub (single producer) and the writePump (single consumer): gorilla requires a
// single writer, so all writes go through writePump.
type client struct {
	conn   *websocket.Conn
	userID string
	send   chan []byte
	log    *slog.Logger
}

func newClient(conn *websocket.Conn, userID string, log *slog.Logger) *client {
	return &client{
		conn:   conn,
		userID: userID,
		send:   make(chan []byte, sendBuffer),
		log:    log,
	}
}

// Serve attaches an upgraded WebSocket connection to the hub for userID and
// blocks until the connection closes. It returns immediately (closing conn) if
// the hub is shutting down. This is the package's entry point for handlers, so
// the client type and its pumps can stay unexported.
func (h *Hub) Serve(conn *websocket.Conn, userID string, log *slog.Logger) {
	c := newClient(conn, userID, log)
	if !h.Register(c) {
		_ = conn.Close()
		return
	}
	go c.writePump(h)
	c.readPump(h) // blocks until the peer disconnects
}

// writePump is the sole writer for the connection. It drains the send channel,
// emits periodic pings, and exits when the hub closes send (eviction/shutdown)
// or a write fails. On exit it closes the connection (unblocking readPump),
// unregisters (idempotent), and signals the hub's drain WaitGroup.
//
// Precondition: only started after Hub.Register returned true, so the matching
// wg.Done here keeps the hub's WaitGroup balanced.
func (c *client) writePump(h *Hub) {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
		h.unregister(c)
		h.wg.Done()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel: send a clean close frame and stop.
				_ = c.conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump reads from the connection only to detect disconnects and keep the
// read deadline fresh via pong handling. Inbound application messages are
// ignored (the protocol is server→client only). It blocks until the peer
// disconnects, then unregisters the client.
func (c *client) readPump(h *Hub) {
	defer h.unregister(c)
	c.conn.SetReadLimit(readLimit)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}
