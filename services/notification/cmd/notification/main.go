package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	pkgconfig "github.com/fsociety/todo/pkg/config"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	pkglogger "github.com/fsociety/todo/pkg/logger"
	pkgmetrics "github.com/fsociety/todo/pkg/metrics"
	"github.com/fsociety/todo/services/notification/internal/consumer"
	"github.com/fsociety/todo/services/notification/internal/mailer"
	"github.com/redis/go-redis/v9"
)

type Config struct {
	KafkaBrokers string `env:"KAFKA_BROKERS"  default:"todo-kafka-bootstrap.data.svc.cluster.local:9092"`
	KafkaGroup   string `env:"KAFKA_GROUP"    default:"notification-service"`
	RedisAddr    string `env:"REDIS_ADDR"     default:"redis.data.svc.cluster.local:6379"`
	SMTPAddr     string `env:"SMTP_ADDR"      default:"mailpit.data.svc.cluster.local:1025"`
	SMTPFrom     string `env:"SMTP_FROM"      default:"noreply@todo.local"`
	SMTPUser     string `env:"SMTP_USER"      default:""`
	SMTPPass     string `env:"SMTP_PASS"      default:""`
}

func main() {
	log := pkglogger.New("notification")

	cfg, err := pkgconfig.Load[Config]()
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	brokers := strings.Split(cfg.KafkaBrokers, ",")

	kafkaConsumer, err := pkgkafka.NewConsumer(brokers, cfg.KafkaGroup, consumer.Topics(), log)
	if err != nil {
		log.Error("create kafka consumer", "err", err)
		os.Exit(1)
	}
	defer kafkaConsumer.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()

	m := mailer.New(cfg.SMTPAddr, cfg.SMTPFrom, cfg.SMTPUser, cfg.SMTPPass)
	h := consumer.New(m, rdb, log)

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
	)

	if err := consumer.Run(ctx, brokers, kafkaConsumer, h); err != nil {
		log.Error("consumer error", "err", err)
		os.Exit(1)
	}
}
