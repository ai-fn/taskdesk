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
	"time"

	pkgconfig "github.com/fsociety/todo/pkg/config"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	pkglogger "github.com/fsociety/todo/pkg/logger"
	pkgmetrics "github.com/fsociety/todo/pkg/metrics"
	pkgpgx "github.com/fsociety/todo/pkg/pgx"
	"github.com/fsociety/todo/pkg/tokens"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/auth/internal/server"
	"github.com/fsociety/todo/services/auth/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

type Config struct {
	ListenAddr    string `env:"LISTEN_ADDR"    default:":50051"`
	MigrationsDir string `env:"MIGRATIONS_DIR" default:"file:///migrations/auth"`
	JWTKeyFile    string `env:"JWT_KEY_FILE,required"`
	pkgpgx.Config        // provides DSN (DATABASE_URL), MaxConns, etc.

	// Kafka (optional — omit KAFKA_BROKERS to disable email sending in tests/dev)
	KafkaBrokers string `env:"KAFKA_BROKERS" default:""`

	// OTP code settings
	CodeTTL            time.Duration `env:"CODE_TTL"             default:"10m"`
	CodeResendCooldown time.Duration `env:"CODE_RESEND_COOLDOWN" default:"3m"`
	CodeMaxAttempts    int           `env:"CODE_MAX_ATTEMPTS"    default:"3"`

	// Stripe (optional — omit STRIPE_SECRET_KEY to run without payments)
	StripeSecretKey    string `env:"STRIPE_SECRET_KEY"    default:""`
	StripeWebhookSec   string `env:"STRIPE_WEBHOOK_SECRET" default:""`
	StripePriceID      string `env:"STRIPE_PRICE_ID"      default:""`
	StripeSuccessURL   string `env:"STRIPE_SUCCESS_URL"   default:""`
	StripeCancelURL    string `env:"STRIPE_CANCEL_URL"    default:""`
}

func main() {
	log := pkglogger.New("auth")

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

	maker, err := tokens.NewMakerFromFile(cfg.JWTKeyFile)
	if err != nil {
		log.Error("load JWT key", "err", err)
		os.Exit(1)
	}

	// Kafka producer is optional; omit KAFKA_BROKERS to run without email.
	var producer *pkgkafka.Producer
	if cfg.KafkaBrokers != "" {
		p, err := pkgkafka.NewProducer(strings.Split(cfg.KafkaBrokers, ","), log)
		if err != nil {
			log.Error("create kafka producer", "err", err)
			os.Exit(1)
		}
		defer p.Close()
		producer = p
	} else {
		log.Warn("KAFKA_BROKERS not set — email verification codes will be logged only")
	}

	codeCfg := server.CodeConfig{
		TTL:            cfg.CodeTTL,
		ResendCooldown: cfg.CodeResendCooldown,
		MaxAttempts:    cfg.CodeMaxAttempts,
	}

	stripeCfg := server.StripeConfig{
		SecretKey:     cfg.StripeSecretKey,
		WebhookSecret: cfg.StripeWebhookSec,
		PriceID:       cfg.StripePriceID,
		SuccessURL:    cfg.StripeSuccessURL,
		CancelURL:     cfg.StripeCancelURL,
	}

	svc := server.New(store.New(pool), maker, log, producer, codeCfg, stripeCfg)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(pkgmetrics.UnaryServerInterceptor("auth", log)),
		grpc.ChainStreamInterceptor(pkgmetrics.StreamServerInterceptor("auth", log)),
	)
	authv1.RegisterAuthServiceServer(grpcServer, svc)
	reflection.Register(grpcServer)

	// Standard gRPC health protocol — required for K8s grpc probe.
	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Error("listen", "err", err)
		os.Exit(1)
	}

	// Serve Prometheus metrics on :9090 for scraping.
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", pkgmetrics.Handler())
		if err := http.ListenAndServe(":9090", mux); err != nil {
			log.Error("metrics server", "err", err)
		}
	}()

	log.Info("auth-service starting", "addr", cfg.ListenAddr)

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
