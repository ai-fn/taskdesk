package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pkgconfig "github.com/fsociety/todo/pkg/config"
	"github.com/fsociety/todo/pkg/logger"
	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/gateway/internal/server"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Config struct {
	ListenAddr string `env:"LISTEN_ADDR"  default:":8080"`
	AuthAddr   string `env:"AUTH_ADDR,required"`
	TodoAddr   string `env:"TODO_ADDR,required"`
	AIAddr     string `env:"AI_ADDR"      default:"ai.todo.svc.cluster.local:50054"`
	RedisAddr  string `env:"REDIS_ADDR"   default:"redis.data.svc.cluster.local:6379"`
}

func main() {
	log := logger.New("gateway")

	cfg, err := pkgconfig.Load[Config]()
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	// gRPC connections to downstream services.
	// insecure is fine for in-cluster traffic (mTLS is a Phase 5 concern).
	authConn, err := grpc.NewClient(cfg.AuthAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial auth-service", "err", err)
		os.Exit(1)
	}
	defer authConn.Close()

	todoConn, err := grpc.NewClient(cfg.TodoAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial todo-service", "err", err)
		os.Exit(1)
	}
	defer todoConn.Close()

	aiConn, err := grpc.NewClient(cfg.AIAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial ai-service", "err", err)
		os.Exit(1)
	}
	defer aiConn.Close()

	authClient := authv1.NewAuthServiceClient(authConn)
	todoClient := todov1.NewTodoServiceClient(todoConn)
	aiClient := aiv1.NewAIServiceClient(aiConn)

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.New(log, authClient, todoClient, aiClient, rdb),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("gateway listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}
