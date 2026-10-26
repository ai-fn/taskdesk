package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var testNow = time.Date(2026, 10, 26, 6, 0, 0, 0, time.UTC)

type fakeAuth struct {
	users      []*authv1.UserSummary
	err        error
	gotFilters []string
}

func (f *fakeAuth) ListUserIDs(_ context.Context, in *authv1.ListUserIDsRequest, _ ...grpc.CallOption) (*authv1.ListUserIDsResponse, error) {
	f.gotFilters = append(f.gotFilters, in.PlanFilter)
	if f.err != nil {
		return nil, f.err
	}
	return &authv1.ListUserIDsResponse{Users: f.users}, nil
}

// fakeAI keeps plans in memory so a second run sees what the first one generated.
type fakeAI struct {
	mu          sync.Mutex
	plans       map[string]time.Time
	generateErr map[string]error
	latestErr   map[string]error
	genCalls    map[string]int
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
}

func newFakeAI() *fakeAI {
	return &fakeAI{plans: map[string]time.Time{}, generateErr: map[string]error{}, latestErr: map[string]error{}, genCalls: map[string]int{}}
}

func (f *fakeAI) GetLatestPlan(_ context.Context, in *aiv1.GetLatestPlanRequest, _ ...grpc.CallOption) (*aiv1.GetLatestPlanResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.latestErr[in.UserId]; err != nil {
		return nil, err
	}
	at, ok := f.plans[in.UserId]
	if !ok {
		return &aiv1.GetLatestPlanResponse{}, nil
	}
	return &aiv1.GetLatestPlanResponse{Plan: &aiv1.DailyPlan{PlanId: "p-" + in.UserId, GeneratedAt: timestamppb.New(at)}}, nil
}

func (f *fakeAI) GeneratePlan(_ context.Context, in *aiv1.GeneratePlanRequest, _ ...grpc.CallOption) (*aiv1.GeneratePlanResponse, error) {
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.maxInFlight.Load()
		if n <= m || f.maxInFlight.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(time.Millisecond)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.genCalls[in.UserId]++
	if err := f.generateErr[in.UserId]; err != nil {
		return nil, err
	}
	f.plans[in.UserId] = testNow
	return &aiv1.GeneratePlanResponse{Plan: &aiv1.DailyPlan{PlanId: "p-" + in.UserId}}, nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func users(ids ...string) []*authv1.UserSummary {
	var out []*authv1.UserSummary
	for _, id := range ids {
		out = append(out, &authv1.UserSummary{UserId: id, Language: "en", Plan: "pro"})
	}
	return out
}

func TestGenerateAll_RequestsOnlyProUsers(t *testing.T) {
	auth := &fakeAuth{users: users("a")}
	if _, err := generateAll(context.Background(), quiet(), auth, newFakeAI(), 2, testNow); err != nil {
		t.Fatal(err)
	}
	if len(auth.gotFilters) != 1 || auth.gotFilters[0] != "pro" {
		t.Errorf("plan filters = %v, want [pro]", auth.gotFilters)
	}
}

func TestGenerateAll_NoTasksIsSkipNotFailure(t *testing.T) {
	ai := newFakeAI()
	ai.generateErr["empty"] = status.Error(codes.FailedPrecondition, "no tasks to prioritize")
	res, err := generateAll(context.Background(), quiet(), &fakeAuth{users: users("empty", "busy")}, ai, 2, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 0 || res.Skipped != 1 || res.Generated != 1 {
		t.Errorf("result = %+v, want generated=1 skipped=1 failed=0", res)
	}
}

func TestGenerateAll_RealErrorsCountAsFailed(t *testing.T) {
	ai := newFakeAI()
	ai.generateErr["bad"] = status.Error(codes.Unavailable, "claude down")
	ai.latestErr["worse"] = errors.New("db down")
	res, _ := generateAll(context.Background(), quiet(), &fakeAuth{users: users("bad", "worse", "ok")}, ai, 2, testNow)
	if res.Failed != 2 || res.Generated != 1 {
		t.Errorf("result = %+v, want failed=2 generated=1", res)
	}
	if ai.genCalls["worse"] != 0 {
		t.Error("must not generate when the latest-plan check failed: it could duplicate a paid call")
	}
}

func TestGenerateAll_SkipsUsersWithPlanToday(t *testing.T) {
	ai := newFakeAI()
	ai.plans["done"] = testNow.Add(-5 * time.Hour)   // earlier the same UTC day
	ai.plans["stale"] = testNow.Add(-24 * time.Hour) // yesterday
	res, _ := generateAll(context.Background(), quiet(), &fakeAuth{users: users("done", "stale", "new")}, ai, 2, testNow)
	if res.Skipped != 1 || res.Generated != 2 {
		t.Errorf("result = %+v, want skipped=1 generated=2", res)
	}
	if ai.genCalls["done"] != 0 {
		t.Error("user with today's plan was regenerated")
	}
}

func TestGenerateAll_RerunMakesNoNewCalls(t *testing.T) {
	ai := newFakeAI()
	auth := &fakeAuth{users: users("a", "b", "c")}
	if _, err := generateAll(context.Background(), quiet(), auth, ai, 2, testNow); err != nil {
		t.Fatal(err)
	}
	res, _ := generateAll(context.Background(), quiet(), auth, ai, 2, testNow)
	if res.Generated != 0 || res.Skipped != 3 {
		t.Errorf("rerun result = %+v, want generated=0 skipped=3", res)
	}
	for id, n := range ai.genCalls {
		if n != 1 {
			t.Errorf("user %s generated %d times, want 1", id, n)
		}
	}
}

func TestGenerateAll_BoundsConcurrency(t *testing.T) {
	ai := newFakeAI()
	ids := make([]string, 30)
	for i := range ids {
		ids[i] = string(rune('a'+i%26)) + string(rune('A'+i/26))
	}
	if _, err := generateAll(context.Background(), quiet(), &fakeAuth{users: users(ids...)}, ai, 3, testNow); err != nil {
		t.Fatal(err)
	}
	if got := ai.maxInFlight.Load(); got > 3 {
		t.Errorf("max concurrent GeneratePlan = %d, want <= 3", got)
	}
}

func TestGenerateAll_ListErrorIsReturned(t *testing.T) {
	_, err := generateAll(context.Background(), quiet(), &fakeAuth{err: errors.New("auth down")}, newFakeAI(), 2, testNow)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGenerateAll_CancelledRunFailsWithUnprocessedUsers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ai := newFakeAI()
	res, err := generateAll(ctx, quiet(), &fakeAuth{users: users("a", "b", "c")}, ai, 3, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(ai.genCalls) != 0 {
		t.Errorf("dispatched %d users after cancel, want 0", len(ai.genCalls))
	}
	if res.Failed != 3 {
		t.Errorf("failed = %d, want 3: unprocessed users must fail the job", res.Failed)
	}
}

func TestSameUTCDay(t *testing.T) {
	est := time.FixedZone("EST", -5*3600)
	cases := []struct {
		name string
		a, b time.Time
		want bool
	}{
		{"same instant", testNow, testNow, true},
		{"earlier same day", testNow.Add(-6 * time.Hour), testNow, true},
		{"previous day", testNow.Add(-7 * time.Hour), testNow, false},
		{"23:30 EST is next UTC day", time.Date(2026, 10, 25, 23, 30, 0, 0, est), time.Date(2026, 10, 26, 6, 0, 0, 0, time.UTC), true},
		{"19:30 EST is same UTC day as 00:30 UTC", time.Date(2026, 10, 25, 19, 30, 0, 0, est), time.Date(2026, 10, 25, 6, 0, 0, 0, time.UTC), false},
	}
	for _, tc := range cases {
		if got := sameUTCDay(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: sameUTCDay = %v, want %v", tc.name, got, tc.want)
		}
	}
}
