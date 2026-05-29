// Command scheduler is the K8s CronJob driver for daily AI plan generation.
// It lists all users from auth-service and calls ai-service.GeneratePlan for each.
// Designed to run as a one-shot K8s Job (exit 0 = success, non-zero = retry).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
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

	log.Info("scheduler started", "auth", cfg.AuthServiceAddr, "ai", cfg.AIServiceAddr)

	usersResp, err := authClient.ListUserIDs(ctx, &authv1.ListUserIDsRequest{})
	if err != nil {
		log.Error("list users", "err", err)
		os.Exit(1)
	}

	log.Info("generating plans", "user_count", len(usersResp.Users))

	failed := 0
	for _, u := range usersResp.Users {
		if ctx.Err() != nil {
			log.Warn("context cancelled, stopping")
			break
		}

		planCtx, planCancel := context.WithTimeout(ctx, 2*time.Minute)
		resp, err := aiClient.GeneratePlan(planCtx, &aiv1.GeneratePlanRequest{UserId: u.UserId, Language: u.Language})
		planCancel()

		if err != nil {
			log.Error("generate plan", "user_id", u.UserId, "email", u.Email, "err", err)
			failed++
			continue
		}

		log.Info("plan generated",
			"user_id", u.UserId,
			"plan_id", resp.Plan.PlanId,
			"tasks", len(resp.Plan.Items),
		)
	}

	if failed > 0 {
		log.Error("some plans failed", slog.Int("failed", failed), slog.Int("total", len(usersResp.Users)))
		os.Exit(1)
	}

	log.Info("scheduler done", "plans_generated", len(usersResp.Users))
}
