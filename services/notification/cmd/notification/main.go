package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	pkgblobstore "github.com/fsociety/todo/pkg/blobstore"
	pkgconfig "github.com/fsociety/todo/pkg/config"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
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

	kafkaConsumer, err := pkgkafka.NewConsumer(brokers, cfg.KafkaGroup, consumer.Topics(), log)
	if err != nil {
		log.Error("create kafka consumer", "err", err)
		os.Exit(1)
	}
	defer kafkaConsumer.Close()

	scanConsumer, err := pkgkafka.NewConsumer(brokers, cfg.KafkaScanGroup, consumer.ScanTopics(), log)
	if err != nil {
		log.Error("create kafka scan consumer", "err", err)
		os.Exit(1)
	}
	defer scanConsumer.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()

	todoConn, err := grpc.NewClient(cfg.TodoAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial todo-service", "err", err)
		os.Exit(1)
	}
	defer todoConn.Close()
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
		if err := http.ListenAndServe(":9090", mux); err != nil {
			log.Error("metrics server", "err", err)
		}
	}()

	log.Info("notification-service starting",
		"brokers", cfg.KafkaBrokers,
		"group", cfg.KafkaGroup,
		"redis", cfg.RedisAddr,
		"todo", cfg.TodoAddr,
		"clamav", cfg.ClamAVAddr,
	)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 2)
	go func() { errc <- consumer.Run(runCtx, brokers, kafkaConsumer, h) }()
	go func() { errc <- consumer.Run(runCtx, brokers, scanConsumer, h) }()
	err = <-errc
	cancel()
	<-errc
	if err != nil {
		log.Error("consumer error", "err", err)
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
