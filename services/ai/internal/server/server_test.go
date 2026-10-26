package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/tz"
	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNewPlanEvent_CarriesRecipient(t *testing.T) {
	dp := &aiv1.DailyPlan{Summary: "s", GeneratedAt: timestamppb.Now(), Items: []*aiv1.PlanItem{{TaskId: "t1", Rank: 1}}}

	ev := newPlanEvent("p1", "u1", "zh", "u@x.com", dp)
	if ev.Email != "u@x.com" || ev.Language != "zh" || ev.UserID != "u1" || len(ev.Items) != 1 {
		t.Errorf("event = %+v", ev)
	}
	raw, _ := json.Marshal(ev)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m["email"] != "u@x.com" {
		t.Errorf("payload email = %v, want u@x.com", m["email"])
	}

	raw, _ = json.Marshal(newPlanEvent("p1", "u1", "en", "", dp))
	m = nil
	_ = json.Unmarshal(raw, &m)
	if _, ok := m["email"]; ok {
		t.Error("empty email must be omitted from the payload")
	}
}

func TestLocalDateAndPlannerDueDates(t *testing.T) {
	sh := tz.Load("Asia/Shanghai")
	late := time.Date(2026, 5, 19, 18, 30, 0, 0, time.UTC) // 02:30 on the 20th in Shanghai
	if got := localDate(late, sh); got != "2026-05-20" {
		t.Errorf("localDate = %s, want 2026-05-20", got)
	}
	if got := localDate(late, time.UTC); got != "2026-05-19" {
		t.Errorf("localDate(UTC) = %s, want 2026-05-19", got)
	}
	in := tasksToInput([]*todov1.Task{{Id: "a", DueAt: timestamppb.New(late)}}, sh)
	if len(in) != 1 || in[0].DueAt == nil || *in[0].DueAt != "2026-05-20" {
		t.Errorf("due date = %v, want 2026-05-20 in the user's zone", in)
	}
}
