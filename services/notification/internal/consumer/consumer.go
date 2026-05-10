// Package consumer handles Kafka task events and dispatches notifications.
package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/fsociety/todo/pkg/events"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	"github.com/fsociety/todo/services/notification/internal/mailer"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Handler consumes task.events.v1, sends email, and publishes to Redis pub/sub.
type Handler struct {
	mailer *mailer.Mailer
	redis  *redis.Client
	// notifyEmail is called to resolve a user_id → email address.
	// In this phase we use a stub; Phase 3 will wire auth-service lookup.
	notifyEmail func(userID string) string
	log         *slog.Logger
}

func New(m *mailer.Mailer, rdb *redis.Client, log *slog.Logger) *Handler {
	return &Handler{
		mailer:      m,
		redis:       rdb,
		notifyEmail: stubEmail,
		log:         log,
	}
}

// Handle satisfies pkgkafka.Handler.
func (h *Handler) Handle(ctx context.Context, r *kgo.Record) error {
	var ev events.TaskEvent
	if err := json.Unmarshal(r.Value, &ev); err != nil {
		// Poison pill — log and skip; don't block the consumer.
		h.log.Error("decode task event", "err", err)
		return nil
	}

	// Only notify on task.completed; ignore created/updated/deleted for now.
	// Phase 3 will add richer dispatch logic.
	if ev.EventType != events.TypeTaskCompleted {
		return nil
	}

	subject := fmt.Sprintf("[todo] Task completed: %s", ev.TaskTitle)
	body := fmt.Sprintf("Great work! You completed task \"%s\" at %s.", ev.TaskTitle, ev.OccurredAt.Format("2006-01-02 15:04"))

	email := h.notifyEmail(ev.UserID)
	if err := h.mailer.Send(email, subject, body); err != nil {
		h.log.Error("send email", "user_id", ev.UserID, "err", err)
		// don't fail — Redis pub/sub should still fire
	}

	// Publish to Redis channel user:<id> so the gateway WebSocket picks it up.
	payload, _ := json.Marshal(ev)
	channel := fmt.Sprintf("user:%s", ev.UserID)
	if err := h.redis.Publish(ctx, channel, payload).Err(); err != nil {
		return fmt.Errorf("redis publish: %w", err)
	}

	h.log.Info("notification dispatched", "user_id", ev.UserID, "task", ev.TaskTitle)
	return nil
}

// Run starts the Kafka consumer loop until ctx is cancelled.
func Run(ctx context.Context, brokers []string, consumer *pkgkafka.Consumer, h *Handler) error {
	return consumer.Run(ctx, h.Handle)
}

func stubEmail(userID string) string {
	// In dev, all notifications go to the catch-all Mailpit inbox.
	return "dev@todo.local"
}
