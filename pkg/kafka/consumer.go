package kafka

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// DLQSuffix is appended to a record's topic to name its dead-letter topic.
const DLQSuffix = ".dlq"

const (
	defaultMaxAttempts = 5
	defaultBaseBackoff = 200 * time.Millisecond
	maxBackoff         = 5 * time.Second
	commitTimeout      = 2 * time.Second
	maxPollRecords     = 100 // bounds the redelivery window after a crash
	maxErrLen          = 256
)

// Handler processes one record. Errors are retried, then dead-lettered, so it must
// be idempotent and return ctx errors.
type Handler func(ctx context.Context, record *kgo.Record) error

// client is the subset of *kgo.Client the consumer uses; faked in tests.
type client interface {
	PollRecords(ctx context.Context, maxPollRecords int) kgo.Fetches
	AllowRebalance()
	CommitRecords(ctx context.Context, rs ...*kgo.Record) error
	ProduceSync(ctx context.Context, rs ...*kgo.Record) kgo.ProduceResults
	Close()
}

// Consumer wraps a franz-go client in a simple poll loop.
type Consumer struct {
	client      client
	groupID     string
	log         *slog.Logger
	maxAttempts int
	baseBackoff time.Duration
}

// NewConsumer creates a consumer joining groupID and subscribing to topics.
func NewConsumer(brokers []string, groupID string, topics []string, log *slog.Logger) (*Consumer, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeTopics(topics...),
		kgo.DisableAutoCommit(),    // we commit manually after handler succeeds
		kgo.BlockRebalanceOnPoll(), // commits must not touch partitions revoked mid-batch
	)
	if err != nil {
		return nil, fmt.Errorf("kafka consumer: %w", err)
	}
	return newConsumer(cl, groupID, log), nil
}

func newConsumer(cl client, groupID string, log *slog.Logger) *Consumer {
	return &Consumer{client: cl, groupID: groupID, log: log, maxAttempts: defaultMaxAttempts, baseBackoff: defaultBaseBackoff}
}

// Run calls h per record in partition order; a record is committed only once handled
// or dead-lettered. A DLQ outage past the rebalance timeout evicts the member.
func (c *Consumer) Run(ctx context.Context, h Handler) error {
	for {
		fetches := c.client.PollRecords(ctx, maxPollRecords)
		if fetches.IsClientClosed() {
			return nil
		}
		// Their positions already advanced, so records next to a fetch error must still be handled.
		fetches.EachError(func(topic string, partition int32, err error) {
			if ctx.Err() == nil {
				c.log.Error("kafka fetch error", "topic", topic, "partition", partition, "err", err)
			}
		})

		done := make(map[topicPartition]*kgo.Record)
		it := fetches.RecordIter()
		for !it.Done() && ctx.Err() == nil { // handlers must not see records after shutdown starts
			r := it.Next()
			if err := c.process(ctx, h, r); err != nil {
				break // ctx cancelled; commit what was handled
			}
			done[topicPartition{r.Topic, r.Partition}] = r
		}
		c.commit(ctx, done)
		c.client.AllowRebalance()

		if ctx.Err() != nil {
			return nil
		}
	}
}

// Close shuts down the consumer.
func (c *Consumer) Close() {
	c.client.Close()
}

type topicPartition struct {
	topic     string
	partition int32
}

// process retries h, then dead-letters r. It errors only when ctx is cancelled.
func (c *Consumer) process(ctx context.Context, h Handler, r *kgo.Record) error {
	var err error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		if err = h(ctx, r); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.log.Warn("handler error", "topic", r.Topic, "partition", r.Partition, "offset", r.Offset, "attempt", attempt, "err", err)
		if attempt < c.maxAttempts && !sleep(ctx, c.backoff(attempt)) {
			return ctx.Err()
		}
	}
	return c.deadLetter(ctx, r, err)
}

// deadLetter retries until the DLQ accepts r: giving up would drop the record.
func (c *Consumer) deadLetter(ctx context.Context, r *kgo.Record, cause error) error {
	headers := make([]kgo.RecordHeader, 0, len(r.Headers)+5)
	headers = append(headers, r.Headers...)
	headers = append(headers,
		kgo.RecordHeader{Key: "dlq-source-topic", Value: []byte(r.Topic)},
		kgo.RecordHeader{Key: "dlq-source-partition", Value: []byte(strconv.Itoa(int(r.Partition)))},
		kgo.RecordHeader{Key: "dlq-source-offset", Value: []byte(strconv.FormatInt(r.Offset, 10))},
		kgo.RecordHeader{Key: "dlq-consumer-group", Value: []byte(c.groupID)},
		kgo.RecordHeader{Key: "dlq-error", Value: []byte(truncate(cause.Error(), maxErrLen))},
	)
	dlq := &kgo.Record{Topic: r.Topic + DLQSuffix, Key: r.Key, Value: r.Value, Headers: headers}

	for attempt := 1; ; attempt++ {
		err := c.client.ProduceSync(ctx, dlq).FirstErr()
		if err == nil {
			c.log.Error("record dead-lettered", "topic", r.Topic, "partition", r.Partition, "offset", r.Offset, "err", cause)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.log.Error("dead-letter produce failed", "topic", dlq.Topic, "attempt", attempt, "err", err)
		if !sleep(ctx, c.backoff(attempt)) {
			return ctx.Err()
		}
	}
}

// commit commits the last handled record per partition; detached from ctx so shutdown keeps the handled prefix.
func (c *Consumer) commit(ctx context.Context, done map[topicPartition]*kgo.Record) {
	if len(done) == 0 {
		return
	}
	recs := make([]*kgo.Record, 0, len(done))
	for _, r := range done {
		recs = append(recs, r)
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
	defer cancel()
	if err := c.client.CommitRecords(cctx, recs...); err != nil {
		c.log.Error("commit error", "err", err)
	}
}

func (c *Consumer) backoff(attempt int) time.Duration {
	d := c.baseBackoff << (attempt - 1)
	if d <= 0 || d > maxBackoff {
		return maxBackoff
	}
	return d
}

// sleep waits d and reports false if ctx ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
