// Package outbox contains the background worker that drains todo.outbox to Kafka.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/fsociety/todo/pkg/events"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	"github.com/fsociety/todo/services/todo/internal/store"
)

const (
	batchSize    = 100
	pollInterval = 500 * time.Millisecond
	// claimLease also paces retries of rows Kafka rejected.
	claimLease = 30 * time.Second
	// produceTimeout must stay well under claimLease, or a slow produce overlaps another publisher's claim.
	produceTimeout = claimLease / 2
	// markTimeout bounds recording acks after shutdown, so delivered rows aren't re-sent.
	markTimeout = 5 * time.Second
	markRetries = 3

	pruneInterval   = time.Minute
	pruneBatch      = 1000
	maxPruneBatches = 10 // per tick, so a large backlog never stalls publishing
	// retention bounds how long published rows (and the emails in their payloads) are kept.
	retention = 7 * 24 * time.Hour
)

// producer is the part of *pkgkafka.Producer the publisher uses; faked in tests.
type producer interface {
	PublishBatch(ctx context.Context, topic string, msgs []pkgkafka.Message) []error
}

// Publisher claims pending todo.outbox rows and produces them to Kafka. Replicas may run
// it concurrently: each row is claimed by one at a time. Delivery is at-least-once and
// ordering per user is best-effort across replicas and retries.
type Publisher struct {
	store    *store.Store
	producer producer
	log      *slog.Logger
}

func New(st *store.Store, p *pkgkafka.Producer, log *slog.Logger) *Publisher {
	return &Publisher{store: st, producer: p, log: log}
}

// Run loops until ctx is cancelled, draining without pause while batches come back full.
func (p *Publisher) Run(ctx context.Context) {
	nextPrune := time.Now().Add(rand.N(pruneInterval)) // jitter so replicas don't prune together
	for ctx.Err() == nil {
		full, err := p.flush(ctx)
		if err != nil && ctx.Err() == nil {
			p.log.Error("outbox flush", "err", err)
		}
		if !time.Now().Before(nextPrune) {
			nextPrune = time.Now().Add(pruneInterval)
			p.prune(ctx)
		}
		if full && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

// flush publishes one claimed batch and reports whether the queue likely has more ready.
// A batch with rejected records is not "full": looping on it would burn through the backlog.
func (p *Publisher) flush(ctx context.Context) (full bool, err error) {
	entries, err := p.store.ClaimOutbox(ctx, batchSize, claimLease)
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return false, nil
	}

	msgs := make([]pkgkafka.Message, 0, len(entries))
	sent := make([]store.OutboxEntry, 0, len(entries))
	for _, e := range entries {
		// The user_id is the partition key, so one user's events stay ordered.
		var key struct {
			UserID string `json:"user_id"`
		}
		if err := json.Unmarshal(e.Payload, &key); err != nil {
			p.log.Error("undecodable outbox payload, parking it", "id", e.ID, "err", err)
			if err := p.store.MarkOutboxFailed(ctx, e.ID, fmt.Sprintf("decode payload: %v", err)); err != nil {
				p.log.Error("park outbox row", "id", e.ID, "err", err)
			}
			continue
		}
		msgs = append(msgs, pkgkafka.Message{Key: key.UserID, Value: e.Payload})
		sent = append(sent, e)
	}

	pctx, pcancel := context.WithTimeout(ctx, produceTimeout)
	defer pcancel()
	var published, rejected []string
	for i, perr := range p.producer.PublishBatch(pctx, events.Topic, msgs) {
		if perr != nil {
			p.log.Error("publish to kafka", "id", sent[i].ID, "err", perr)
			rejected = append(rejected, sent[i].ID)
			continue
		}
		published = append(published, sent[i].ID)
	}

	// Detached from ctx: acks must be recorded, and leases freed, even during shutdown.
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
	defer cancel()
	if err := p.markPublished(mctx, published); err != nil {
		return false, fmt.Errorf("mark published: %w", err)
	}
	if ctx.Err() != nil { // shutting down: let another replica retry now instead of after the lease
		if err := p.store.ReleaseOutbox(mctx, rejected); err != nil {
			p.log.Warn("release outbox leases", "err", err)
		}
	}
	// Otherwise rejected rows stay leased: lease expiry is their retry backoff.
	return len(entries) == batchSize && len(rejected) == 0, nil
}

// markPublished retries briefly: a lost mark re-sends the whole batch after the lease expires.
func (p *Publisher) markPublished(ctx context.Context, ids []string) (err error) {
	for attempt := 0; attempt < markRetries; attempt++ {
		if err = p.store.MarkPublished(ctx, ids); err == nil {
			return nil
		}
		if attempt == markRetries-1 {
			break
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(100 * time.Millisecond << attempt):
		}
	}
	return err
}

func (p *Publisher) prune(ctx context.Context) {
	for i := 0; i < maxPruneBatches && ctx.Err() == nil; i++ {
		n, err := p.store.PruneOutbox(ctx, retention, pruneBatch)
		if err != nil {
			if ctx.Err() == nil {
				p.log.Error("outbox prune", "err", err)
			}
			return
		}
		if n < pruneBatch {
			return
		}
	}
}
