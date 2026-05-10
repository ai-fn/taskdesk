package kafka

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Handler processes a single Kafka record. Return a non-nil error to nack.
type Handler func(ctx context.Context, record *kgo.Record) error

// Consumer wraps a franz-go client in a simple poll loop.
type Consumer struct {
	client *kgo.Client
	log    *slog.Logger
}

// NewConsumer creates a consumer joining groupID and subscribing to topics.
func NewConsumer(brokers []string, groupID string, topics []string, log *slog.Logger) (*Consumer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeTopics(topics...),
		kgo.DisableAutoCommit(), // we commit manually after handler succeeds
	)
	if err != nil {
		return nil, fmt.Errorf("kafka consumer: %w", err)
	}
	return &Consumer{client: client, log: log}, nil
}

// Run polls Kafka in a loop, calling h for each record.
// It commits offsets only after h returns nil.
// Blocks until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context, h Handler) error {
	for {
		fetches := c.client.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return nil
		}
		if err := fetches.Err(); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			c.log.Error("kafka fetch error", "err", err)
			continue
		}

		var commitErr error
		fetches.EachRecord(func(r *kgo.Record) {
			if err := h(ctx, r); err != nil {
				c.log.Error("handler error", "topic", r.Topic, "offset", r.Offset, "err", err)
				// leave offset uncommitted — record will be redelivered
				return
			}
			if err := c.client.CommitRecords(ctx, r); err != nil {
				c.log.Error("commit error", "err", err)
				commitErr = err
			}
		})
		if commitErr != nil && ctx.Err() != nil {
			return nil
		}
	}
}

// Close shuts down the consumer.
func (c *Consumer) Close() {
	c.client.Close()
}
