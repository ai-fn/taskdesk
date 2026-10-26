package reaper

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/fsociety/todo/services/todo/internal/store"
)

type fakeStore struct {
	reaped  int
	queue   []store.BlobGCEntry
	done    []int64
	claimed int
}

func (f *fakeStore) ReapAbandoned(_ context.Context, _, _ time.Duration, _ int) (int, error) {
	return f.reaped, nil
}

func (f *fakeStore) ClaimBlobGC(_ context.Context, n int, _ time.Duration) ([]store.BlobGCEntry, error) {
	f.claimed++
	if n > len(f.queue) {
		n = len(f.queue)
	}
	out := f.queue[:n]
	f.queue = f.queue[n:]
	return out, nil
}

func (f *fakeStore) DoneBlobGC(_ context.Context, ids []int64) error {
	f.done = append(f.done, ids...)
	return nil
}

type fakeBlob struct {
	deleted  []string
	aborted  []string
	deleteFn func(key string) error
}

func (f *fakeBlob) Delete(_ context.Context, key string) error {
	if f.deleteFn != nil {
		if err := f.deleteFn(key); err != nil {
			return err
		}
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func (f *fakeBlob) AbortMultipart(_ context.Context, _, uploadID string) error {
	f.aborted = append(f.aborted, uploadID)
	return nil
}

func newReaper(st *fakeStore, b *fakeBlob) *Reaper {
	return New(st, b, slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

func TestDrain_SinglePUT_Deletes(t *testing.T) {
	st := &fakeStore{queue: []store.BlobGCEntry{{ID: 1, ObjectKey: "eu/u/t/a/file.pdf"}}}
	b := &fakeBlob{}
	if err := newReaper(st, b).drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.deleted) != 1 || len(b.aborted) != 0 {
		t.Fatalf("deleted=%v aborted=%v", b.deleted, b.aborted)
	}
	if len(st.done) != 1 || st.done[0] != 1 {
		t.Fatalf("done = %v", st.done)
	}
}

func TestDrain_Multipart_AbortsThenDeletes(t *testing.T) {
	st := &fakeStore{queue: []store.BlobGCEntry{{ID: 7, ObjectKey: "k", UploadID: "up-1"}}}
	b := &fakeBlob{}
	if err := newReaper(st, b).drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.aborted) != 1 || b.aborted[0] != "up-1" || len(b.deleted) != 1 {
		t.Fatalf("aborted=%v deleted=%v", b.aborted, b.deleted)
	}
}

func TestDrain_FailedBlobStaysQueued(t *testing.T) {
	st := &fakeStore{queue: []store.BlobGCEntry{{ID: 1, ObjectKey: "bad"}, {ID: 2, ObjectKey: "ok"}}}
	b := &fakeBlob{deleteFn: func(k string) error {
		if k == "bad" {
			return errors.New("storage down")
		}
		return nil
	}}
	if err := newReaper(st, b).drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.done) != 1 || st.done[0] != 2 {
		t.Fatalf("only the removed blob may be marked done, got %v", st.done)
	}
}

func TestDrain_ProcessesBacklogInBatches(t *testing.T) {
	st := &fakeStore{}
	for i := range 250 {
		st.queue = append(st.queue, store.BlobGCEntry{ID: int64(i + 1), ObjectKey: "k"})
	}
	b := &fakeBlob{}
	if err := newReaper(st, b).drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.deleted) != 250 {
		t.Fatalf("deleted %d of 250", len(b.deleted))
	}
}
