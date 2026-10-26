// Package kafka provides thin franz-go wrappers for the todo app services.
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Producer wraps a franz-go client configured for acknowledged (acks=all, idempotent) publishing.
// The caller is responsible for providing messages with the correct topic.
type Producer struct {
	client *kgo.Client
	log    *slog.Logger
}

// NewProducer creates a producer connected to the given brokers.
// It returns an error if the initial connection attempt fails.
func NewProducer(brokers []string, log *slog.Logger) (*Producer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ProducerBatchMaxBytes(1<<20),   // 1 MiB
		kgo.RequiredAcks(kgo.AllISRAcks()), // idempotent writes (franz-go's default) need acks=all
		kgo.RecordRetries(5),
		kgo.RecordDeliveryTimeout(10*time.Second), // fail instead of blocking while Kafka is down
	)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: %w", err)
	}
	return &Producer{client: client, log: log}, nil
}

// Publish encodes v as JSON and produces it to topic with key.
// It blocks until the record is acknowledged by the broker.
func (p *Producer) Publish(ctx context.Context, topic, key string, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	results := p.client.ProduceSync(ctx, &kgo.Record{
		Topic: topic,
		Key:   []byte(key),
		Value: payload,
	})
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("produce to %s: %w", topic, err)
	}
	return nil
}

// Message is one record for PublishBatch.
type Message struct {
	Key   string
	Value []byte
}

// PublishBatch produces msgs to topic in order and waits for all acks. The returned
// errors are index-aligned with msgs; nil means that record was acknowledged.
func (p *Producer) PublishBatch(ctx context.Context, topic string, msgs []Message) []error {
	recs := make([]*kgo.Record, len(msgs))
	for i, m := range msgs {
		recs[i] = &kgo.Record{Topic: topic, Key: []byte(m.Key), Value: m.Value}
	}
	return alignErrors(recs, p.client.ProduceSync(ctx, recs...), topic)
}

// alignErrors maps results (which arrive in completion order) back onto recs.
func alignErrors(recs []*kgo.Record, results kgo.ProduceResults, topic string) []error {
	byRec := make(map[*kgo.Record]error, len(results))
	for _, r := range results {
		byRec[r.Record] = r.Err
	}
	errs := make([]error, len(recs))
	for i, r := range recs {
		err, ok := byRec[r]
		switch {
		case !ok:
			errs[i] = fmt.Errorf("produce to %s: no result", topic)
		case err != nil:
			errs[i] = fmt.Errorf("produce to %s: %w", topic, err)
		}
	}
	return errs
}

// Close flushes and shuts down the underlying client.
func (p *Producer) Close() {
	p.client.Close()
}
