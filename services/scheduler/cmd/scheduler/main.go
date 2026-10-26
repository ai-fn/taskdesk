// Command scheduler is the K8s CronJob driver for daily AI plan generation.
// It lists pro users from auth-service and calls ai-service.GeneratePlan for those without a plan today.
// Designed to run as a one-shot K8s Job (exit 0 = success, non-zero = retry; reruns only redo failures).
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
	defer func() {
		if err := authConn.Close(); err != nil {
			log.Warn("close auth-service conn", "err", err)
		}
	}()

	aiConn, err := grpc.NewClient(cfg.AIServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial ai-service", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := aiConn.Close(); err != nil {
			log.Warn("close ai-service conn", "err", err)
		}
	}()

	authClient := authv1.NewAuthServiceClient(authConn)
	aiClient := aiv1.NewAIServiceClient(aiConn)

	log.Info("scheduler started",
		"auth", cfg.AuthServiceAddr,
		"ai", cfg.AIServiceAddr,
		"workers", cfg.WorkerCount,
	)

	res, err := generateAll(ctx, log, authClient, aiClient, cfg.WorkerCount, time.Now())
	if err != nil {
		log.Error("list users", "err", err)
		os.Exit(1)
	}

	if res.Failed > 0 {
		log.Error("some plans failed", slog.Int("failed", res.Failed), slog.Int("total", res.Total))
		os.Exit(1)
	}

	log.Info("scheduler done", "generated", res.Generated, "skipped", res.Skipped, "not_due", res.NotDue)
}
