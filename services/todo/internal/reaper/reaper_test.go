package reaper

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/fsociety/todo/services/todo/internal/domain"
)

// fakeStore satisfies Store for tests.
type fakeStore struct {
	atts []*domain.Attachment
}

func (f *fakeStore) ListAbandonedAttachments(_ context.Context, _ time.Duration, _ int) ([]*domain.Attachment, error) {
	return f.atts, nil
}

func (f *fakeStore) PurgeAttachmentRow(_ context.Context, attID string) error {
	for i, a := range f.atts {
		if a.ID == attID {
			f.atts = append(f.atts[:i], f.atts[i+1:]...)
			return nil
		}
	}
	return nil
}

// fakeBlob records which operations were called.
type fakeBlob struct {
	deleted []string
	aborted []struct{ key, uploadID string }
}

func (f *fakeBlob) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}

func (f *fakeBlob) AbortMultipart(_ context.Context, key, uploadID string) error {
	f.aborted = append(f.aborted, struct{ key, uploadID string }{key, uploadID})
	return nil
}

func TestReap_SinglePUT_Delete(t *testing.T) {
	att := &domain.Attachment{
		ID: "att_1", ObjectKey: "eu/u/t/a/file.pdf", UploadID: "",
	}
	store := &fakeStore{atts: []*domain.Attachment{att}}
	blob := &fakeBlob{}
	r := New(store, blob, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if err := r.reap(context.Background()); err != nil {
		t.Fatalf("reap: %v", err)
	}
	if len(blob.deleted) != 1 || blob.deleted[0] != att.ObjectKey {
		t.Fatalf("want Delete(%q), got deleted=%v", att.ObjectKey, blob.deleted)
	}
	if len(blob.aborted) != 0 {
		t.Fatalf("want no AbortMultipart calls, got %v", blob.aborted)
	}
}

func TestReap_Multipart_Abort(t *testing.T) {
	att := &domain.Attachment{
		ID: "att_2", ObjectKey: "eu/u/t/a/big.mp4", UploadID: "upload-abc-123",
	}
	store := &fakeStore{atts: []*domain.Attachment{att}}
	blob := &fakeBlob{}
	r := New(store, blob, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if err := r.reap(context.Background()); err != nil {
		t.Fatalf("reap: %v", err)
	}
	if len(blob.aborted) != 1 {
		t.Fatalf("want 1 AbortMultipart call, got %d", len(blob.aborted))
	}
	if blob.aborted[0].key != att.ObjectKey || blob.aborted[0].uploadID != att.UploadID {
		t.Fatalf("AbortMultipart args wrong: got %+v", blob.aborted[0])
	}
	if len(blob.deleted) != 0 {
		t.Fatalf("want no Delete calls, got %v", blob.deleted)
	}
}
