package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	pkgconfig "github.com/fsociety/todo/pkg/config"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	"github.com/fsociety/todo/pkg/lifecycle"
	pkglogger "github.com/fsociety/todo/pkg/logger"
	pkgmetrics "github.com/fsociety/todo/pkg/metrics"
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

	// wg tracks the outbox publisher so we can wait for any in-flight flush to
	// finish before pool.Close() and producer.Close() run via defer.
	// Without this, a mid-flush batch (read from DB, published to Kafka, but not
	// yet marked published) gets cut off and will be re-delivered on next startup.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pub.Run(ctx)
	}()

	svc := server.New(st, log)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(pkgmetrics.UnaryServerInterceptor("todo", log)),
		grpc.ChainStreamInterceptor(pkgmetrics.StreamServerInterceptor("todo", log)),
	)
	todov1.RegisterTodoServiceServer(grpcServer, svc)
	reflection.Register(grpcServer)

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	lifecycle.WatchGRPC(ctx, healthSrv, lifecycle.ProbeInterval, lifecycle.ProbeTimeout, log,
		lifecycle.Check{Name: "postgres", Fn: pool.Ping})

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Error("listen", "err", err)
		os.Exit(1)
	}

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", pkgmetrics.Handler())
		if err := http.ListenAndServe(":9090", mux); err != nil {
			log.Error("metrics server", "err", err)
		}
	}()

	log.Info("todo-service starting", "addr", cfg.ListenAddr)

	errCh := make(chan error, 1)
	go func() { errCh <- grpcServer.Serve(lis) }()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		healthSrv.Shutdown()
		lifecycle.GracefulStop(grpcServer, lifecycle.ShutdownTimeout, log)
		// Wait for the outbox publisher to finish its current flush before
		// pool.Close() and producer.Close() run via defer.
		// wg.Wait() returns only after pub.Run() exits, which happens because
		// ctx is already cancelled — the publisher's select will see ctx.Done()
		// on the next iteration.
		wg.Wait()
	case err := <-errCh:
		log.Error("serve", "err", fmt.Sprintf("%v", err))
		// Cancel ctx so the outbox publisher sees ctx.Done() and exits,
		// then join it before os.Exit — which skips all defers.
		stop()
		wg.Wait()
		os.Exit(1)
	}
}
