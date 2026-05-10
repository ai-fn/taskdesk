// Package outbox contains the background worker that drains todo.outbox to Kafka.
package outbox

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/fsociety/todo/pkg/events"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	"github.com/fsociety/todo/services/todo/internal/store"
)

const (
	batchSize     = 50
	pollInterval  = 500 * time.Millisecond
)

// Publisher polls todo.outbox and produces unpublished events to Kafka.
type Publisher struct {
	store    *store.Store
	producer *pkgkafka.Producer
	log      *slog.Logger
}

func New(st *store.Store, producer *pkgkafka.Producer, log *slog.Logger) *Publisher {
	return &Publisher{store: st, producer: producer, log: log}
}

// Run loops until ctx is cancelled.
func (p *Publisher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
			if err := p.flush(ctx); err != nil && ctx.Err() == nil {
				p.log.Error("outbox flush", "err", err)
			}
		}
	}
}

func (p *Publisher) flush(ctx context.Context) error {
	entries, err := p.store.UnpublishedBatch(ctx, batchSize)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	var published []string
	for _, e := range entries {
		// Decode just to get the user_id for use as the Kafka partition key.
		var ev events.TaskEvent
		if err := json.Unmarshal(e.Payload, &ev); err != nil {
			p.log.Error("decode outbox payload", "id", e.ID, "err", err)
			continue
		}

		if err := p.producer.Publish(ctx, events.Topic, ev.UserID, ev); err != nil {
			p.log.Error("publish to kafka", "id", e.ID, "err", err)
			continue
		}
		published = append(published, e.ID)
	}

	return p.store.MarkPublished(ctx, published)
}
