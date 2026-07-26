// Command scheduler is the K8s CronJob driver for daily AI plan generation.
// It lists all users from auth-service and calls ai-service.GeneratePlan for each.
// Designed to run as a one-shot K8s Job (exit 0 = success, non-zero = retry).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	pkgconfig "github.com/fsociety/todo/pkg/config"
	pkglogger "github.com/fsociety/todo/pkg/logger"
	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Config struct {
	AuthServiceAddr string `env:"AUTH_SERVICE_ADDR" default:"auth.todo.svc.cluster.local:50051"`
	AIServiceAddr   string `env:"AI_SERVICE_ADDR"   default:"ai.todo.svc.cluster.local:50054"`
	WorkerCount     int    `env:"WORKER_COUNT"      default:"5"`
}

func main() {
	log := pkglogger.New("scheduler")

	cfg, err := pkgconfig.Load[Config]()
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	authConn, err := grpc.NewClient(cfg.AuthServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial auth-service", "err", err)
		os.Exit(1)
	}
	defer authConn.Close()

	aiConn, err := grpc.NewClient(cfg.AIServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial ai-service", "err", err)
		os.Exit(1)
	}
	defer aiConn.Close()

	authClient := authv1.NewAuthServiceClient(authConn)
	aiClient := aiv1.NewAIServiceClient(aiConn)

	log.Info("scheduler started",
		"auth", cfg.AuthServiceAddr,
		"ai", cfg.AIServiceAddr,
		"workers", cfg.WorkerCount,
	)

	usersResp, err := authClient.ListUserIDs(ctx, &authv1.ListUserIDsRequest{})
	if err != nil {
		log.Error("list users", "err", err)
		os.Exit(1)
	}

	log.Info("generating plans", "user_count", len(usersResp.Users))

	// sem is a counting semaphore implemented as a buffered channel.
	// Sending claims a slot (blocks when all WorkerCount slots are taken).
	// Receiving inside each goroutine releases the slot.
	// This bounds the number of concurrent GeneratePlan calls to WorkerCount.
	sem := make(chan struct{}, cfg.WorkerCount)

	// wg tracks every launched goroutine. wg.Wait() below ensures we read
	// `failed` only after every goroutine has called wg.Done() — establishing
	// the happens-before edge that makes the subsequent failed.Load() safe.
	var wg sync.WaitGroup

	// failed counts plans that errored. It is written concurrently from multiple
	// goroutines, so it must be an atomic — a plain int would be a data race.
	var failed atomic.Int32

outer:
	for _, u := range usersResp.Users {
		// Stop dispatching new work if a cancellation signal arrived.
		// In-flight goroutines continue; wg.Wait() below joins them all.
		if ctx.Err() != nil {
			log.Warn("context cancelled, stopping dispatch")
			break outer
		}

		// Acquire a semaphore slot. Using select with a labeled break ensures
		// we exit the for loop (not just the select) when a signal fires while
		// all workers are busy. A plain break inside select only exits select.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			log.Warn("context cancelled while waiting for worker slot")
			break outer
		}

		// wg.Add must happen before go func, not inside it.
		// If Add were called inside the goroutine, wg.Wait() could return before
		// any goroutine increments the counter — a classic WaitGroup race.
		wg.Add(1)

		u := u // copy loop variable (good habit even in Go 1.22+)
		go func() {
			// Release the semaphore slot and decrement the WaitGroup when done,
			// regardless of whether the goroutine succeeded or failed.
			defer wg.Done()
			defer func() { <-sem }()

			planCtx, planCancel := context.WithTimeout(ctx, 2*time.Minute)
			defer planCancel()

			resp, err := aiClient.GeneratePlan(planCtx, &aiv1.GeneratePlanRequest{
				UserId:   u.UserId,
				Language: u.Language,
			})
			if err != nil {
				log.Error("generate plan", "user_id", u.UserId, "email", u.Email, "err", err)
				// Add(1) is an atomic read-modify-write. Multiple goroutines can
				// call this concurrently without a data race.
				failed.Add(1)
				return
			}

			log.Info("plan generated",
				"user_id", u.UserId,
				"plan_id", resp.Plan.PlanId,
				"tasks", len(resp.Plan.Items),
			)
		}()
	}

	// Join all goroutines before reading `failed`. wg.Wait() establishes a
	// happens-before edge with every wg.Done(), which in turn happens after the
	// last failed.Add(1) in each goroutine — making failed.Load() race-free.
	wg.Wait()

	if n := failed.Load(); n > 0 {
		log.Error("some plans failed", slog.Int("failed", int(n)), slog.Int("total", len(usersResp.Users)))
		os.Exit(1)
	}

	log.Info("scheduler done", "plans_generated", len(usersResp.Users))
}
