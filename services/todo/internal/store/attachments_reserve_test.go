package store_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
)

func newAtt(userID, taskID, id string, size int64) *domain.Attachment {
	return &domain.Attachment{
		ID: id, UserID: userID, TaskID: taskID, Filename: id + ".bin",
		ContentType: "application/octet-stream", SizeBytes: size,
		ObjectKey: "k/" + id, CreatedAt: time.Now().UTC(),
	}
}

func reserve(t *testing.T, st *store.Store, a *domain.Attachment, uploadID string, lim store.ReserveLimits) error {
	t.Helper()
	return st.ReserveAttachment(context.Background(), a, uploadID, lim)
}

func gcKeys(t *testing.T) map[string]string {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `SELECT object_key, COALESCE(upload_id, '') FROM todo.blob_gc`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, u string
		if err := rows.Scan(&k, &u); err != nil {
			t.Fatal(err)
		}
		out[k] = u
	}
	return out
}

func TestReserve_RejectsTasksTheUserDoesNotOwn(t *testing.T) {
	st := newStoreForAttachments(t)
	seedTask(t, st, "owner", "task_1")

	if err := reserve(t, st, newAtt("intruder", "task_1", "a1", 10), "", store.ReserveLimits{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("foreign task: err = %v, want ErrNotFound", err)
	}
	if err := reserve(t, st, newAtt("owner", "missing", "a2", 10), "", store.ReserveLimits{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing task: err = %v, want ErrNotFound", err)
	}
	if err := st.DeleteTaskWithEvent(context.Background(), "owner", "task_1"); err != nil {
		t.Fatal(err)
	}
	if err := reserve(t, st, newAtt("owner", "task_1", "a3", 10), "", store.ReserveLimits{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted task: err = %v, want ErrNotFound", err)
	}
	if _, err := st.TaskCommittedAttachmentCount(context.Background(), "intruder", "task_1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("count of a foreign task: err = %v, want ErrNotFound", err)
	}
}

func TestReserve_PendingCountsTowardTaskLimit(t *testing.T) {
	st := newStoreForAttachments(t)
	seedTask(t, st, "u", "task_1")
	lim := store.ReserveLimits{MaxFilesPerTask: 2}

	for i := range 2 {
		if err := reserve(t, st, newAtt("u", "task_1", fmt.Sprintf("a%d", i), 10), "", lim); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	if err := reserve(t, st, newAtt("u", "task_1", "a3", 10), "", lim); !errors.Is(err, store.ErrTaskAttachmentFull) {
		t.Fatalf("third pending: err = %v, want ErrTaskAttachmentFull", err)
	}
}

func TestReserve_PendingCountsTowardQuota(t *testing.T) {
	st := newStoreForAttachments(t)
	seedTask(t, st, "u", "task_1")
	lim := store.ReserveLimits{QuotaBytes: 1000}

	if err := reserve(t, st, newAtt("u", "task_1", "a1", 600), "", lim); err != nil {
		t.Fatal(err)
	}
	if err := reserve(t, st, newAtt("u", "task_1", "a2", 600), "", lim); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("second reservation: err = %v, want ErrQuotaExceeded", err)
	}
	if err := reserve(t, st, newAtt("u", "task_1", "a3", 400), "", lim); err != nil {
		t.Fatalf("a reservation that fits: %v", err)
	}
}

func TestReserve_ConcurrentReservationsRespectQuota(t *testing.T) {
	st := newStoreForAttachments(t)
	seedTask(t, st, "u", "task_1")
	lim := store.ReserveLimits{QuotaBytes: 1000}

	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- reserve(t, st, newAtt("u", "task_1", fmt.Sprintf("c%d", i), 300), "", lim)
		}()
	}
	wg.Wait()
	close(results)
	ok := 0
	for err := range results {
		if err == nil {
			ok++
		} else if !errors.Is(err, store.ErrQuotaExceeded) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 3 {
		t.Fatalf("%d reservations of 300 fit a 1000-byte quota, want 3", ok)
	}
}

func TestConfirm_RejectsObjectLargerThanReserved(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	seedTask(t, st, "u", "task_1")
	if err := reserve(t, st, newAtt("u", "task_1", "a1", 100), "", store.ReserveLimits{}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.ConfirmAttachment(ctx, "u", "a1", 101, "en"); !errors.Is(err, store.ErrSizeExceedsDeclared) {
		t.Fatalf("oversize confirm: err = %v, want ErrSizeExceedsDeclared", err)
	}
	if used, _ := st.UserStorageUsed(ctx, "u"); used != 0 {
		t.Errorf("used_bytes = %d after a rejected confirm, want 0", used)
	}

	got, err := st.ConfirmAttachment(ctx, "u", "a1", 90, "en")
	if err != nil {
		t.Fatalf("confirm within the reservation: %v", err)
	}
	if got.SizeBytes != 90 {
		t.Errorf("committed size = %d, want the stored 90", got.SizeBytes)
	}
	if used, _ := st.UserStorageUsed(ctx, "u"); used != 90 {
		t.Errorf("used_bytes = %d, want 90", used)
	}
}

func TestReapAbandoned_FollowsLastActivityAndStatus(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	seedTask(t, st, "u", "task_1")
	old := time.Now().UTC().Add(-3 * time.Hour)

	mk := func(id, uploadID string) {
		a := newAtt("u", "task_1", id, 10)
		a.CreatedAt = old
		if err := reserve(t, st, a, uploadID, store.ReserveLimits{}); err != nil {
			t.Fatal(err)
		}
	}
	mk("single_old", "")
	mk("multi_active", "up-active")
	mk("multi_idle", "up-idle")
	mk("committed_old", "")
	if _, err := st.ConfirmAttachment(ctx, "u", "committed_old", 10, "en"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchAttachment(ctx, "u", "multi_active"); err != nil {
		t.Fatal(err)
	}

	// 3h-old rows: past the 2h single-PUT TTL, inside a 24h multipart TTL.
	n, err := st.ReapAbandoned(ctx, 2*time.Hour, 24*time.Hour, 50)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want only single_old", n)
	}
	// With a 1h multipart TTL the idle one goes too, the recently touched one stays.
	if n, err = st.ReapAbandoned(ctx, 2*time.Hour, time.Hour, 50); err != nil || n != 1 {
		t.Fatalf("second pass reaped %d (%v), want multi_idle only", n, err)
	}
	for _, id := range []string{"multi_active", "committed_old"} {
		if _, err := st.GetAttachment(ctx, "u", id); err != nil {
			t.Errorf("%s was reaped: %v", id, err)
		}
	}

	keys := gcKeys(t)
	if keys["k/single_old"] != "" || keys["k/multi_idle"] != "up-idle" || len(keys) != 2 {
		t.Errorf("blob_gc = %v, want single_old (no upload) and multi_idle (up-idle)", keys)
	}
}

func TestReapAbandoned_SkipsRowsLockedByConfirm(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	seedTask(t, st, "u", "task_1")
	a := newAtt("u", "task_1", "a1", 10)
	a.CreatedAt = time.Now().UTC().Add(-5 * time.Hour)
	if err := reserve(t, st, a, "", store.ReserveLimits{}); err != nil {
		t.Fatal(err)
	}

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM todo.attachments WHERE id = 'a1' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}

	done := make(chan int, 1)
	go func() {
		n, _ := st.ReapAbandoned(ctx, time.Hour, time.Hour, 50)
		done <- n
	}()
	select {
	case n := <-done:
		if n != 0 {
			t.Errorf("reaped %d locked rows", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReapAbandoned blocked on a locked row")
	}
}

func TestDeleteAttachment_QueuesBlobForRemoval(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	seedTask(t, st, "u", "task_1")
	if err := reserve(t, st, newAtt("u", "task_1", "multi", 10), "up-1", store.ReserveLimits{}); err != nil {
		t.Fatal(err)
	}
	if err := reserve(t, st, newAtt("u", "task_1", "single", 10), "", store.ReserveLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConfirmAttachment(ctx, "u", "single", 10, "en"); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"multi", "single"} {
		if _, err := st.DeleteAttachment(ctx, "u", id); err != nil {
			t.Fatal(err)
		}
	}
	keys := gcKeys(t)
	if v, ok := keys["k/multi"]; !ok || v != "up-1" {
		t.Errorf("pending multipart must queue its upload id, got %v", keys)
	}
	if _, ok := keys["k/single"]; !ok {
		t.Errorf("committed attachment must queue its blob, got %v", keys)
	}
}

func TestPurgeTask_ReleasesStorageAndQueuesBlobs(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	seedTask(t, st, "u", "parent")
	now := time.Now().UTC()
	if err := st.CreateTask(ctx, &domain.Task{ID: "child", UserID: "u", Title: "c", ParentTaskID: ptr("parent"), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	seedTask(t, st, "u", "other")

	for _, c := range []struct{ id, task string }{{"p1", "parent"}, {"c1", "child"}, {"o1", "other"}} {
		if err := reserve(t, st, newAtt("u", c.task, c.id, 100), "", store.ReserveLimits{}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.ConfirmAttachment(ctx, "u", c.id, 100, "en"); err != nil {
			t.Fatal(err)
		}
	}
	if used, _ := st.UserStorageUsed(ctx, "u"); used != 300 {
		t.Fatalf("setup: used = %d", used)
	}

	if err := st.PurgeTask(ctx, "u", "parent"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("purge of a live task: err = %v, want ErrNotFound", err)
	}
	if err := st.DeleteTaskWithEvent(ctx, "u", "parent"); err != nil {
		t.Fatal(err)
	}
	if err := st.PurgeTask(ctx, "u", "parent"); err != nil {
		t.Fatal(err)
	}

	if used, _ := st.UserStorageUsed(ctx, "u"); used != 100 {
		t.Errorf("used_bytes = %d after purge, want 100 (only 'other' remains)", used)
	}
	keys := gcKeys(t)
	if _, p := keys["k/p1"]; !p {
		t.Errorf("blob of the purged task not queued: %v", keys)
	}
	if _, c := keys["k/c1"]; !c {
		t.Errorf("blob of the purged subtask not queued: %v", keys)
	}
	if _, o := keys["k/o1"]; o {
		t.Errorf("blob of an unrelated task queued: %v", keys)
	}
}

func TestListAttachments_Cursor(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	seedTask(t, st, "u", "task_1")
	for i := range 3 {
		a := newAtt("u", "task_1", fmt.Sprintf("a%d", i), 10)
		a.CreatedAt = time.Now().UTC().Add(time.Duration(i) * time.Second)
		if err := reserve(t, st, a, "", store.ReserveLimits{}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.ConfirmAttachment(ctx, "u", a.ID, 10, "en"); err != nil {
			t.Fatal(err)
		}
	}

	page1, next, err := st.ListAttachments(ctx, "u", "task_1", 2, "")
	if err != nil || len(page1) != 2 || next == "" {
		t.Fatalf("page 1: %d rows, next %q, err %v", len(page1), next, err)
	}
	page2, next2, err := st.ListAttachments(ctx, "u", "task_1", 2, next)
	if err != nil || len(page2) != 1 || next2 != "" {
		t.Fatalf("page 2: %d rows, next %q, err %v", len(page2), next2, err)
	}
	emptyID := base64.RawURLEncoding.EncodeToString([]byte(`{"c":"2020-01-01T00:00:00Z","i":""}`))
	for _, bad := range []string{"garbage", "!!!", `{"c":"2020-01-01T00:00:00Z","i":"x"}`, emptyID} {
		if _, _, err := st.ListAttachments(ctx, "u", "task_1", 2, bad); !errors.Is(err, store.ErrInvalidCursor) {
			t.Errorf("cursor %q: err = %v, want ErrInvalidCursor", bad, err)
		}
	}
}

func TestBlobGC_ClaimLeaseAndDone(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	seedTask(t, st, "u", "task_1")
	for _, id := range []string{"a", "b"} {
		if err := reserve(t, st, newAtt("u", "task_1", id, 1), "", store.ReserveLimits{}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DeleteAttachment(ctx, "u", id); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.ClaimBlobGC(ctx, 10, time.Minute)
	if err != nil || len(got) != 2 {
		t.Fatalf("claim: %d entries, %v", len(got), err)
	}
	again, _ := st.ClaimBlobGC(ctx, 10, time.Minute)
	if len(again) != 0 {
		t.Errorf("leased entries were claimed again: %v", again)
	}
	if err := st.DoneBlobGC(ctx, []int64{got[0].ID}); err != nil {
		t.Fatal(err)
	}
	if left := gcKeys(t); len(left) != 1 {
		t.Errorf("blob_gc has %d rows after one Done, want 1", len(left))
	}
}

func TestPurgeTask_ConcurrentWithDeleteDoesNotDoubleRelease(t *testing.T) {
	for i := range 5 {
		st := newStoreForAttachments(t)
		ctx := context.Background()
		seedTask(t, st, "u", "t1")
		seedTask(t, st, "u", "t2")
		for _, c := range []struct{ id, task string }{{"a", "t1"}, {"b", "t2"}} {
			if err := reserve(t, st, newAtt("u", c.task, c.id, 100), "", store.ReserveLimits{}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.ConfirmAttachment(ctx, "u", c.id, 100, "en"); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.DeleteTaskWithEvent(ctx, "u", "t1"); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); errs <- st.PurgeTask(ctx, "u", "t1") }()
		go func() { defer wg.Done(); _, err := st.DeleteAttachment(ctx, "u", "a"); errs <- err }()
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("round %d: %v (a deadlock surfaces here as 40P01)", i, err)
			}
		}
		if used, _ := st.UserStorageUsed(ctx, "u"); used != 100 {
			t.Fatalf("round %d: used_bytes = %d, want 100 (t2's file only)", i, used)
		}
	}
}

func TestConfirm_ConcurrentWithPurgeDoesNotDeadlock(t *testing.T) {
	for i := range 5 {
		st := newStoreForAttachments(t)
		ctx := context.Background()
		seedTask(t, st, "u", "t1")
		if err := reserve(t, st, newAtt("u", "t1", "a", 100), "", store.ReserveLimits{}); err != nil {
			t.Fatal(err)
		}
		if err := st.DeleteTaskWithEvent(ctx, "u", "t1"); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); errs <- st.PurgeTask(ctx, "u", "t1") }()
		go func() { defer wg.Done(); _, err := st.ConfirmAttachment(ctx, "u", "a", 100, "en"); errs <- err }()
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("round %d: %v", i, err)
			}
		}
		if used, _ := st.UserStorageUsed(ctx, "u"); used != 0 {
			t.Fatalf("round %d: used_bytes = %d, want 0", i, used)
		}
	}
}

func TestTouchAttachment_OnlyPending(t *testing.T) {
	st := newStoreForAttachments(t)
	ctx := context.Background()
	seedTask(t, st, "u", "t1")
	if err := reserve(t, st, newAtt("u", "t1", "a", 10), "up", store.ReserveLimits{}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchAttachment(ctx, "u", "a"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchAttachment(ctx, "other", "a"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("foreign user: err = %v, want ErrNotFound", err)
	}
	if _, err := st.DeleteAttachment(ctx, "u", "a"); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchAttachment(ctx, "u", "a"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted upload: err = %v, want ErrNotFound", err)
	}
}
