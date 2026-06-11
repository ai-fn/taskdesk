package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
	"github.com/fsociety/todo/services/todo/internal/testutil"
)

func newStoreForAttachments(t *testing.T) *store.Store {
	t.Helper()
	testutil.Truncate(t, testPool)
	return store.New(testPool)
}

func seedTask(t *testing.T, st *store.Store, userID, taskID string) {
	t.Helper()
	now := time.Now().UTC()
	err := st.CreateTask(context.Background(), &domain.Task{
		ID:        taskID,
		UserID:    userID,
		Title:     "test task",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seedTask: %v", err)
	}
}

func TestAttachments_PendingAndConfirm(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	const (
		userID = "user_1"
		taskID = "task_1"
		attID  = "att_1"
	)
	seedTask(t, st, userID, taskID)

	now := time.Now().UTC()
	a := &domain.Attachment{
		ID:          attID,
		UserID:      userID,
		TaskID:      taskID,
		Filename:    "report.pdf",
		ContentType: "application/pdf",
		SizeBytes:   1024,
		ObjectKey:   "eu/user_1/task_1/att_1/report.pdf",
		CreatedAt:   now,
	}

	if err := st.CreateAttachmentPending(ctx, a); err != nil {
		t.Fatalf("CreateAttachmentPending: %v", err)
	}

	// Quota should not be charged yet.
	used, err := st.UserStorageUsed(ctx, userID)
	if err != nil {
		t.Fatalf("UserStorageUsed: %v", err)
	}
	if used != 0 {
		t.Fatalf("want used=0 before confirm, got %d", used)
	}

	// Task counter should not be bumped yet.
	count, err := st.TaskCommittedAttachmentCount(ctx, taskID)
	if err != nil {
		t.Fatalf("TaskCommittedAttachmentCount: %v", err)
	}
	if count != 0 {
		t.Fatalf("want count=0 before confirm, got %d", count)
	}

	confirmed, err := st.ConfirmAttachment(ctx, userID, attID, a.SizeBytes)
	if err != nil {
		t.Fatalf("ConfirmAttachment: %v", err)
	}
	if confirmed.Status != domain.AttachmentCommitted {
		t.Fatalf("want status=%q, got %q", domain.AttachmentCommitted, confirmed.Status)
	}

	// Quota and counter should be bumped.
	used, err = st.UserStorageUsed(ctx, userID)
	if err != nil {
		t.Fatalf("UserStorageUsed after confirm: %v", err)
	}
	if used != a.SizeBytes {
		t.Fatalf("want used=%d, got %d", a.SizeBytes, used)
	}

	count, err = st.TaskCommittedAttachmentCount(ctx, taskID)
	if err != nil {
		t.Fatalf("TaskCommittedAttachmentCount after confirm: %v", err)
	}
	if count != 1 {
		t.Fatalf("want count=1, got %d", count)
	}
}

func TestAttachments_ScanStatusPendingAfterConfirm(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	const (
		userID = "user_sc1"
		taskID = "task_sc1"
		attID  = "att_sc1"
	)
	seedTask(t, st, userID, taskID)

	now := time.Now().UTC()
	a := &domain.Attachment{
		ID: attID, UserID: userID, TaskID: taskID,
		Filename: "doc.pdf", ContentType: "application/pdf",
		SizeBytes: 512, ObjectKey: "key/sc1", CreatedAt: now,
	}
	if err := st.CreateAttachmentPending(ctx, a); err != nil {
		t.Fatalf("CreateAttachmentPending: %v", err)
	}

	confirmed, err := st.ConfirmAttachment(ctx, userID, attID, a.SizeBytes)
	if err != nil {
		t.Fatalf("ConfirmAttachment: %v", err)
	}
	if confirmed.ScanStatus != domain.ScanPending {
		t.Errorf("want scan_status=%q after confirm, got %q", domain.ScanPending, confirmed.ScanStatus)
	}
}

func TestUpdateScanStatus(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	const (
		userID = "user_sc2"
		taskID = "task_sc2"
		attID  = "att_sc2"
	)
	seedTask(t, st, userID, taskID)

	now := time.Now().UTC()
	a := &domain.Attachment{
		ID: attID, UserID: userID, TaskID: taskID,
		Filename: "img.png", ContentType: "image/png",
		SizeBytes: 200, ObjectKey: "key/sc2", CreatedAt: now,
	}
	if err := st.CreateAttachmentPending(ctx, a); err != nil {
		t.Fatalf("CreateAttachmentPending: %v", err)
	}
	if _, err := st.ConfirmAttachment(ctx, userID, attID, a.SizeBytes); err != nil {
		t.Fatalf("ConfirmAttachment: %v", err)
	}

	// Transition to quarantined.
	if err := st.UpdateScanStatus(ctx, attID, domain.ScanQuarantined); err != nil {
		t.Fatalf("UpdateScanStatus quarantined: %v", err)
	}
	got, err := st.GetAttachment(ctx, userID, attID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if got.ScanStatus != domain.ScanQuarantined {
		t.Errorf("want %q, got %q", domain.ScanQuarantined, got.ScanStatus)
	}

	// Transition to clean.
	if err := st.UpdateScanStatus(ctx, attID, domain.ScanClean); err != nil {
		t.Fatalf("UpdateScanStatus clean: %v", err)
	}
	got2, _ := st.GetAttachment(ctx, userID, attID)
	if got2.ScanStatus != domain.ScanClean {
		t.Errorf("want %q, got %q", domain.ScanClean, got2.ScanStatus)
	}

	// Non-existent ID returns ErrNotFound.
	if err := st.UpdateScanStatus(ctx, "no-such-id", domain.ScanClean); err != store.ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestAttachments_ConfirmIdempotentNotFound(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	const (
		userID = "user_1"
		taskID = "task_1"
		attID  = "att_1"
	)
	seedTask(t, st, userID, taskID)

	now := time.Now().UTC()
	a := &domain.Attachment{
		ID: attID, UserID: userID, TaskID: taskID,
		Filename: "f.pdf", ContentType: "application/pdf",
		SizeBytes: 100, ObjectKey: "key", CreatedAt: now,
	}
	if err := st.CreateAttachmentPending(ctx, a); err != nil {
		t.Fatalf("CreateAttachmentPending: %v", err)
	}
	if _, err := st.ConfirmAttachment(ctx, userID, attID, a.SizeBytes); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	// Second confirm should return not-found (row is no longer pending).
	_, err := st.ConfirmAttachment(ctx, userID, attID, a.SizeBytes)
	if err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound on double-confirm, got %v", err)
	}
}

func TestAttachments_ListAndGet(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	const (
		userID = "user_1"
		taskID = "task_1"
	)
	seedTask(t, st, userID, taskID)

	// Insert 3 committed attachments.
	for i := 0; i < 3; i++ {
		attID := fmt.Sprintf("att_%d", i)
		now := time.Now().UTC().Add(time.Duration(i) * time.Second)
		a := &domain.Attachment{
			ID: attID, UserID: userID, TaskID: taskID,
			Filename: fmt.Sprintf("f%d.pdf", i), ContentType: "application/pdf",
			SizeBytes: 100, ObjectKey: fmt.Sprintf("key/%d", i), CreatedAt: now,
		}
		if err := st.CreateAttachmentPending(ctx, a); err != nil {
			t.Fatalf("CreateAttachmentPending %d: %v", i, err)
		}
		if _, err := st.ConfirmAttachment(ctx, userID, attID, a.SizeBytes); err != nil {
			t.Fatalf("ConfirmAttachment %d: %v", i, err)
		}
	}

	atts, next, err := st.ListAttachments(ctx, userID, taskID, 2, "")
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(atts) != 2 {
		t.Fatalf("want 2 items (page size), got %d", len(atts))
	}
	if next == "" {
		t.Fatal("want non-empty next cursor")
	}

	// Fetch second page.
	page2, next2, err := st.ListAttachments(ctx, userID, taskID, 2, next)
	if err != nil {
		t.Fatalf("ListAttachments page2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("want 1 item on page2, got %d", len(page2))
	}
	if next2 != "" {
		t.Fatalf("want empty next cursor on last page, got %q", next2)
	}

	// GetAttachment
	got, err := st.GetAttachment(ctx, userID, atts[0].ID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if got.ID != atts[0].ID {
		t.Fatalf("GetAttachment ID mismatch: want %q, got %q", atts[0].ID, got.ID)
	}
}

func TestAttachments_Delete(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	const (
		userID = "user_1"
		taskID = "task_1"
		attID  = "att_1"
	)
	seedTask(t, st, userID, taskID)

	now := time.Now().UTC()
	a := &domain.Attachment{
		ID: attID, UserID: userID, TaskID: taskID,
		Filename: "f.pdf", ContentType: "application/pdf",
		SizeBytes: 500, ObjectKey: "key", CreatedAt: now,
	}
	if err := st.CreateAttachmentPending(ctx, a); err != nil {
		t.Fatalf("CreateAttachmentPending: %v", err)
	}
	if _, err := st.ConfirmAttachment(ctx, userID, attID, a.SizeBytes); err != nil {
		t.Fatalf("ConfirmAttachment: %v", err)
	}

	if _, err := st.DeleteAttachment(ctx, userID, attID); err != nil {
		t.Fatalf("DeleteAttachment: %v", err)
	}

	// Quota and counter should be decremented.
	used, err := st.UserStorageUsed(ctx, userID)
	if err != nil {
		t.Fatalf("UserStorageUsed: %v", err)
	}
	if used != 0 {
		t.Fatalf("want used=0 after delete, got %d", used)
	}

	count, err := st.TaskCommittedAttachmentCount(ctx, taskID)
	if err != nil {
		t.Fatalf("TaskCommittedAttachmentCount: %v", err)
	}
	if count != 0 {
		t.Fatalf("want count=0 after delete, got %d", count)
	}

	// Second delete should return not-found.
	_, err = st.DeleteAttachment(ctx, userID, attID)
	if err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound on double-delete, got %v", err)
	}
}

func TestAttachments_ListAbandoned(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	const (
		userID = "user_1"
		taskID = "task_1"
	)
	seedTask(t, st, userID, taskID)

	// Insert a pending attachment with a backdated created_at.
	old := &domain.Attachment{
		ID: "att_old", UserID: userID, TaskID: taskID,
		Filename: "old.pdf", ContentType: "application/pdf",
		SizeBytes: 100, ObjectKey: "k/old",
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour),
	}
	if err := st.CreateAttachmentPending(ctx, old); err != nil {
		t.Fatalf("CreateAttachmentPending old: %v", err)
	}

	// Insert a recent pending attachment.
	recent := &domain.Attachment{
		ID: "att_new", UserID: userID, TaskID: taskID,
		Filename: "new.pdf", ContentType: "application/pdf",
		SizeBytes: 100, ObjectKey: "k/new",
		CreatedAt: time.Now().UTC(),
	}
	if err := st.CreateAttachmentPending(ctx, recent); err != nil {
		t.Fatalf("CreateAttachmentPending new: %v", err)
	}

	abandoned, err := st.ListAbandonedAttachments(ctx, time.Hour, 50)
	if err != nil {
		t.Fatalf("ListAbandonedAttachments: %v", err)
	}
	if len(abandoned) != 1 {
		t.Fatalf("want 1 abandoned, got %d", len(abandoned))
	}
	if abandoned[0].ID != "att_old" {
		t.Fatalf("want att_old, got %s", abandoned[0].ID)
	}

	// PurgeAttachmentRow removes the row without touching counters.
	if err := st.PurgeAttachmentRow(ctx, "att_old"); err != nil {
		t.Fatalf("PurgeAttachmentRow: %v", err)
	}
	abandoned2, err := st.ListAbandonedAttachments(ctx, time.Hour, 50)
	if err != nil {
		t.Fatalf("ListAbandonedAttachments after purge: %v", err)
	}
	if len(abandoned2) != 0 {
		t.Fatalf("want 0 abandoned after purge, got %d", len(abandoned2))
	}
}

func TestMultipart_CreateAndGetUploadID(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	const (
		userID   = "user_1"
		taskID   = "task_1"
		attID    = "att_mp1"
		uploadID = "upload-xyz-123"
	)
	seedTask(t, st, userID, taskID)

	now := time.Now().UTC()
	att := &domain.Attachment{
		ID:          attID,
		UserID:      userID,
		TaskID:      taskID,
		Filename:    "bigfile.mp4",
		ContentType: "video/mp4",
		SizeBytes:   200 << 20,
		ObjectKey:   "eu/user_1/task_1/att_mp1/bigfile.mp4",
		CreatedAt:   now,
	}
	if err := st.CreateMultipartPending(ctx, att, uploadID); err != nil {
		t.Fatalf("CreateMultipartPending: %v", err)
	}

	got, err := st.GetAttachment(ctx, userID, attID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if got.UploadID != uploadID {
		t.Fatalf("UploadID = %q, want %q", got.UploadID, uploadID)
	}
	if got.Status != domain.AttachmentPending {
		t.Fatalf("Status = %v, want pending", got.Status)
	}
}
