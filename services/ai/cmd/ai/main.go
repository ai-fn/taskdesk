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
	"github.com/fsociety/todo/services/ai/internal/parser"
	"github.com/fsociety/todo/services/ai/internal/planner"
	"github.com/fsociety/todo/services/ai/internal/scorer"
	"github.com/fsociety/todo/services/ai/internal/server"
	"github.com/fsociety/todo/services/ai/internal/store"
	"golang.org/x/sync/errgroup"
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
	EmbedRPM        int    `env:"EMBED_RPM"         default:"3"` // Voyage requests/min per pod; raise for a paid key
	pkgpgx.Config
}

func main() {
	log := pkglogger.New("ai")

	cfg, err := pkgconfig.Load[Config]()
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	// sigCtx is cancelled when SIGINT/SIGTERM arrives.
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pkgpgx.New(sigCtx, cfg.Config, log)
	if err != nil {
		log.Error("connect postgres", "err", err)
		os.Exit(1)
	}
	// pool.Close is deferred at the outermost scope. defer runs after main
	// returns, which is after eg.Wait() below. Every goroutine has exited by
	// then, so no goroutine can use a connection after the pool is closed.
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

	todoConn, err := grpc.NewClient(cfg.TodoServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial todo-service", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := todoConn.Close(); err != nil {
			log.Warn("close todo-service conn", "err", err)
		}
	}()
	todoClient := todov1.NewTodoServiceClient(todoConn)

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
	authClient := authv1.NewAuthServiceClient(authConn)

	emb := embedder.New(cfg.VoyageAPIKey)
	p := planner.New(cfg.AnthropicAPIKey)
	pa := parser.New(cfg.AnthropicAPIKey)
	sc := scorer.New(cfg.AnthropicAPIKey)
	st := store.New(pool)
	svc := server.New(p, pa, emb, st, todoClient, producer, log)

	embHandler := consumer.New(emb, todoClient, authClient, log, cfg.EmbedRPM)
	embConsumer, err := consumer.NewRunner(brokers, embHandler, log)
	if err != nil {
		log.Error("create embedding consumer", "err", err)
		os.Exit(1)
	}

	scoreHandler := scorer.NewConsumer(sc, todoClient, authClient, log)
	scoreConsumer, err := scorer.NewRunner(brokers, scoreHandler, log)
	if err != nil {
		log.Error("create scoring consumer", "err", err)
		os.Exit(1)
	}

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

	// metricsSrv is an explicit http.Server so Shutdown can be called on it
	// during cleanup. http.ListenAndServe has no shutdown path.
	metricsSrv := &http.Server{
		Addr:    ":9090",
		Handler: pkgmetrics.Handler(),
	}

	// eg manages every long-running goroutine. egCtx is derived from sigCtx and
	// is cancelled when either:
	//   (a) sigCtx is cancelled (SIGINT/SIGTERM), or
	//   (b) any eg.Go function returns a non-nil error.
	// Case (b) is the key difference from a plain WaitGroup: a crashing consumer
	// now propagates its failure upward and triggers a coordinated shutdown of
	// everything else — without any explicit signalling channel.
	eg, egCtx := errgroup.WithContext(sigCtx)

	// Embedding consumer exits with an error on unrecoverable Kafka failures.
	// Wrapping the error gives eg.Wait() a meaningful message.
	eg.Go(func() error {
		if err := embConsumer.Run(egCtx); err != nil {
			return fmt.Errorf("embedding consumer: %w", err)
		}
		return nil
	})

	// Backfill goroutines run until ctx is cancelled and do not produce
	// meaningful errors (they log internally). Returning nil here documents that
	// these are fire-and-finish workers, not error-propagating ones.
	eg.Go(func() error {
		embHandler.Backfill(egCtx)
		return nil
	})

	eg.Go(func() error {
		if err := scoreConsumer.Run(egCtx); err != nil {
			return fmt.Errorf("scoring consumer: %w", err)
		}
		return nil
	})

	eg.Go(func() error {
		scoreHandler.Backfill(egCtx)
		return nil
	})

	// gRPC server: Serve blocks until GracefulStop is called by the shutdown
	// coordinator below.
	eg.Go(func() error {
		log.Info("ai-service starting", "addr", cfg.ListenAddr)
		if err := grpcServer.Serve(lis); err != nil {
			return fmt.Errorf("grpc serve: %w", err)
		}
		return nil
	})

	// Metrics server: ListenAndServe returns http.ErrServerClosed on a clean
	// Shutdown call, which is not a real error.
	eg.Go(func() error {
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("metrics server: %w", err)
		}
		return nil
	})

	// Shutdown coordinator: blocks on egCtx.Done(), then drives the teardown
	// sequence. egCtx is cancelled on SIGTERM (via sigCtx) or on any goroutine
	// error above — both paths flow through here, so there is one shutdown path.
	//
	// Ordering matters:
	//   1. GracefulStop drains in-flight RPCs, then unblocks grpcServer.Serve.
	//   2. metricsSrv.Shutdown stops the metrics HTTP server.
	//   3. Both Serve goroutines return → eg.Wait() unblocks.
	//   4. main returns → defer pool.Close(), producer.Close(), etc. run.
	eg.Go(func() error {
		<-egCtx.Done()
		log.Info("shutting down", "reason", egCtx.Err())
		grpcServer.GracefulStop()
		if err := metricsSrv.Shutdown(context.Background()); err != nil {
			log.Error("metrics server shutdown", "err", err)
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		log.Error("ai-service exited with error", "err", err)
		os.Exit(1)
	}

	log.Info("ai-service stopped cleanly")
}
