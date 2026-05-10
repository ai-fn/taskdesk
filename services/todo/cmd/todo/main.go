package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	pkgconfig "github.com/fsociety/todo/pkg/config"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	pkglogger "github.com/fsociety/todo/pkg/logger"
	pkgpgx "github.com/fsociety/todo/pkg/pgx"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/todo/internal/outbox"
	"github.com/fsociety/todo/services/todo/internal/server"
	"github.com/fsociety/todo/services/todo/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

type Config struct {
	ListenAddr    string `env:"LISTEN_ADDR"    default:":50052"`
	MigrationsDir string `env:"MIGRATIONS_DIR" default:"file:///migrations/todo"`
	KafkaBrokers  string `env:"KAFKA_BROKERS"  default:"todo-kafka-bootstrap.data.svc.cluster.local:9092"`
	pkgpgx.Config
}

func main() {
	log := pkglogger.New("todo")

	cfg, err := pkgconfig.Load[Config]()
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pkgpgx.New(ctx, cfg.Config, log)
	if err != nil {
		log.Error("connect postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pkgpgx.Migrate(cfg.DSN, cfg.MigrationsDir, log); err != nil {
		log.Error("run migrations", "err", err)
		os.Exit(1)
	}

	brokers := strings.Split(cfg.KafkaBrokers, ",")
	producer, err := pkgkafka.NewProducer(brokers, log)
	if err != nil {
		log.Error("create kafka producer", "err", err)
		os.Exit(1)
	}
	defer producer.Close()

	st := store.New(pool)
	pub := outbox.New(st, producer, log)
	go pub.Run(ctx)

	svc := server.New(st, log)

	grpcServer := grpc.NewServer()
	todov1.RegisterTodoServiceServer(grpcServer, svc)
	reflection.Register(grpcServer)

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Error("listen", "err", err)
		os.Exit(1)
	}

	log.Info("todo-service starting", "addr", cfg.ListenAddr)

	errCh := make(chan error, 1)
	go func() { errCh <- grpcServer.Serve(lis) }()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		grpcServer.GracefulStop()
	case err := <-errCh:
		log.Error("serve", "err", fmt.Sprintf("%v", err))
		os.Exit(1)
	}
}
