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
	KafkaAuthGrp string `env:"KAFKA_AUTH_GROUP" default:"notification-auth"`
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

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer func() {
		if err := rdb.Close(); err != nil {
			log.Warn("close redis client", "err", err)
		}
	}()

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
		"auth_group", cfg.KafkaAuthGrp,
		"redis", cfg.RedisAddr,
	)

	// Either loop ending takes the other down so the pod restarts instead of half-working.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var failed atomic.Bool
	for name, c := range map[string]*pkgkafka.Consumer{"events": kafkaConsumer, "auth": authConsumer} {
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
