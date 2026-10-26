// Package consumer handles Kafka events and dispatches notifications.
package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	done   doneStore // nil = no dedupe
	log    *slog.Logger
	now    func() time.Time // nil = time.Now

	scanner      ScannerClient // nil = skip scan (SCAN_DISABLED, dev only)
	blob         BlobGetter
	scanUpdater  ScanResultUpdater
	scanMaxBytes int64 // objects above this are quarantined unscanned; 0 = no limit
}

// sender and publisher are the parts of *mailer.Mailer and *redis.Client used here; faked in tests.
type sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

type publisher interface {
	Publish(ctx context.Context, channel string, message any) *redis.IntCmd
}

// doneStore remembers which events were fully handled; the *redis.Client satisfies it.
type doneStore interface {
	Exists(ctx context.Context, keys ...string) *redis.IntCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
}

// doneTTL outlasts any realistic redelivery (rebalance, retry, short replay) and bounds key growth.
const doneTTL = 48 * time.Hour

func New(m *mailer.Mailer, rdb *redis.Client, scanner ScannerClient, blob BlobGetter, scanUpdater ScanResultUpdater, log *slog.Logger) *Handler {
	return &Handler{mailer: m, redis: rdb, done: rdb, scanner: scanner, blob: blob, scanUpdater: scanUpdater, log: log}
}

// alreadyDone reports whether event id was handled before. Delivery is at-least-once, so a redelivered
// event must not re-mail. A Redis failure fails open: a duplicate email beats a stuck consumer.
func (h *Handler) alreadyDone(ctx context.Context, kind, id string) bool {
	if h.done == nil || id == "" {
		return false
	}
	n, err := h.done.Exists(ctx, "notif:done:"+kind+":"+id).Result()
	if err != nil {
		h.log.Warn("dedupe lookup failed, handling anyway", "err", err)
		return false
	}
	return n > 0
}

// markDone records a fully handled event. Marking after the side effects means a crash in between
// re-sends once; marking before would lose the email.
func (h *Handler) markDone(ctx context.Context, kind, id string) {
	if h.done == nil || id == "" {
		return
	}
	if err := h.done.Set(ctx, "notif:done:"+kind+":"+id, 1, doneTTL).Err(); err != nil {
		h.log.Warn("dedupe mark failed", "err", err)
	}
}

func (h *Handler) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// sendTo mails a verified address stamped on the event by its producer; empty means
// the user has none, so only the in-app notification goes out. A transient failure is
// returned for retry; a refused address is dropped, as no retry can deliver it.
func (h *Handler) sendTo(ctx context.Context, email, userID, subject, body string) error {
	if email == "" {
		h.log.Info("no verified email, skipping email", "user_id", userID)
		return nil
	}
	err := h.mailer.Send(ctx, email, subject, body)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mailer.ErrRecipientRejected):
		h.log.Warn("email address refused, dropping email", "user_id", userID, "err", err)
		return nil
	default:
		return fmt.Errorf("send email: %w", err)
	}
}

// Topics are the task and plan topics, consumed in the main consumer group.
func Topics() []string {
	return []string{events.Topic, DailyPlanTopic}
}

// AuthTopics are the OTP topics; their own group keeps slow task traffic from delaying codes.
func AuthTopics() []string {
	return []string{events.AuthEmailTopic}
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
	case events.FileTopic:
		return h.handleFileUploaded(ctx, r)
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
	if h.alreadyDone(ctx, "task", ev.EventID) {
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

	// Mail first: a failed send retries the record, which must not re-push the toast each time.
	if err := h.sendTo(ctx, ev.Email, ev.UserID, subjectBuf.String(), bodyBuf.String()); err != nil {
		return err
	}
	sentMail := ev.Email != ""

	// Push to Redis pub/sub so gateway WebSocket picks it up.
	ev.Email = "" // the browser doesn't need it
	payload, _ := json.Marshal(ev)
	channel := fmt.Sprintf("user:%s", ev.UserID)
	if err := h.redis.Publish(ctx, channel, payload).Err(); err != nil {
		if sentMail {
			// The toast is ephemeral; retrying would re-send the mail each time.
			h.log.Warn("redis publish failed after mailing, not retrying", "user_id", ev.UserID, "err", err)
		} else {
			return fmt.Errorf("redis publish: %w", err)
		}
	}
	if sentMail { // nothing but an ephemeral toast to protect otherwise
		h.markDone(ctx, "task", ev.EventID)
	}
	h.log.Info("task notification dispatched", "user_id", ev.UserID, "task_id", ev.TaskID)
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

	if h.alreadyDone(ctx, "plan", ev.EventID) {
		return nil
	}
	subject, body := buildPlanEmail(ev)
	if err := h.sendTo(ctx, ev.Email, ev.UserID, subject, body); err != nil {
		return err
	}
	sentMail := ev.Email != ""

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
		if sentMail {
			h.log.Warn("redis publish failed after mailing, not retrying", "user_id", ev.UserID, "err", err)
		} else {
			return fmt.Errorf("redis publish daily plan: %w", err)
		}
	}
	if sentMail {
		h.markDone(ctx, "plan", ev.EventID)
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

	// A code past its lifetime is useless; this also keeps a replayed topic from mailing old codes.
	if ev.ExpiresMins > 0 && !ev.OccurredAt.IsZero() && h.clock().After(ev.OccurredAt.Add(time.Duration(ev.ExpiresMins)*time.Minute)) {
		h.log.Warn("auth email expired before sending, dropping", "user_id", ev.UserID, "purpose", ev.Purpose)
		return nil
	}

	if h.alreadyDone(ctx, "auth", ev.EventID) {
		return nil
	}
	tmpl := pickAuthEmailTmpl(ev.Purpose, ev.Language)
	data := map[string]any{
		"Code":        ev.Code,
		"ExpiresMins": ev.ExpiresMins,
	}

	var subjectBuf, bodyBuf bytes.Buffer
	_ = tmpl.Subject.Execute(&subjectBuf, data)
	_ = tmpl.Body.Execute(&bodyBuf, data)

	if err := h.sendTo(ctx, ev.Email, ev.UserID, subjectBuf.String(), bodyBuf.String()); err != nil {
		return err
	}

	h.markDone(ctx, "auth", ev.EventID)
	h.log.Info("auth email dispatched", "user_id", ev.UserID, "purpose", ev.Purpose)
	return nil
}

// Run starts the Kafka consumer loop until ctx is cancelled.
func Run(ctx context.Context, brokers []string, consumer *pkgkafka.Consumer, h *Handler) error {
	return consumer.Run(ctx, h.Handle)
}
