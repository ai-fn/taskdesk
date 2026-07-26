// Package kafka provides thin franz-go wrappers for the todo app services.
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Producer wraps a franz-go client configured for fire-and-forget publishing.
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
		kgo.AllowAutoTopicCreation(),     // topics are pre-created via KafkaTopic CRD; this is a safety net
		kgo.ProducerBatchMaxBytes(1<<20), // 1 MiB
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

// Close flushes and shuts down the underlying client.
func (p *Producer) Close() {
	p.client.Close()
}
