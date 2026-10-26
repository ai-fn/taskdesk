package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/events"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
)

type sentMail struct{ to, subject, body string }

type fakeSender struct {
	sent []sentMail
	err  error
}

func (f *fakeSender) Send(to, subject, body string) error {
	f.sent = append(f.sent, sentMail{to, subject, body})
	return f.err
}

type published struct {
	channel string
	payload string
}

type fakePublisher struct{ got []published }

func (f *fakePublisher) Publish(ctx context.Context, channel string, message any) *redis.IntCmd {
	f.got = append(f.got, published{channel, string(message.([]byte))})
	return redis.NewIntCmd(ctx)
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

func TestTaskCompleted_MailFailureStillNotifiesInApp(t *testing.T) {
	h, mail, ps := newHandler()
	mail.err = errors.New("smtp down")
	if err := h.Handle(context.Background(), record(t, events.Topic, completed("alice@example.com"))); err != nil {
		t.Fatalf("Handle = %v, want nil so the record is not retried and re-published", err)
	}
	if len(ps.got) != 1 {
		t.Errorf("redis publishes = %d, want 1", len(ps.got))
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
