package main

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsociety/todo/pkg/paging"
	"github.com/fsociety/todo/pkg/tz"
	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// proPlan is the only plan with daily plans; free users must never reach Claude.
const proPlan = "pro"

// A user's plan is due from planHour local time; planWindow hours of retries absorb failed runs,
// and keep a plan from appearing hours late when tasks are added in the afternoon.
const (
	planHour   = 6
	planWindow = 4
)

type userLister interface {
	ListUserIDs(ctx context.Context, in *authv1.ListUserIDsRequest, opts ...grpc.CallOption) (*authv1.ListUserIDsResponse, error)
}

type planService interface {
	GeneratePlan(ctx context.Context, in *aiv1.GeneratePlanRequest, opts ...grpc.CallOption) (*aiv1.GeneratePlanResponse, error)
	GetLatestPlan(ctx context.Context, in *aiv1.GetLatestPlanRequest, opts ...grpc.CallOption) (*aiv1.GetLatestPlanResponse, error)
}

// NotDue counts users outside their local morning window; Total includes them.
type result struct{ Total, Generated, Skipped, Failed, NotDue int }

// generateAll generates today's plan for every pro user whose local morning has come and who lacks
// one. Users with no tasks or an existing plan are skipped, so reruns only redo real failures.
func generateAll(ctx context.Context, log *slog.Logger, auth userLister, ai planService, workers int, now time.Time) (result, error) {
	users, err := paging.All(ctx, func(ctx context.Context, token string) ([]*authv1.UserSummary, string, error) {
		resp, err := auth.ListUserIDs(ctx, &authv1.ListUserIDsRequest{PlanFilter: proPlan, PageSize: 500, PageToken: token})
		if err != nil {
			return nil, "", err
		}
		return resp.Users, resp.NextPageToken, nil
	})
	if err != nil {
		return result{}, err
	}
	res := result{Total: len(users)}
	log.Info("generating plans", "user_count", res.Total)

	var generated, skipped, failed atomic.Int32
	notDue := 0
	sem := make(chan struct{}, workers) // bounds concurrent users, hence Claude calls
	var wg sync.WaitGroup

outer:
	for _, u := range users {
		loc := tz.Load(u.TimeZone)
		if !planDue(now, loc) {
			notDue++
			continue
		}
		// A free slot would make the select below pick randomly once ctx is done.
		if ctx.Err() != nil {
			log.Warn("context cancelled, stopping dispatch")
			break outer
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			log.Warn("context cancelled, stopping dispatch")
			break outer
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			planCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()

			switch generateOne(planCtx, log, ai, u.UserId, u.Language, u.TimeZone, notifyEmail(u), now, loc) {
			case outcomeGenerated:
				generated.Add(1)
			case outcomeSkipped:
				skipped.Add(1)
			default:
				failed.Add(1)
			}
		}()
	}
	wg.Wait()

	res.Generated, res.Skipped, res.Failed, res.NotDue = int(generated.Load()), int(skipped.Load()), int(failed.Load()), notDue
	// Users never dispatched (shutdown) must fail the job, or the Job completes without them.
	res.Failed += res.Total - res.Generated - res.Skipped - res.Failed - res.NotDue
	return res, nil
}

type outcome int

const (
	outcomeFailed outcome = iota
	outcomeGenerated
	outcomeSkipped
)

// planDue reports whether now falls inside the user's morning window.
func planDue(now time.Time, loc *time.Location) bool {
	h := now.In(loc).Hour()
	return h >= planHour && h < planHour+planWindow
}

func generateOne(ctx context.Context, log *slog.Logger, ai planService, userID, language, zone, email string, now time.Time, loc *time.Location) outcome {
	latest, err := ai.GetLatestPlan(ctx, &aiv1.GetLatestPlanRequest{UserId: userID})
	if err != nil {
		log.Error("get latest plan", "user_id", userID, "err", err)
		return outcomeFailed
	}
	if p := latest.GetPlan(); p != nil && sameDay(p.GetGeneratedAt().AsTime(), now, loc) {
		return outcomeSkipped
	}

	resp, err := ai.GeneratePlan(ctx, &aiv1.GeneratePlanRequest{UserId: userID, Language: language, Email: email, TimeZone: zone})
	if err != nil {
		if status.Code(err) == codes.FailedPrecondition { // no tasks to prioritize
			return outcomeSkipped
		}
		log.Error("generate plan", "user_id", userID, "err", err)
		return outcomeFailed
	}
	log.Info("plan generated", "user_id", userID, "plan_id", resp.Plan.GetPlanId(), "tasks", len(resp.Plan.GetItems()))
	return outcomeGenerated
}

// notifyEmail is the user's address once verified; signup accepts any address.
func notifyEmail(u *authv1.UserSummary) string {
	if u.GetEmailVerified() {
		return u.GetEmail()
	}
	return ""
}

func sameDay(a, b time.Time, loc *time.Location) bool {
	ay, am, ad := a.In(loc).Date()
	by, bm, bd := b.In(loc).Date()
	return ay == by && am == bm && ad == bd
}
