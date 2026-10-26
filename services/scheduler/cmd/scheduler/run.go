package main

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// proPlan is the only plan with daily plans; free users must never reach Claude.
const proPlan = "pro"

type userLister interface {
	ListUserIDs(ctx context.Context, in *authv1.ListUserIDsRequest, opts ...grpc.CallOption) (*authv1.ListUserIDsResponse, error)
}

type planService interface {
	GeneratePlan(ctx context.Context, in *aiv1.GeneratePlanRequest, opts ...grpc.CallOption) (*aiv1.GeneratePlanResponse, error)
	GetLatestPlan(ctx context.Context, in *aiv1.GetLatestPlanRequest, opts ...grpc.CallOption) (*aiv1.GetLatestPlanResponse, error)
}

type result struct{ Total, Generated, Skipped, Failed int }

// generateAll generates today's plan for every pro user that lacks one. Users with no
// tasks or an existing plan are skipped, so a job retry only redoes real failures.
func generateAll(ctx context.Context, log *slog.Logger, auth userLister, ai planService, workers int, now time.Time) (result, error) {
	usersResp, err := auth.ListUserIDs(ctx, &authv1.ListUserIDsRequest{PlanFilter: proPlan})
	if err != nil {
		return result{}, err
	}
	res := result{Total: len(usersResp.Users)}
	log.Info("generating plans", "user_count", res.Total)

	var generated, skipped, failed atomic.Int32
	sem := make(chan struct{}, workers) // bounds concurrent users, hence Claude calls
	var wg sync.WaitGroup

outer:
	for _, u := range usersResp.Users {
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

			switch generateOne(planCtx, log, ai, u.UserId, u.Language, notifyEmail(u), now) {
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

	res.Generated, res.Skipped, res.Failed = int(generated.Load()), int(skipped.Load()), int(failed.Load())
	// Users never dispatched (shutdown) must fail the job, or the Job completes without them.
	res.Failed += res.Total - res.Generated - res.Skipped - res.Failed
	return res, nil
}

type outcome int

const (
	outcomeFailed outcome = iota
	outcomeGenerated
	outcomeSkipped
)

func generateOne(ctx context.Context, log *slog.Logger, ai planService, userID, language, email string, now time.Time) outcome {
	latest, err := ai.GetLatestPlan(ctx, &aiv1.GetLatestPlanRequest{UserId: userID})
	if err != nil {
		log.Error("get latest plan", "user_id", userID, "err", err)
		return outcomeFailed
	}
	if p := latest.GetPlan(); p != nil && sameUTCDay(p.GetGeneratedAt().AsTime(), now) {
		return outcomeSkipped
	}

	resp, err := ai.GeneratePlan(ctx, &aiv1.GeneratePlanRequest{UserId: userID, Language: language, Email: email})
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

func sameUTCDay(a, b time.Time) bool {
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	return ay == by && am == bm && ad == bd
}
