package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/tz"
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
	pageSize   int // 0 = everything in one response; the token is the next index
}

func (f *fakeAuth) ListUserIDs(_ context.Context, in *authv1.ListUserIDsRequest, _ ...grpc.CallOption) (*authv1.ListUserIDsResponse, error) {
	f.gotFilters = append(f.gotFilters, in.PlanFilter)
	if f.err != nil {
		return nil, f.err
	}
	if f.pageSize == 0 {
		return &authv1.ListUserIDsResponse{Users: f.users}, nil
	}
	start := 0
	if in.PageToken != "" {
		start, _ = strconv.Atoi(in.PageToken)
	}
	end := min(start+f.pageSize, len(f.users))
	resp := &authv1.ListUserIDsResponse{Users: f.users[start:end]}
	if end < len(f.users) {
		resp.NextPageToken = strconv.Itoa(end)
	}
	return resp, nil
}

// fakeAI keeps plans in memory so a second run sees what the first one generated.
type fakeAI struct {
	mu          sync.Mutex
	plans       map[string]time.Time
	generateErr map[string]error
	latestErr   map[string]error
	genCalls    map[string]int
	gotEmail    map[string]string
	gotZone     map[string]string
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
}

func newFakeAI() *fakeAI {
	return &fakeAI{plans: map[string]time.Time{}, generateErr: map[string]error{}, latestErr: map[string]error{}, genCalls: map[string]int{}, gotEmail: map[string]string{}, gotZone: map[string]string{}}
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
	f.gotEmail[in.UserId] = in.Email
	f.gotZone[in.UserId] = in.TimeZone
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

func TestSameDay(t *testing.T) {
	est := time.FixedZone("EST", -5*3600)
	sh := time.FixedZone("CST", 8*3600)
	cases := []struct {
		name string
		a, b time.Time
		loc  *time.Location
		want bool
	}{
		{"same instant", testNow, testNow, time.UTC, true},
		{"earlier same day", testNow.Add(-6 * time.Hour), testNow, time.UTC, true},
		{"previous day", testNow.Add(-7 * time.Hour), testNow, time.UTC, false},
		{"23:30 EST is next UTC day", time.Date(2026, 10, 25, 23, 30, 0, 0, est), time.Date(2026, 10, 26, 6, 0, 0, 0, time.UTC), time.UTC, true},
		{"19:30 EST is same UTC day as 00:30 UTC", time.Date(2026, 10, 25, 19, 30, 0, 0, est), time.Date(2026, 10, 25, 6, 0, 0, 0, time.UTC), time.UTC, false},
		{"UTC days differ but Shanghai day is shared", time.Date(2026, 10, 26, 20, 0, 0, 0, time.UTC), time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC), sh, true},
	}
	for _, tc := range cases {
		if got := sameDay(tc.a, tc.b, tc.loc); got != tc.want {
			t.Errorf("%s: sameDay = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func zoned(id, zone string) *authv1.UserSummary {
	return &authv1.UserSummary{UserId: id, Language: "en", Plan: "pro", TimeZone: zone}
}

func TestGenerateAll_OnlyUsersInTheirLocalMorningWindow(t *testing.T) {
	// 22:00 UTC is 06:00 in Shanghai, 15:00 in Los Angeles.
	now := time.Date(2026, 10, 25, 22, 0, 0, 0, time.UTC)
	ai := newFakeAI()
	auth := &fakeAuth{users: []*authv1.UserSummary{zoned("sh", "Asia/Shanghai"), zoned("la", "America/Los_Angeles"), zoned("utc", "")}}
	res, err := generateAll(context.Background(), quiet(), auth, ai, 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if ai.genCalls["sh"] != 1 || ai.genCalls["la"] != 0 || ai.genCalls["utc"] != 0 {
		t.Errorf("generate calls = %v, want only the Shanghai user", ai.genCalls)
	}
	if ai.gotZone["sh"] != "Asia/Shanghai" {
		t.Errorf("zone sent to ai = %q, want Asia/Shanghai", ai.gotZone["sh"])
	}
	if res.Generated != 1 || res.NotDue != 2 || res.Skipped != 0 || res.Failed != 0 {
		t.Errorf("result = %+v, want 1 generated, 2 not due", res)
	}
}

func TestPlanDueWindow(t *testing.T) {
	sh := time.FixedZone("CST", 8*3600)
	for hourUTC, want := range map[int]bool{21: false, 22: true, 23: true, 0: true, 1: true, 2: false} { // 05:00..10:00 local
		now := time.Date(2026, 10, 25, hourUTC, 0, 0, 0, time.UTC)
		if hourUTC < 21 {
			now = now.AddDate(0, 0, 1)
		}
		if got := planDue(now, sh); got != want {
			t.Errorf("%02d:00 UTC (%02d:00 local): due = %v, want %v", hourUTC, now.In(sh).Hour(), got, want)
		}
	}
}

func TestGenerateAll_SameLocalDayPlanIsNotRegenerated(t *testing.T) {
	// 00:00 UTC Oct 27 = 08:00 Shanghai; the plan from 20:00 UTC Oct 26 is 04:00 the same local day.
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	ai := newFakeAI()
	ai.plans["sh"] = time.Date(2026, 10, 26, 20, 0, 0, 0, time.UTC)
	res, _ := generateAll(context.Background(), quiet(), &fakeAuth{users: []*authv1.UserSummary{zoned("sh", "Asia/Shanghai")}}, ai, 2, now)
	if ai.genCalls["sh"] != 0 || res.Skipped != 1 {
		t.Errorf("calls %v, result %+v; a plan from earlier the same local day must be kept", ai.genCalls, res)
	}
}

func TestGenerateAll_EmailsOnlyVerifiedAddresses(t *testing.T) {
	ai := newFakeAI()
	us := []*authv1.UserSummary{
		{UserId: "ok", Email: "ok@x.com", EmailVerified: true, Plan: "pro"},
		{UserId: "unv", Email: "unv@x.com", Plan: "pro"},
	}
	if _, err := generateAll(context.Background(), quiet(), &fakeAuth{users: us}, ai, 2, testNow); err != nil {
		t.Fatal(err)
	}
	if ai.gotEmail["ok"] != "ok@x.com" || ai.gotEmail["unv"] != "" {
		t.Errorf("emails sent to ai = %v, want ok@x.com only for the verified user", ai.gotEmail)
	}
}

func TestGenerateAll_WalksEveryPageOfUsers(t *testing.T) {
	ai := newFakeAI()
	res, err := generateAll(context.Background(), quiet(), &fakeAuth{users: users("a", "b", "c", "d", "e"), pageSize: 2}, ai, 2, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 5 || res.Generated != 5 {
		t.Errorf("result = %+v, want all 5 users across 3 pages", res)
	}
}

func TestPlanDue_RealZones(t *testing.T) {
	ny, kolkata := tz.Load("America/New_York"), tz.Load("Asia/Kolkata")
	for _, tc := range []struct {
		name string
		now  time.Time
		loc  *time.Location
		want bool
	}{
		{"New York 06:00 EDT before the change", time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC), ny, true},
		{"New York 05:00 EST the day of the change", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), ny, false},
		{"New York 06:00 EST after the change", time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC), ny, true},
		{"Kolkata 05:30 is too early", time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC), kolkata, false},
		{"Kolkata 06:30 is due", time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC), kolkata, true},
		{"Kolkata 09:30 is the last due hour", time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC), kolkata, true},
		{"Kolkata 10:30 is past the window", time.Date(2026, 10, 26, 5, 0, 0, 0, time.UTC), kolkata, false},
	} {
		if got := planDue(tc.now, tc.loc); got != tc.want {
			t.Errorf("%s: planDue = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSameDay_AcrossDST(t *testing.T) {
	ny := tz.Load("America/New_York")
	// 1 Nov 2026 is 25 hours long in New York: 00:30 EDT and 23:30 EST are the same local day.
	a := time.Date(2026, 11, 1, 4, 30, 0, 0, time.UTC)
	b := time.Date(2026, 11, 2, 4, 30, 0, 0, time.UTC)
	if !sameDay(a, b.Add(-time.Hour), ny) || sameDay(a, b.Add(time.Hour), ny) {
		t.Error("DST day boundaries are wrong")
	}
}
