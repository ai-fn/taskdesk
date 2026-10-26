package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	pkgblobstore "github.com/fsociety/todo/pkg/blobstore"
	pkgconfig "github.com/fsociety/todo/pkg/config"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	"github.com/fsociety/todo/pkg/lifecycle"
	pkglogger "github.com/fsociety/todo/pkg/logger"
	pkgmetrics "github.com/fsociety/todo/pkg/metrics"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/notification/internal/consumer"
	"github.com/fsociety/todo/services/notification/internal/mailer"
	"github.com/fsociety/todo/services/notification/internal/scanner"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Config struct {
	KafkaBrokers string `env:"KAFKA_BROKERS"  default:"todo-kafka-bootstrap.data.svc.cluster.local:9092"`
	KafkaGroup   string `env:"KAFKA_GROUP"    default:"notification-service"`
	KafkaAuthGrp string `env:"KAFKA_AUTH_GROUP" default:"notification-auth"`
	RedisAddr    string `env:"REDIS_ADDR"     default:"redis.data.svc.cluster.local:6379"`
	SMTPAddr     string `env:"SMTP_ADDR"      default:"mailpit.data.svc.cluster.local:1025"`
	SMTPFrom     string `env:"SMTP_FROM"      default:"noreply@todo.local"`
	SMTPUser     string `env:"SMTP_USER"      default:""`
	SMTPPass     string `env:"SMTP_PASS"      default:""`

	// Own group for file.uploaded so a long malware scan never delays emails.
	KafkaScanGroup string `env:"KAFKA_SCAN_GROUP" default:"notification-scan"`

	// todo-service gRPC (for the UpdateScanStatus callback).
	TodoAddr string `env:"TODO_ADDR" default:"todo.todo.svc.cluster.local:50052"`

	// ClamAV. Empty is an error unless SCAN_DISABLED=true (dev: every file is approved unscanned).
	ClamAVAddr   string `env:"CLAMAV_ADDR"   default:""`
	ScanDisabled bool   `env:"SCAN_DISABLED" default:"false"`
	// Objects above this are quarantined unscanned; keep it <= clamd StreamMaxLength.
	ScanMaxBytes int64 `env:"SCAN_MAX_BYTES" default:"2147483648"`
}

// todoScanUpdater wraps the todo-service gRPC client to satisfy consumer.ScanResultUpdater.
type todoScanUpdater struct {
	client todov1.TodoServiceClient
}

func (u *todoScanUpdater) UpdateScanStatus(ctx context.Context, attachmentID, scanStatus string) error {
	_, err := u.client.UpdateScanStatus(ctx, &todov1.UpdateScanStatusRequest{
		AttachmentId: attachmentID,
		ScanStatus:   scanStatus,
	})
	return err
}

func main() {
	log := pkglogger.New("notification")

	cfg, err := pkgconfig.Load[Config]()
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	blobCfg, err := pkgconfig.Load[pkgblobstore.Config]()
	if err != nil {
		log.Error("load blob config", "err", err)
		os.Exit(1)
	}
	blobStore := pkgblobstore.New(blobCfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	brokers := strings.Split(cfg.KafkaBrokers, ",")

	// A hung SMTP session can hold a batch for up to maxAttempts*session timeout per record.
	slowHandler := []pkgkafka.ConsumerOption{pkgkafka.WithMaxPollRecords(3), pkgkafka.WithRebalanceTimeout(5 * time.Minute)}
	kafkaConsumer, err := pkgkafka.NewConsumer(brokers, cfg.KafkaGroup, consumer.Topics(), log, slowHandler...)
	if err != nil {
		log.Error("create kafka consumer", "err", err)
		os.Exit(1)
	}
	defer kafkaConsumer.Close()

	authConsumer, err := pkgkafka.NewConsumer(brokers, cfg.KafkaAuthGrp, consumer.AuthTopics(), log, slowHandler...)
	if err != nil {
		log.Error("create auth kafka consumer", "err", err)
		os.Exit(1)
	}
	defer authConsumer.Close()
	// One object per poll and a long rebalance window: a scan of a multi-GiB object can run for many minutes.
	scanOpts := []pkgkafka.ConsumerOption{pkgkafka.WithMaxPollRecords(1), pkgkafka.WithRebalanceTimeout(time.Hour)}
	scanConsumer, err := pkgkafka.NewConsumer(brokers, cfg.KafkaScanGroup, consumer.ScanTopics(), log, scanOpts...)
	if err != nil {
		log.Error("create kafka scan consumer", "err", err)
		os.Exit(1)
	}
	defer scanConsumer.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer func() {
		if err := rdb.Close(); err != nil {
			log.Warn("close redis client", "err", err)
		}
	}()

	todoConn, err := grpc.NewClient(cfg.TodoAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial todo-service", "err", err)
		os.Exit(1)
	}
	defer func() { _ = todoConn.Close() }()
	todoClient := todov1.NewTodoServiceClient(todoConn)

	scn, err := newScanner(cfg)
	if err != nil {
		log.Error("scanner config", "err", err)
		os.Exit(1)
	}
	if scn == nil {
		log.Warn("SCAN_DISABLED is set; all uploaded files are approved without scanning")
	} else {
		log.Info("ClamAV scanning enabled", "addr", cfg.ClamAVAddr, "max_bytes", cfg.ScanMaxBytes)
	}

	m := mailer.New(cfg.SMTPAddr, cfg.SMTPFrom, cfg.SMTPUser, cfg.SMTPPass)
	h := consumer.New(m, rdb, scn, blobStore, &todoScanUpdater{client: todoClient}, log).WithScanLimit(cfg.ScanMaxBytes)

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", pkgmetrics.Handler())
		mux.Handle("/healthz", lifecycle.LiveHandler())
		// SMTP is left out: a mail outage is retried/DLQ'd and must not restart or unroute the pod.
		mux.Handle("/readyz", lifecycle.ReadyHandler(lifecycle.ProbeTimeout, log,
			lifecycle.Check{Name: "redis", Fn: func(ctx context.Context) error { return rdb.Ping(ctx).Err() }},
			lifecycle.Check{Name: "kafka-events", Fn: kafkaConsumer.Ping},
			lifecycle.Check{Name: "kafka-auth", Fn: authConsumer.Ping},
		))
		if err := http.ListenAndServe(":9090", mux); err != nil {
			log.Error("metrics server", "err", err)
		}
	}()

	log.Info("notification-service starting",
		"brokers", cfg.KafkaBrokers,
		"group", cfg.KafkaGroup,
		"auth_group", cfg.KafkaAuthGrp,
		"redis", cfg.RedisAddr,
		"todo", cfg.TodoAddr,
		"clamav", cfg.ClamAVAddr,
	)

	// Either loop ending takes the other down so the pod restarts instead of half-working.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var failed atomic.Bool
	for name, c := range map[string]*pkgkafka.Consumer{"events": kafkaConsumer, "auth": authConsumer, "scan": scanConsumer} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()
			err := consumer.Run(runCtx, brokers, c, h)
			if err != nil || runCtx.Err() == nil { // ending without a shutdown signal is a failure
				log.Error("consumer stopped", "consumer", name, "err", err)
				failed.Store(true)
			}
		}()
	}
	wg.Wait()
	if failed.Load() {
		os.Exit(1)
	}
}

// newScanner returns a true nil interface when scanning is disabled; a typed-nil *Scanner would make the handler scan with it.
func newScanner(cfg Config) (consumer.ScannerClient, error) {
	s, err := scanner.FromConfig(cfg.ClamAVAddr, cfg.ScanDisabled)
	if err != nil || s == nil {
		return nil, err
	}
	return s, nil
}
