// Package consumer handles Kafka events and dispatches notifications.
package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"text/template"
	"time"

	"github.com/fsociety/todo/pkg/events"
	pki18n "github.com/fsociety/todo/pkg/i18n"
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
	mailer sender
	redis  publisher
	log    *slog.Logger
}

// sender and publisher are the parts of *mailer.Mailer and *redis.Client used here; faked in tests.
type sender interface {
	Send(to, subject, body string) error
}

type publisher interface {
	Publish(ctx context.Context, channel string, message any) *redis.IntCmd
}

func New(m *mailer.Mailer, rdb *redis.Client, log *slog.Logger) *Handler {
	return &Handler{mailer: m, redis: rdb, log: log}
}

// sendTo mails a verified address stamped on the event by its producer; empty means
// the user has none, so only the in-app notification goes out.
func (h *Handler) sendTo(email, userID, subject, body string) {
	if email == "" {
		h.log.Info("no verified email, skipping email", "user_id", userID)
		return
	}
	if err := h.mailer.Send(email, subject, body); err != nil {
		h.log.Error("send email", "user_id", userID, "err", err)
	}
}

// Topics returns the list of topics this handler needs to subscribe to.
func Topics() []string {
	return []string{events.Topic, DailyPlanTopic, events.AuthEmailTopic}
}

// Handle dispatches based on topic.
func (h *Handler) Handle(ctx context.Context, r *kgo.Record) error {
	switch r.Topic {
	case events.Topic:
		return h.handleTaskEvent(ctx, r)
	case DailyPlanTopic:
		return h.handleDailyPlan(ctx, r)
	case events.AuthEmailTopic:
		return h.handleAuthEmail(ctx, r)
	default:
		h.log.Warn("unknown topic", "topic", r.Topic)
		return nil
	}
}

// ── task events ───────────────────────────────────────────────────────────────

// taskCompletedTmpl holds localized subject+body templates for task.completed.
// Dates use ISO format (2006-01-02 15:04) — locale-neutral and readable in both EN/ZH.
// Adding a new locale: add an entry here and in pkg/i18n.
type taskCompletedTmpl struct {
	Subject *template.Template
	Body    *template.Template
}

var taskCompletedTemplates = map[string]taskCompletedTmpl{
	"en": {
		Subject: template.Must(template.New("").Parse(`[todo] Task completed: {{.Title}}`)),
		Body:    template.Must(template.New("").Parse("Great work! You completed task \"{{.Title}}\" at {{.Date}}.")),
	},
	"zh": {
		Subject: template.Must(template.New("").Parse(`[todo] 任务已完成：{{.Title}}`)),
		Body:    template.Must(template.New("").Parse(`做得好！您于 {{.Date}} 完成了任务「{{.Title}}」。`)),
	},
}

func pickTaskCompletedTmpl(lang string) taskCompletedTmpl {
	if t, ok := taskCompletedTemplates[pki18n.Normalize(lang)]; ok {
		return t
	}
	return taskCompletedTemplates["en"]
}

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

	tmpl := pickTaskCompletedTmpl(ev.Language)
	data := map[string]string{
		"Title": ev.TaskTitle,
		"Date":  ev.OccurredAt.Format("2006-01-02 15:04"),
	}

	var subjectBuf, bodyBuf bytes.Buffer
	_ = tmpl.Subject.Execute(&subjectBuf, data)
	_ = tmpl.Body.Execute(&bodyBuf, data)

	h.sendTo(ev.Email, ev.UserID, subjectBuf.String(), bodyBuf.String())

	// Push to Redis pub/sub so gateway WebSocket picks it up.
	ev.Email = "" // the browser doesn't need it
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
// Language is denormalized by the producer (ai-service) so we can localize
// emails without a synchronous auth lookup.
type DailyPlanEvent struct {
	EventID     string          `json:"event_id"`
	UserID      string          `json:"user_id"`
	PlanID      string          `json:"plan_id"`
	Summary     string          `json:"summary"`
	Items       []DailyPlanItem `json:"items"`
	GeneratedAt time.Time       `json:"generated_at"`
	Language    string          `json:"language"`
	Email       string          `json:"email"` // verified address; empty = no email
}

type DailyPlanItem struct {
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	Rank      int    `json:"rank"`
	Rationale string `json:"rationale"`
}

// dailyPlanTmpl holds localized subject+body-header templates.
type dailyPlanTmpl struct {
	Subject    *template.Template
	BodyHeader *template.Template
	Footer     string
}

var dailyPlanTemplates = map[string]dailyPlanTmpl{
	"en": {
		Subject:    template.Must(template.New("").Parse(`[todo] Your daily plan is ready — {{.Date}}`)),
		BodyHeader: template.Must(template.New("").Parse("Your AI-powered daily plan for {{.Date}}:\n\n{{.Summary}}\n\nTop tasks:\n")),
		Footer:     "\nOpen the app to see the full plan.",
	},
	"zh": {
		Subject:    template.Must(template.New("").Parse(`[todo] 您的每日计划已就绪 — {{.Date}}`)),
		BodyHeader: template.Must(template.New("").Parse("您 {{.Date}} 的 AI 每日计划：\n\n{{.Summary}}\n\n重点任务：\n")),
		Footer:     "\n打开应用查看完整计划。",
	},
}

func pickDailyPlanTmpl(lang string) dailyPlanTmpl {
	if t, ok := dailyPlanTemplates[pki18n.Normalize(lang)]; ok {
		return t
	}
	return dailyPlanTemplates["en"]
}

func (h *Handler) handleDailyPlan(ctx context.Context, r *kgo.Record) error {
	var ev DailyPlanEvent
	if err := json.Unmarshal(r.Value, &ev); err != nil {
		h.log.Error("decode daily plan event", "err", err)
		return nil
	}

	subject, body := buildPlanEmail(ev)
	h.sendTo(ev.Email, ev.UserID, subject, body)

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

func buildPlanEmail(ev DailyPlanEvent) (subject, body string) {
	tmpl := pickDailyPlanTmpl(ev.Language)
	data := map[string]string{
		"Date":    ev.GeneratedAt.Format("2006-01-02"),
		"Summary": ev.Summary,
	}

	var subjectBuf, bodyBuf bytes.Buffer
	_ = tmpl.Subject.Execute(&subjectBuf, data)
	_ = tmpl.BodyHeader.Execute(&bodyBuf, data)

	for _, item := range ev.Items {
		if item.Rank > 5 {
			break
		}
		fmt.Fprintf(&bodyBuf, "  %d. %s — %s\n", item.Rank, item.TaskTitle, item.Rationale)
	}
	bodyBuf.WriteString(tmpl.Footer)

	return subjectBuf.String(), bodyBuf.String()
}

// ── auth email events ─────────────────────────────────────────────────────────

type authEmailTmpl struct {
	Subject *template.Template
	Body    *template.Template
}

var passwordResetTemplates = map[string]authEmailTmpl{
	"en": {
		Subject: template.Must(template.New("").Parse(`[todo] Your password reset code`)),
		Body: template.Must(template.New("").Parse(
			"Your password reset code is: {{.Code}}\n\nThis code expires in {{.ExpiresMins}} minutes.\n\nIf you did not request this, you can safely ignore this email.",
		)),
	},
	"zh": {
		Subject: template.Must(template.New("").Parse(`[todo] 您的密码重置验证码`)),
		Body: template.Must(template.New("").Parse(
			"您的密码重置验证码是：{{.Code}}\n\n该验证码将在 {{.ExpiresMins}} 分钟后失效。\n\n如果您未发起此请求，请忽略此邮件。",
		)),
	},
}

var emailVerifyTemplates = map[string]authEmailTmpl{
	"en": {
		Subject: template.Must(template.New("").Parse(`[todo] Verify your email address`)),
		Body: template.Must(template.New("").Parse(
			"Your email verification code is: {{.Code}}\n\nThis code expires in {{.ExpiresMins}} minutes.",
		)),
	},
	"zh": {
		Subject: template.Must(template.New("").Parse(`[todo] 验证您的邮箱地址`)),
		Body: template.Must(template.New("").Parse(
			"您的邮箱验证码是：{{.Code}}\n\n该验证码将在 {{.ExpiresMins}} 分钟后失效。",
		)),
	},
}

func pickAuthEmailTmpl(purpose, lang string) authEmailTmpl {
	var m map[string]authEmailTmpl
	switch purpose {
	case "email_verify":
		m = emailVerifyTemplates
	default:
		m = passwordResetTemplates
	}
	if t, ok := m[pki18n.Normalize(lang)]; ok {
		return t
	}
	return m["en"]
}

func (h *Handler) handleAuthEmail(ctx context.Context, r *kgo.Record) error {
	var ev events.AuthEmailEvent
	if err := json.Unmarshal(r.Value, &ev); err != nil {
		h.log.Error("decode auth email event", "err", err)
		return nil // poison pill — skip
	}

	tmpl := pickAuthEmailTmpl(ev.Purpose, ev.Language)
	data := map[string]any{
		"Code":        ev.Code,
		"ExpiresMins": ev.ExpiresMins,
	}

	var subjectBuf, bodyBuf bytes.Buffer
	_ = tmpl.Subject.Execute(&subjectBuf, data)
	_ = tmpl.Body.Execute(&bodyBuf, data)

	if err := h.mailer.Send(ev.Email, subjectBuf.String(), bodyBuf.String()); err != nil {
		h.log.Error("send auth email", "user_id", ev.UserID, "purpose", ev.Purpose, "err", err)
	}

	h.log.Info("auth email dispatched", "user_id", ev.UserID, "purpose", ev.Purpose)
	return nil
}

// Run starts the Kafka consumer loop until ctx is cancelled.
func Run(ctx context.Context, brokers []string, consumer *pkgkafka.Consumer, h *Handler) error {
	return consumer.Run(ctx, h.Handle)
}
