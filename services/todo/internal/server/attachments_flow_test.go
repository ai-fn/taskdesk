package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/blobstore"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
	"github.com/fsociety/todo/services/todo/internal/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// memBlob is an in-memory blobstore.Store; sizes maps object key → stored size.
type memBlob struct {
	sizes      map[string]int64
	presigned  []int64
	presignErr error
	aborted    []string
	completed  [][]blobstore.CompletedPart
}

func newMemBlob() *memBlob { return &memBlob{sizes: map[string]int64{}} }

func (b *memBlob) PresignPut(_ context.Context, key, _ string, size int64, _ time.Duration) (string, error) {
	if b.presignErr != nil {
		return "", b.presignErr
	}
	b.presigned = append(b.presigned, size)
	return "http://blob/" + key, nil
}
func (b *memBlob) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "http://blob/get/" + key, nil
}
func (b *memBlob) Head(_ context.Context, key string) (int64, error) {
	n, ok := b.sizes[key]
	if !ok {
		return 0, errors.New("not found")
	}
	return n, nil
}
func (b *memBlob) Delete(_ context.Context, key string) error { delete(b.sizes, key); return nil }
func (b *memBlob) InitiateMultipart(context.Context, string, string) (string, error) {
	return "upload-1", nil
}
func (b *memBlob) PresignUploadPart(_ context.Context, key, _ string, n int32, _ time.Duration) (string, error) {
	return "http://blob/part/" + key, nil
}
func (b *memBlob) CompleteMultipart(_ context.Context, _, _ string, parts []blobstore.CompletedPart) error {
	b.completed = append(b.completed, parts)
	return nil
}
func (b *memBlob) AbortMultipart(_ context.Context, _, uploadID string) error {
	b.aborted = append(b.aborted, uploadID)
	return nil
}
func (b *memBlob) Get(context.Context, string) (io.ReadCloser, error) { return nil, errors.New("n/a") }

func newFlowServer(t *testing.T) (*Server, *store.Store, *memBlob) {
	t.Helper()
	testutil.Truncate(t, testPool)
	st := store.New(testPool)
	blob := newMemBlob()
	return New(st, blob, "eu", slog.New(slog.NewTextHandler(io.Discard, nil))), st, blob
}

func seedTask(t *testing.T, st *store.Store, userID, taskID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.CreateTask(context.Background(), &domain.Task{ID: taskID, UserID: userID, Title: "t", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("code = %v (%v), want %v", got, err, want)
	}
}

func uploadReq(user, task string, size int64, plan string) *todov1.CreateAttachmentUploadURLRequest {
	return &todov1.CreateAttachmentUploadURLRequest{UserId: user, TaskId: task, Filename: "f.bin", SizeBytes: size, Plan: plan}
}

func TestCreateUploadURL_UnknownPlanGetsFreeLimits(t *testing.T) {
	s, st, _ := newFlowServer(t)
	seedTask(t, st, "u", "t1")
	for _, plan := range []string{"", "Pro", "enterprise"} {
		_, err := s.CreateAttachmentUploadURL(t.Context(), uploadReq("u", "t1", 11<<20, plan))
		wantCode(t, err, codes.ResourceExhausted)
	}
	if _, err := s.CreateAttachmentUploadURL(t.Context(), uploadReq("u", "t1", 11<<20, "pro")); err != nil {
		t.Fatalf("pro: %v", err)
	}
}

func TestCreateUploadURL_TaskMustBeOwned(t *testing.T) {
	s, st, blob := newFlowServer(t)
	seedTask(t, st, "owner", "t1")
	_, err := s.CreateAttachmentUploadURL(t.Context(), uploadReq("intruder", "t1", 10, "pro"))
	wantCode(t, err, codes.NotFound)
	_, err = s.InitiateMultipartUpload(t.Context(), &todov1.InitiateMultipartUploadRequest{
		UserId: "intruder", TaskId: "t1", Filename: "f.bin", SizeBytes: multipartThreshold, Plan: "pro"})
	wantCode(t, err, codes.NotFound)
	if len(blob.aborted) != 1 {
		t.Errorf("multipart started for a foreign task was not aborted: %v", blob.aborted)
	}
}

func TestCreateUploadURL_SignsDeclaredSize(t *testing.T) {
	s, st, blob := newFlowServer(t)
	seedTask(t, st, "u", "t1")
	if _, err := s.CreateAttachmentUploadURL(t.Context(), uploadReq("u", "t1", 4242, "free")); err != nil {
		t.Fatal(err)
	}
	if len(blob.presigned) != 1 || blob.presigned[0] != 4242 {
		t.Errorf("presigned sizes = %v, want [4242]", blob.presigned)
	}
}

func TestCreateUploadURL_FreeQuotaCountsPendingUploads(t *testing.T) {
	s, st, _ := newFlowServer(t)
	seedTask(t, st, "u", "t1")
	// 10 MiB each: 102 fit in 1 GiB if they were committed, but they are only reserved.
	for i := range freeMaxFilesPerTask {
		if _, err := s.CreateAttachmentUploadURL(t.Context(), uploadReq("u", "t1", 10<<20, "free")); err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
	}
	_, err := s.CreateAttachmentUploadURL(t.Context(), uploadReq("u", "t1", 1, "free"))
	wantCode(t, err, codes.ResourceExhausted)
}

func TestCreateUploadURL_PresignFailureReleasesReservation(t *testing.T) {
	s, st, blob := newFlowServer(t)
	seedTask(t, st, "u", "t1")
	blob.presignErr = errors.New("boom")
	_, err := s.CreateAttachmentUploadURL(t.Context(), uploadReq("u", "t1", 10, "free"))
	wantCode(t, err, codes.Internal)

	var n int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM todo.attachments`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("attachment rows = %d (%v), want the reservation released", n, err)
	}
}

func TestSinglePutThresholdMatchesMultipart(t *testing.T) {
	s, st, _ := newFlowServer(t)
	seedTask(t, st, "u", "t1")
	_, err := s.CreateAttachmentUploadURL(t.Context(), uploadReq("u", "t1", multipartThreshold, "pro"))
	wantCode(t, err, codes.InvalidArgument)
	if _, err := s.CreateAttachmentUploadURL(t.Context(), uploadReq("u", "t1", multipartThreshold-1, "pro")); err != nil {
		t.Fatalf("just below the threshold: %v", err)
	}
	if _, err := s.InitiateMultipartUpload(t.Context(), &todov1.InitiateMultipartUploadRequest{
		UserId: "u", TaskId: "t1", Filename: "f.bin", SizeBytes: multipartThreshold, Plan: "pro"}); err != nil {
		t.Fatalf("multipart at exactly the threshold: %v", err)
	}
}

func TestConfirm_OversizeObjectIsDiscarded(t *testing.T) {
	s, st, blob := newFlowServer(t)
	seedTask(t, st, "u", "t1")
	resp, err := s.CreateAttachmentUploadURL(t.Context(), uploadReq("u", "t1", 100, "free"))
	if err != nil {
		t.Fatal(err)
	}
	att, _ := st.GetAttachment(t.Context(), "u", resp.AttachmentId)
	blob.sizes[att.ObjectKey] = 5 << 20 // bypassed the signed length somehow

	_, err = s.ConfirmAttachment(t.Context(), &todov1.ConfirmAttachmentRequest{UserId: "u", AttachmentId: resp.AttachmentId})
	wantCode(t, err, codes.FailedPrecondition)
	if _, err := st.GetAttachment(t.Context(), "u", resp.AttachmentId); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("oversize attachment survived: %v", err)
	}
	if used, _ := st.UserStorageUsed(t.Context(), "u"); used != 0 {
		t.Errorf("used_bytes = %d, want 0", used)
	}
	var queued int
	_ = testPool.QueryRow(t.Context(), `SELECT count(*) FROM todo.blob_gc WHERE object_key = $1`, att.ObjectKey).Scan(&queued)
	if queued != 1 {
		t.Errorf("oversize blob not queued for removal")
	}
}

func TestGetUploadPartURL_PartNumberRange(t *testing.T) {
	s, st, _ := newFlowServer(t)
	seedTask(t, st, "u", "t1")
	init, err := s.InitiateMultipartUpload(t.Context(), &todov1.InitiateMultipartUploadRequest{
		UserId: "u", TaskId: "t1", Filename: "f.bin", SizeBytes: multipartThreshold, Plan: "pro"})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int32{0, -1, 10001} {
		_, err := s.GetUploadPartURL(t.Context(), &todov1.GetUploadPartURLRequest{UserId: "u", AttachmentId: init.AttachmentId, PartNumber: n})
		wantCode(t, err, codes.InvalidArgument)
	}
	if _, err := s.GetUploadPartURL(t.Context(), &todov1.GetUploadPartURLRequest{UserId: "u", AttachmentId: init.AttachmentId, PartNumber: 10000}); err != nil {
		t.Fatalf("part 10000: %v", err)
	}
	var touched bool
	_ = testPool.QueryRow(t.Context(), `SELECT last_activity_at IS NOT NULL FROM todo.attachments WHERE id = $1`, init.AttachmentId).Scan(&touched)
	if !touched {
		t.Error("requesting a part URL must stamp last_activity_at")
	}
}

func TestCompleteMultipart_ValidatesAndSortsParts(t *testing.T) {
	s, st, blob := newFlowServer(t)
	seedTask(t, st, "u", "t1")
	init, err := s.InitiateMultipartUpload(t.Context(), &todov1.InitiateMultipartUploadRequest{
		UserId: "u", TaskId: "t1", Filename: "f.bin", SizeBytes: multipartThreshold, Plan: "pro"})
	if err != nil {
		t.Fatal(err)
	}
	att, _ := st.GetAttachment(t.Context(), "u", init.AttachmentId)
	blob.sizes[att.ObjectKey] = multipartThreshold

	complete := func(parts ...*todov1.CompletedPart) error {
		_, err := s.CompleteMultipartUpload(t.Context(), &todov1.CompleteMultipartUploadRequest{UserId: "u", AttachmentId: init.AttachmentId, Parts: parts})
		return err
	}
	wantCode(t, complete(&todov1.CompletedPart{PartNumber: 1}), codes.InvalidArgument)                                                             // empty etag
	wantCode(t, complete(&todov1.CompletedPart{PartNumber: 0, Etag: "e"}), codes.InvalidArgument)                                                  // out of range
	wantCode(t, complete(&todov1.CompletedPart{PartNumber: 1, Etag: "a"}, &todov1.CompletedPart{PartNumber: 1, Etag: "b"}), codes.InvalidArgument) // duplicate
	if len(blob.completed) != 0 {
		t.Fatal("storage was asked to complete an invalid part list")
	}
	if err := complete(&todov1.CompletedPart{PartNumber: 2, Etag: "b"}, &todov1.CompletedPart{PartNumber: 1, Etag: "a"}); err != nil {
		t.Fatal(err)
	}
	got := blob.completed[0]
	if got[0].PartNumber != 1 || got[1].PartNumber != 2 {
		t.Errorf("parts sent to storage = %v, want ascending", got)
	}
}

func TestDeleteAttachment_PendingMultipartIsAbortedByGC(t *testing.T) {
	s, st, _ := newFlowServer(t)
	seedTask(t, st, "u", "t1")
	init, err := s.InitiateMultipartUpload(t.Context(), &todov1.InitiateMultipartUploadRequest{
		UserId: "u", TaskId: "t1", Filename: "f.bin", SizeBytes: multipartThreshold, Plan: "pro"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteAttachment(t.Context(), &todov1.DeleteAttachmentRequest{UserId: "u", AttachmentId: init.AttachmentId}); err != nil {
		t.Fatal(err)
	}
	entries, err := st.ClaimBlobGC(t.Context(), 10, time.Minute)
	if err != nil || len(entries) != 1 || entries[0].UploadID != "upload-1" {
		t.Fatalf("gc entries = %v (%v), want the upload id queued for abort", entries, err)
	}
}

func TestListAttachments_BadPageToken(t *testing.T) {
	s, st, _ := newFlowServer(t)
	seedTask(t, st, "u", "t1")
	_, err := s.ListAttachments(t.Context(), &todov1.ListAttachmentsRequest{UserId: "u", TaskId: "t1", PageToken: strings.Repeat("x", 8)})
	wantCode(t, err, codes.InvalidArgument)
}
