package consumer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/events"
	"github.com/redis/go-redis/v9"
)

type fakeDone struct {
	keys      map[string]time.Duration
	existsErr error
	setErr    error
}

func (f *fakeDone) Exists(ctx context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)
	if f.existsErr != nil {
		cmd.SetErr(f.existsErr)
		return cmd
	}
	for _, k := range keys {
		if _, ok := f.keys[k]; ok {
			cmd.SetVal(1)
		}
	}
	return cmd
}

func (f *fakeDone) Set(ctx context.Context, key string, _ any, ttl time.Duration) *redis.StatusCmd {
	if f.keys == nil {
		f.keys = map[string]time.Duration{}
	}
	f.keys[key] = ttl
	cmd := redis.NewStatusCmd(ctx)
	cmd.SetErr(f.setErr)
	return cmd
}

func TestRedeliveredEventsDoNotMailTwice(t *testing.T) {
	ctx := context.Background()
	task := completed("a@example.com")
	task.EventID = "ev-task"
	auth := events.AuthEmailEvent{EventID: "ev-auth", UserID: "u1", Email: "a@example.com", Purpose: events.PurposePasswordReset, Code: "123456", Language: "en", OccurredAt: time.Now()}
	plan := DailyPlanEvent{EventID: "ev-plan", UserID: "u1", PlanID: "p1", Email: "a@example.com", Language: "en", GeneratedAt: time.Now()}

	for _, tc := range []struct {
		name  string
		topic string
		ev    any
	}{
		{"task.completed", events.Topic, task},
		{"auth email", events.AuthEmailTopic, auth},
		{"daily plan", DailyPlanTopic, plan},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, mail, _ := newHandler()
			done := &fakeDone{}
			h.done = done
			for i := 0; i < 3; i++ {
				if err := h.Handle(ctx, record(t, tc.topic, tc.ev)); err != nil {
					t.Fatal(err)
				}
			}
			if len(mail.sent) != 1 {
				t.Errorf("sent %d mails for one event delivered 3 times, want 1", len(mail.sent))
			}
			for k, ttl := range done.keys {
				if ttl <= 0 {
					t.Errorf("dedupe key %s has no TTL", k)
				}
			}
		})
	}
}

func TestFailedSendIsNotMarkedDone(t *testing.T) {
	h, mail, _ := newHandler()
	h.done = &fakeDone{}
	mail.err = errors.New("smtp down")
	ev := completed("a@example.com")
	ev.EventID = "ev-1"
	if err := h.Handle(context.Background(), record(t, events.Topic, ev)); err == nil {
		t.Fatal("want the send error returned for retry")
	}
	mail.err = nil
	if err := h.Handle(context.Background(), record(t, events.Topic, ev)); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 2 { // first attempt (failed) + retry
		t.Errorf("sends = %d, want 2: the retry must still go out", len(mail.sent))
	}
}

func TestDedupeFailsOpenWhenRedisIsDown(t *testing.T) {
	h, mail, _ := newHandler()
	h.done = &fakeDone{existsErr: errors.New("redis down")}
	ev := completed("a@example.com")
	ev.EventID = "ev-1"
	if err := h.Handle(context.Background(), record(t, events.Topic, ev)); err != nil {
		t.Fatal(err)
	}
	if len(mail.sent) != 1 {
		t.Errorf("sent %d, want 1: a dedupe outage must not drop mail", len(mail.sent))
	}
}

func TestDifferentEventsAreBothMailed(t *testing.T) {
	h, mail, _ := newHandler()
	done := &fakeDone{}
	h.done = done
	for _, id := range []string{"ev-a", "ev-b"} {
		ev := completed("a@example.com")
		ev.EventID = id
		if err := h.Handle(context.Background(), record(t, events.Topic, ev)); err != nil {
			t.Fatal(err)
		}
	}
	if len(mail.sent) != 2 {
		t.Errorf("sent %d, want 2: distinct event ids must not collide", len(mail.sent))
	}
	for _, id := range []string{"ev-a", "ev-b"} {
		if _, ok := done.keys["notif:done:task:"+id]; !ok {
			t.Errorf("no dedupe key for %s in %v", id, done.keys)
		}
	}
}

func TestEventsWithoutAnIDAreNeverDeduped(t *testing.T) {
	h, mail, _ := newHandler()
	done := &fakeDone{}
	h.done = done
	for i := 0; i < 2; i++ {
		if err := h.Handle(context.Background(), record(t, events.Topic, completed("a@example.com"))); err != nil {
			t.Fatal(err)
		}
	}
	if len(mail.sent) != 2 || len(done.keys) != 0 {
		t.Errorf("sent %d, keys %v; want 2 mails and no keys for id-less events", len(mail.sent), done.keys)
	}
}

func TestRedeliveryDoesNotRepeatTheToast(t *testing.T) {
	h, _, ps := newHandler()
	h.done = &fakeDone{}
	ev := completed("a@example.com")
	ev.EventID = "ev-1"
	for i := 0; i < 2; i++ {
		if err := h.Handle(context.Background(), record(t, events.Topic, ev)); err != nil {
			t.Fatal(err)
		}
	}
	if len(ps.got) != 1 {
		t.Errorf("publishes = %d, want 1", len(ps.got))
	}
}

func TestPublishFailureAfterMailingDoesNotRetryTheMail(t *testing.T) {
	h, mail, ps := newHandler()
	ps.err = errors.New("redis down")
	h.done = &fakeDone{}
	ev := completed("a@example.com")
	ev.EventID = "ev-1"
	if err := h.Handle(context.Background(), record(t, events.Topic, ev)); err != nil {
		t.Fatalf("Handle = %v, want nil: retrying would re-send the mail", err)
	}
	if len(mail.sent) != 1 {
		t.Errorf("sent %d, want 1", len(mail.sent))
	}
}

func TestPublishFailureWithoutMailIsRetried(t *testing.T) {
	h, _, ps := newHandler()
	ps.err = errors.New("redis down")
	if err := h.Handle(context.Background(), record(t, events.Topic, completed(""))); err == nil {
		t.Error("Handle = nil, want the publish error so the toast is retried")
	}
}

func TestMarkFailureStillSucceeds(t *testing.T) {
	h, mail, _ := newHandler()
	h.done = &fakeDone{setErr: errors.New("redis down")}
	ev := completed("a@example.com")
	ev.EventID = "ev-1"
	if err := h.Handle(context.Background(), record(t, events.Topic, ev)); err != nil {
		t.Fatalf("Handle = %v, want nil: a lost mark only risks one duplicate", err)
	}
	if len(mail.sent) != 1 {
		t.Errorf("sent %d, want 1", len(mail.sent))
	}
}

func TestFailedSendIsNotMarkedForAuthAndPlan(t *testing.T) {
	for _, tc := range []struct {
		name, topic string
		ev          any
	}{
		{"auth", events.AuthEmailTopic, events.AuthEmailEvent{EventID: "ev-1", UserID: "u1", Email: "a@example.com", Purpose: events.PurposePasswordReset, Code: "1", Language: "en", OccurredAt: time.Now()}},
		{"plan", DailyPlanTopic, DailyPlanEvent{EventID: "ev-1", UserID: "u1", PlanID: "p1", Email: "a@example.com", Language: "en", GeneratedAt: time.Now()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, mail, _ := newHandler()
			done := &fakeDone{}
			h.done = done
			mail.err = errors.New("smtp down")
			if err := h.Handle(context.Background(), record(t, tc.topic, tc.ev)); err == nil {
				t.Fatal("want the send error returned")
			}
			if len(done.keys) != 0 {
				t.Errorf("a failed send was marked done: %v", done.keys)
			}
		})
	}
}
