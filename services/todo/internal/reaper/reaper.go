// Package reaper removes abandoned pending uploads — attachment rows created
// when a client began an upload but never called ConfirmAttachment. The reaper
// deletes the blob from object storage first, then purges the DB row.
package reaper

import (
	"context"
	"log/slog"
	"time"

	"github.com/fsociety/todo/services/todo/internal/domain"
)

const (
	defaultTTL      = 2 * time.Hour
	defaultBatch    = 100
	defaultInterval = 10 * time.Minute
)

// Store is the subset of store.Store the reaper needs.
type Store interface {
	ListAbandonedAttachments(ctx context.Context, ttl time.Duration, limit int) ([]*domain.Attachment, error)
	PurgeAttachmentRow(ctx context.Context, attID string) error
}

// Blob is the subset of blobstore.Store the reaper needs.
type Blob interface {
	Delete(ctx context.Context, key string) error
}

// Reaper periodically scans for pending attachments that have exceeded the TTL
// and cleans them up: blob deletion first, then row purge.
type Reaper struct {
	store    Store
	blob     Blob
	ttl      time.Duration
	batch    int
	interval time.Duration
	log      *slog.Logger
}

// New returns a Reaper with sensible defaults.
func New(store Store, blob Blob, log *slog.Logger) *Reaper {
	return &Reaper{
		store:    store,
		blob:     blob,
		ttl:      defaultTTL,
		batch:    defaultBatch,
		interval: defaultInterval,
		log:      log,
	}
}

// Run loops until ctx is cancelled, calling reap() on each tick.
func (r *Reaper) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.interval):
			if err := r.reap(ctx); err != nil && ctx.Err() == nil {
				r.log.Error("reaper cycle", "err", err)
			}
		}
	}
}

func (r *Reaper) reap(ctx context.Context) error {
	atts, err := r.store.ListAbandonedAttachments(ctx, r.ttl, r.batch)
	if err != nil {
		return err
	}
	for _, a := range atts {
		if err := r.blob.Delete(ctx, a.ObjectKey); err != nil {
			// Log but continue — the row purge below is still safe even if
			// the blob is already absent or temporarily inaccessible.
			r.log.Warn("reaper blob delete", "key", a.ObjectKey, "err", err)
		}
		if err := r.store.PurgeAttachmentRow(ctx, a.ID); err != nil {
			r.log.Error("reaper purge row", "id", a.ID, "err", err)
		}
	}
	if len(atts) > 0 {
		r.log.Info("reaper cycle", "reaped", len(atts))
	}
	return nil
}
