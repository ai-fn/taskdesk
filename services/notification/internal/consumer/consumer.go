// Package consumer handles Kafka events and dispatches notifications.
package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/fsociety/todo/pkg/events"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	"github.com/fsociety/todo/services/notification/internal/mailer"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
)

// DailyPlanTopic is the Kafka topic published by ai-service.
const DailyPlanTopic = "daily-plan.generated.v1"

// Handler consumes task.events.v1 and daily-plan.generated.v1, sends email,
// and publishes in-app notifications to Redis pub/sub.
type Handler struct {
	mailer      *mailer.Mailer
	redis       *redis.Client
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

// Topics returns the list of topics this handler needs to subscribe to.
func Topics() []string {
	return []string{events.Topic, DailyPlanTopic}
}

// Handle dispatches based on topic.
func (h *Handler) Handle(ctx context.Context, r *kgo.Record) error {
	switch r.Topic {
	case events.Topic:
		return h.handleTaskEvent(ctx, r)
	case DailyPlanTopic:
		return h.handleDailyPlan(ctx, r)
	default:
		h.log.Warn("unknown topic", "topic", r.Topic)
		return nil
	}
}

// ── task events ───────────────────────────────────────────────────────────────

func (h *Handler) handleTaskEvent(ctx context.Context, r *kgo.Record) error {
	var ev events.TaskEvent
	if err := json.Unmarshal(r.Value, &ev); err != nil {
		h.log.Error("decode task event", "err", err)
		return nil // poison pill — skip
	}

	// Only notify on task.completed; ignore created/updated/deleted for now.
	if ev.EventType != events.TypeTaskCompleted {
		return nil
	}

	subject := fmt.Sprintf("[todo] Task completed: %s", ev.TaskTitle)
	body := fmt.Sprintf("Great work! You completed task \"%s\" at %s.", ev.TaskTitle, ev.OccurredAt.Format("2006-01-02 15:04"))

	email := h.notifyEmail(ev.UserID)
	if err := h.mailer.Send(email, subject, body); err != nil {
		h.log.Error("send email", "user_id", ev.UserID, "err", err)
	}

	// Push to Redis pub/sub so gateway WebSocket picks it up.
	payload, _ := json.Marshal(ev)
	channel := fmt.Sprintf("user:%s", ev.UserID)
	if err := h.redis.Publish(ctx, channel, payload).Err(); err != nil {
		return fmt.Errorf("redis publish: %w", err)
	}

	h.log.Info("task notification dispatched", "user_id", ev.UserID, "task", ev.TaskTitle)
	return nil
}

// ── daily plan events ─────────────────────────────────────────────────────────

// DailyPlanEvent mirrors the payload published by ai-service.
type DailyPlanEvent struct {
	EventID     string          `json:"event_id"`
	UserID      string          `json:"user_id"`
	PlanID      string          `json:"plan_id"`
	Summary     string          `json:"summary"`
	Items       []DailyPlanItem `json:"items"`
	GeneratedAt time.Time       `json:"generated_at"`
}

type DailyPlanItem struct {
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	Rank      int    `json:"rank"`
	Rationale string `json:"rationale"`
}

func (h *Handler) handleDailyPlan(ctx context.Context, r *kgo.Record) error {
	var ev DailyPlanEvent
	if err := json.Unmarshal(r.Value, &ev); err != nil {
		h.log.Error("decode daily plan event", "err", err)
		return nil
	}

	// Email notification with today's plan.
	email := h.notifyEmail(ev.UserID)
	subject := fmt.Sprintf("[todo] Your daily plan is ready — %s", ev.GeneratedAt.Format("2006-01-02"))
	body := buildPlanEmail(ev)
	if err := h.mailer.Send(email, subject, body); err != nil {
		h.log.Error("send plan email", "user_id", ev.UserID, "err", err)
	}

	// Push to Redis pub/sub so the SPA shows an in-app notification.
	type wsNotification struct {
		Type    string `json:"type"`
		PlanID  string `json:"plan_id"`
		Summary string `json:"summary"`
	}
	payload, _ := json.Marshal(wsNotification{
		Type:    "daily_plan_ready",
		PlanID:  ev.PlanID,
		Summary: ev.Summary,
	})
	channel := fmt.Sprintf("user:%s", ev.UserID)
	if err := h.redis.Publish(ctx, channel, payload).Err(); err != nil {
		return fmt.Errorf("redis publish daily plan: %w", err)
	}

	h.log.Info("daily plan notification dispatched", "user_id", ev.UserID, "plan_id", ev.PlanID)
	return nil
}

func buildPlanEmail(ev DailyPlanEvent) string {
	body := fmt.Sprintf("Your AI-powered daily plan for %s:\n\n%s\n\nTop tasks:\n", ev.GeneratedAt.Format("2006-01-02"), ev.Summary)
	for _, item := range ev.Items {
		if item.Rank > 5 {
			break
		}
		body += fmt.Sprintf("  %d. %s — %s\n", item.Rank, item.TaskTitle, item.Rationale)
	}
	body += "\nOpen the app to see the full plan."
	return body
}

// Run starts the Kafka consumer loop until ctx is cancelled.
func Run(ctx context.Context, brokers []string, consumer *pkgkafka.Consumer, h *Handler) error {
	return consumer.Run(ctx, h.Handle)
}

func stubEmail(userID string) string {
	return "dev@todo.local"
}
