package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/events"
	"github.com/fsociety/todo/services/notification/internal/mailer"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
)

type sentMail struct{ to, subject, body string }

type fakeSender struct {
	sent []sentMail
	err  error
}

func (f *fakeSender) Send(_ context.Context, to, subject, body string) error {
	f.sent = append(f.sent, sentMail{to, subject, body})
	return f.err
}

type published struct {
	channel string
	payload string
}

type fakePublisher struct {
	got []published
	err error
}

func (f *fakePublisher) Publish(ctx context.Context, channel string, message any) *redis.IntCmd {
	f.got = append(f.got, published{channel, string(message.([]byte))})
	cmd := redis.NewIntCmd(ctx)
	cmd.SetErr(f.err)
	return cmd
}

func newHandler() (*Handler, *fakeSender, *fakePublisher) {
	s, p := &fakeSender{}, &fakePublisher{}
	return &Handler{mailer: s, redis: p, log: slog.New(slog.NewTextHandler(io.Discard, nil))}, s, p
}

func record(t *testing.T, topic string, v any) *kgo.Record {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return &kgo.Record{Topic: topic, Value: b}
}

func completed(email string) events.TaskEvent {
	return events.TaskEvent{EventType: events.TypeTaskCompleted, UserID: "u1", TaskID: "t1", TaskTitle: "Buy milk", OccurredAt: time.Now(), Language: "en", Email: email}
}

func TestTaskCompleted_EmailsTheEventAddress(t *testing.T) {
	h, mail, ps := newHandler()
	if err := h.Handle(context.Background(), record(t, events.Topic, completed("alice@example.com"))); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 1 || mail.sent[0].to != "alice@example.com" {
		t.Fatalf("sent = %+v, want one mail to alice@example.com", mail.sent)
	}
	if len(ps.got) != 1 || ps.got[0].channel != "user:u1" {
		t.Fatalf("redis = %+v, want one publish on user:u1", ps.got)
	}
	if strings.Contains(ps.got[0].payload, "alice@example.com") {
		t.Errorf("websocket payload leaks the email: %s", ps.got[0].payload)
	}
}

func TestTaskCompleted_NoEmailStillNotifiesInApp(t *testing.T) {
	h, mail, ps := newHandler()
	if err := h.Handle(context.Background(), record(t, events.Topic, completed(""))); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 0 {
		t.Errorf("sent = %+v, want no mail without an address", mail.sent)
	}
	if len(ps.got) != 1 {
		t.Errorf("redis publishes = %d, want 1", len(ps.got))
	}
}

func TestDailyPlan_EmailsTheEventAddress(t *testing.T) {
	h, mail, ps := newHandler()
	ev := DailyPlanEvent{UserID: "u1", PlanID: "p1", Summary: "s", GeneratedAt: time.Now(), Language: "en", Email: "bob@example.com"}
	if err := h.Handle(context.Background(), record(t, DailyPlanTopic, ev)); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 1 || mail.sent[0].to != "bob@example.com" {
		t.Fatalf("sent = %+v, want one mail to bob@example.com", mail.sent)
	}
	if len(ps.got) != 1 {
		t.Errorf("redis publishes = %d, want 1", len(ps.got))
	}

	h, mail, ps = newHandler()
	ev.Email = ""
	if err := h.Handle(context.Background(), record(t, DailyPlanTopic, ev)); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 0 || len(ps.got) != 1 {
		t.Errorf("no address: sent=%d publishes=%d, want 0 mails and 1 publish", len(mail.sent), len(ps.got))
	}
}

func TestTaskCompleted_MailFailureIsRetriedWithoutToast(t *testing.T) {
	h, mail, ps := newHandler()
	mail.err = errors.New("smtp down")
	if err := h.Handle(context.Background(), record(t, events.Topic, completed("alice@example.com"))); err == nil {
		t.Fatal("Handle = nil, want the send error so the record is retried")
	}
	if len(ps.got) != 0 {
		t.Errorf("redis publishes = %d, want 0: every retry would re-push the toast", len(ps.got))
	}
}

func TestDailyPlan_MailFailureIsRetried(t *testing.T) {
	h, mail, _ := newHandler()
	mail.err = errors.New("smtp down")
	ev := DailyPlanEvent{UserID: "u1", PlanID: "p1", GeneratedAt: time.Now(), Email: "bob@example.com"}
	if err := h.Handle(context.Background(), record(t, DailyPlanTopic, ev)); err == nil {
		t.Fatal("Handle = nil, want the send error")
	}
}

func TestRefusedAddress_DroppedNotRetried(t *testing.T) {
	refused := fmt.Errorf("smtp rcpt: %w", mailer.ErrRecipientRejected)

	h, mail, ps := newHandler()
	mail.err = refused
	if err := h.Handle(context.Background(), record(t, events.Topic, completed("gone@example.com"))); err != nil {
		t.Fatalf("task event: Handle = %v, want nil: retrying a refused address cannot succeed", err)
	}
	if len(ps.got) != 1 {
		t.Errorf("redis publishes = %d, want 1: the in-app toast must still go out", len(ps.got))
	}

	h, mail, _ = newHandler()
	mail.err = refused
	if err := h.Handle(context.Background(), record(t, events.AuthEmailTopic, authEmail(time.Now(), 15))); err != nil {
		t.Fatalf("auth email: Handle = %v, want nil", err)
	}
}

func authEmail(at time.Time, mins int) events.AuthEmailEvent {
	return events.AuthEmailEvent{UserID: "u1", Email: "alice@example.com", Purpose: events.PurposePasswordReset, Code: "123456", Language: "en", ExpiresMins: mins, OccurredAt: at}
}

func TestAuthEmail_SendFailureIsRetried(t *testing.T) {
	h, mail, _ := newHandler()
	mail.err = errors.New("smtp down")
	if err := h.Handle(context.Background(), record(t, events.AuthEmailTopic, authEmail(time.Now(), 15))); err == nil {
		t.Fatal("Handle = nil, want the send error so the OTP is not lost")
	}
}

func TestAuthEmail_ExpiredCodeIsNotSent(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		at   time.Time
		mins int
		sent int
	}{
		{"fresh", now.Add(-5 * time.Minute), 15, 1},
		{"expired", now.Add(-16 * time.Minute), 15, 0},
		{"no timestamp", time.Time{}, 15, 1},
		{"no lifetime", now.Add(-time.Hour), 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, mail, _ := newHandler()
			h.now = func() time.Time { return now }
			if err := h.Handle(context.Background(), record(t, events.AuthEmailTopic, authEmail(tc.at, tc.mins))); err != nil {
				t.Fatal(err)
			}
			if len(mail.sent) != tc.sent {
				t.Errorf("sent = %d, want %d", len(mail.sent), tc.sent)
			}
		})
	}
}

func TestTopics_AuthMailHasItsOwnGroup(t *testing.T) {
	for _, topic := range Topics() {
		if topic == events.AuthEmailTopic {
			t.Errorf("%s is in the shared topics: it would queue behind task traffic", topic)
		}
	}
	if got := AuthTopics(); len(got) != 1 || got[0] != events.AuthEmailTopic {
		t.Errorf("AuthTopics = %v", got)
	}
}

func TestTaskCompleted_PayloadWithoutEmailKey(t *testing.T) {
	h, mail, ps := newHandler()
	raw := &kgo.Record{Topic: events.Topic, Value: []byte(`{"event_type":"task.completed","user_id":"u1","task_title":"x","language":"en"}`)}
	if err := h.Handle(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 0 || len(ps.got) != 1 {
		t.Errorf("old payload: mails=%d publishes=%d, want 0 and 1", len(mail.sent), len(ps.got))
	}
}
