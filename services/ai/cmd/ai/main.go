package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	pkgconfig "github.com/fsociety/todo/pkg/config"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	pkglogger "github.com/fsociety/todo/pkg/logger"
	pkgmetrics "github.com/fsociety/todo/pkg/metrics"
	pkgpgx "github.com/fsociety/todo/pkg/pgx"
	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/ai/internal/consumer"
	"github.com/fsociety/todo/services/ai/internal/embedder"
	"github.com/fsociety/todo/services/ai/internal/planner"
	"github.com/fsociety/todo/services/ai/internal/server"
	"github.com/fsociety/todo/services/ai/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

type Config struct {
	ListenAddr      string `env:"LISTEN_ADDR"       default:":50054"`
	MigrationsDir   string `env:"MIGRATIONS_DIR"    default:"file:///migrations/ai"`
	KafkaBrokers    string `env:"KAFKA_BROKERS"     default:"todo-kafka-bootstrap.data.svc.cluster.local:9092"`
	TodoServiceAddr string `env:"TODO_SERVICE_ADDR" default:"todo.todo.svc.cluster.local:50052"`
	AuthServiceAddr string `env:"AUTH_SERVICE_ADDR" default:"auth.todo.svc.cluster.local:50051"`
	AnthropicAPIKey string `env:"ANTHROPIC_API_KEY"`
	VoyageAPIKey    string `env:"VOYAGE_API_KEY"`
	pkgpgx.Config
}

func main() {
	log := pkglogger.New("ai")

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

	// Connect to todo-service via gRPC.
	todoConn, err := grpc.NewClient(cfg.TodoServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial todo-service", "err", err)
		os.Exit(1)
	}
	defer todoConn.Close()
	todoClient := todov1.NewTodoServiceClient(todoConn)

	// Connect to auth-service for backfill user enumeration.
	authConn, err := grpc.NewClient(cfg.AuthServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial auth-service", "err", err)
		os.Exit(1)
	}
	defer authConn.Close()
	authClient := authv1.NewAuthServiceClient(authConn)

	emb := embedder.New(cfg.VoyageAPIKey)
	p := planner.New(cfg.AnthropicAPIKey)
	st := store.New(pool)
	svc := server.New(p, emb, st, todoClient, producer, log)

	// Embedding consumer: listens to task events and keeps the vector index fresh.
	embHandler := consumer.New(emb, todoClient, authClient, log)
	embConsumer, err := consumer.NewRunner(brokers, embHandler, log)
	if err != nil {
		log.Error("create embedding consumer", "err", err)
		os.Exit(1)
	}
	go func() {
		if err := embConsumer.Run(ctx); err != nil {
			log.Error("embedding consumer", "err", err)
		}
	}()
	// Backfill: embed any existing tasks that don't have embeddings yet.
	go embHandler.Backfill(ctx)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(pkgmetrics.UnaryServerInterceptor("ai", log)),
		grpc.ChainStreamInterceptor(pkgmetrics.StreamServerInterceptor("ai", log)),
	)
	aiv1.RegisterAIServiceServer(grpcServer, svc)
	reflection.Register(grpcServer)

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

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

	log.Info("ai-service starting", "addr", cfg.ListenAddr)

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
