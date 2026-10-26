// Package reaper removes abandoned pending uploads and drains the blob GC queue: rows are
// dropped (with their blobs queued) in one DB transaction, then the blobs are removed from
// object storage with retries, so a storage outage never orphans an object.
package reaper

import (
	"context"
	"log/slog"
	"time"

	"github.com/fsociety/todo/services/todo/internal/store"
)

const (
	// A single-PUT URL lives 15 min; a multipart upload may run for hours between part URLs.
	singlePutTTL = 2 * time.Hour
	multipartTTL = 24 * time.Hour

	defaultBatch    = 100
	defaultInterval = time.Minute
	reapEvery       = 10 // gc drain runs every tick, the abandoned-upload scan every reapEvery-th
	// gcBatch * gcOpTimeout <= gcLease, so a claimed batch is finished before its lease lapses.
	gcBatch          = 10
	gcOpTimeout      = 30 * time.Second
	gcLease          = 5 * time.Minute
	maxGCBatchPerRun = 50 // bounds one tick under a large backlog
)

// Store is the subset of store.Store the reaper needs.
type Store interface {
	ReapAbandoned(ctx context.Context, singlePutTTL, multipartTTL time.Duration, limit int) (int, error)
	ClaimBlobGC(ctx context.Context, n int, lease time.Duration) ([]store.BlobGCEntry, error)
	DoneBlobGC(ctx context.Context, ids []int64) error
}

// Blob is the subset of blobstore.Store the reaper needs.
type Blob interface {
	Delete(ctx context.Context, key string) error
	AbortMultipart(ctx context.Context, key, uploadID string) error
}

// Reaper periodically drops abandoned uploads and removes the blobs queued for deletion.
type Reaper struct {
	store    Store
	blob     Blob
	batch    int
	interval time.Duration
	log      *slog.Logger
}

// New returns a Reaper with sensible defaults.
func New(store Store, blob Blob, log *slog.Logger) *Reaper {
	return &Reaper{store: store, blob: blob, batch: defaultBatch, interval: defaultInterval, log: log}
}

// Run loops until ctx is cancelled.
func (r *Reaper) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for tick := 0; ; tick++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if tick%reapEvery == 0 {
			if err := r.reap(ctx); err != nil && ctx.Err() == nil {
				r.log.Error("reaper cycle", "err", err)
			}
		}
		if err := r.drain(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("blob gc cycle", "err", err)
		}
	}
}

func (r *Reaper) reap(ctx context.Context) error {
	n, err := r.store.ReapAbandoned(ctx, singlePutTTL, multipartTTL, r.batch)
	if err != nil {
		return err
	}
	if n > 0 {
		r.log.Info("reaper cycle", "reaped", n)
	}
	return nil
}

// drain removes queued blobs; an entry whose removal fails stays queued (its lease lapses) and is retried.
func (r *Reaper) drain(ctx context.Context) error {
	for range maxGCBatchPerRun {
		entries, err := r.store.ClaimBlobGC(ctx, gcBatch, gcLease)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return nil
		}
		var done []int64
		for _, e := range entries {
			if err := r.remove(ctx, e); err != nil {
				r.log.Warn("blob gc", "key", e.ObjectKey, "err", err)
				continue
			}
			done = append(done, e.ID)
		}
		if err := r.store.DoneBlobGC(ctx, done); err != nil {
			return err
		}
		if len(entries) < gcBatch {
			return nil
		}
	}
	return nil
}

// remove aborts a pending multipart upload (a no-op once completed) and then deletes the key.
func (r *Reaper) remove(ctx context.Context, e store.BlobGCEntry) error {
	ctx, cancel := context.WithTimeout(ctx, gcOpTimeout)
	defer cancel()
	if e.UploadID != "" {
		if err := r.blob.AbortMultipart(ctx, e.ObjectKey, e.UploadID); err != nil {
			return err
		}
	}
	return r.blob.Delete(ctx, e.ObjectKey)
}
