package server

import (
	"encoding/json"
	"testing"

	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
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
