# File Attachments — Multipart/Resumable Upload Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the single-PUT attachment flow with a multipart/resumable upload path that lets pro users upload files larger than 100 MiB (up to the ~100 GiB soft cap) directly to object storage in parallel parts, with per-part presigned URLs re-issuable on failure.

**Architecture:** Three new RPCs (and matching HTTP endpoints) cover the multipart lifecycle: `InitiateMultipartUpload` (creates a pending row + S3 multipart upload, returns a per-part presign capability), `GetUploadPartURL` (server looks up the S3 `upload_id` from the DB and returns a fresh presigned URL for any part number — the S3 upload_id never leaves the server), and `CompleteMultipartUpload` (client sends `[(part_number, etag), …]`; server calls S3 complete, HEAD-verifies the assembled object, then reuses the existing `ConfirmAttachment` store method to flip the row to `committed` and bump counters atomically). The abandoned-upload reaper is updated to abort in-progress multipart uploads (not just delete committed objects) by calling `AbortMultipartUpload` when the attachment row has a non-empty `upload_id`. The single-PUT flow gains a `multipartThreshold` cap (100 MiB) that forces pro users with larger files to the multipart endpoint.

**Tech Stack:** Go, `pgx/v5`, `aws-sdk-go-v2/service/s3` (multipart presign + `CreateMultipartUpload` / `CompleteMultipartUpload` / `AbortMultipartUpload`), gRPC + protobuf, chi (gateway), testcontainers (Postgres integration), stdlib `testing`.

**Depends on:** `feat/attachments-core` — all types, store methods, and blobstore from Plan 1 are assumed present.

**Deferred (do NOT build here):**
- *Plan 3 — Malware scan pipeline:* `file.uploaded` consumer + download gate on `scan_status`.
- *Plan 4 — Frontend:* upload UI with progress, i18n keys (EN + ZH), `TaskCard` attachment indicator.

---

## Canonical names (keep consistent across all tasks)

- `multipartThreshold int64 = 100 << 20` — single-PUT cap; files above this must use multipart
- blobstore new methods: `InitiateMultipart`, `PresignUploadPart`, `CompleteMultipart`, `AbortMultipart`
- blobstore new type: `CompletedPart{PartNumber int32, ETag string}`
- store new method: `CreateMultipartPending(ctx, att *domain.Attachment, uploadID string) error`
- domain new field: `domain.Attachment.UploadID string` (empty = single-PUT, non-empty = multipart)
- reaper new `Blob` method: `AbortMultipart(ctx context.Context, key, uploadID string) error`
- new proto RPCs: `InitiateMultipartUpload`, `GetUploadPartURL`, `CompleteMultipartUpload`
- gateway routes (relative to `/{taskID}/attachments`):
  - `POST /multipart/initiate` → initiate
  - `GET /{attachmentID}/parts/{partNumber}` → get part URL
  - `POST /{attachmentID}/multipart/complete` → complete

---

## File map

| Action | Path |
|--------|------|
| Modify | `pkg/blobstore/blobstore.go` |
| Modify | `pkg/blobstore/blobstore_test.go` |
| Create | `migrations/todo/000012_attachment_upload_id.up.sql` |
| Create | `migrations/todo/000012_attachment_upload_id.down.sql` |
| Copy   | both migration files → `deploy/k8s/base/todo/` |
| Modify | `deploy/k8s/base/todo/kustomization.yaml` |
| Modify | `services/todo/internal/domain/attachment.go` |
| Modify | `services/todo/internal/store/attachments.go` |
| Modify | `services/todo/internal/store/attachments_test.go` |
| Modify | `services/todo/internal/reaper/reaper.go` |
| Modify | `proto/todo/v1/todo.proto` |
| Create | `services/todo/internal/server/attachments_multipart.go` |
| Modify | `services/todo/internal/server/attachments.go` |
| Create | `services/gateway/internal/handler/attachments_multipart.go` |
| Modify | `services/gateway/internal/server/server.go` |

---

## Task 1: Extend `pkg/blobstore` with multipart operations

**Files:**
- Modify: `pkg/blobstore/blobstore.go`
- Modify: `pkg/blobstore/blobstore_test.go`

- [ ] **Step 1: Write the failing test** (offline — presigning is local HMAC, no live server)

```go
// Append to pkg/blobstore/blobstore_test.go

import (
    "context"
    // keep existing "strings" and "testing"
)

func TestPresignUploadPart_ContainsExpectedParams(t *testing.T) {
    st := New(Config{
        Endpoint:  "http://localhost:9000",
        Region:    "us-east-1",
        Bucket:    "test",
        AccessKey: "fakekey",
        SecretKey: "fakesecret",
    })
    url, err := st.PresignUploadPart(context.Background(), "eu/u1/t1/a1/file.pdf", "upload-xyz", 2, 15*time.Minute)
    if err != nil {
        t.Fatal(err)
    }
    if !strings.Contains(url, "partNumber=2") {
        t.Fatalf("presigned URL missing partNumber param: %s", url)
    }
    if !strings.Contains(url, "uploadId=upload-xyz") {
        t.Fatalf("presigned URL missing uploadId param: %s", url)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/blobstore/ -run TestPresignUploadPart -v`
Expected: FAIL — `undefined: (*S3Store).PresignUploadPart`

- [ ] **Step 3: Add multipart methods to `blobstore.go`**

Add to the `Store` interface (after `Delete`):

```go
// InitiateMultipart starts an S3 multipart upload and returns the upload ID.
// The upload ID must be stored in the DB; it is passed to PresignUploadPart and
// CompleteMultipart.
InitiateMultipart(ctx context.Context, key, contentType string) (uploadID string, err error)
// PresignUploadPart returns a presigned URL the client can PUT a single part to.
// partNumber is 1-based as required by S3.
PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, ttl time.Duration) (string, error)
// CompleteMultipart finalises a multipart upload. parts must be sorted by
// PartNumber (ascending) as required by S3.
CompleteMultipart(ctx context.Context, key, uploadID string, parts []CompletedPart) error
// AbortMultipart cancels an incomplete multipart upload and frees staged parts.
AbortMultipart(ctx context.Context, key, uploadID string) error
```

Add the `CompletedPart` type (before the `Config` struct):

```go
// CompletedPart carries the part number and ETag returned by the client after
// a successful part PUT, needed to finalise a multipart upload.
type CompletedPart struct {
    PartNumber int32
    ETag       string
}
```

Add the four methods on `*S3Store` (after the existing `Delete` method):

```go
func (s *S3Store) InitiateMultipart(ctx context.Context, key, contentType string) (string, error) {
    out, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
        Bucket:      aws.String(s.bucket),
        Key:         aws.String(key),
        ContentType: aws.String(contentType),
    })
    if err != nil {
        return "", fmt.Errorf("initiate multipart: %w", err)
    }
    return aws.ToString(out.UploadId), nil
}

func (s *S3Store) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, ttl time.Duration) (string, error) {
    req, err := s.presign.PresignUploadPart(ctx, &s3.UploadPartInput{
        Bucket:     aws.String(s.bucket),
        Key:        aws.String(key),
        UploadId:   aws.String(uploadID),
        PartNumber: partNumber,
    }, s3.WithPresignExpires(ttl))
    if err != nil {
        return "", fmt.Errorf("presign upload part: %w", err)
    }
    return req.URL, nil
}

func (s *S3Store) CompleteMultipart(ctx context.Context, key, uploadID string, parts []CompletedPart) error {
    completed := make([]types.CompletedPart, len(parts))
    for i, p := range parts {
        completed[i] = types.CompletedPart{
            PartNumber: p.PartNumber,
            ETag:       aws.String(p.ETag),
        }
    }
    _, err := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
        Bucket:          aws.String(s.bucket),
        Key:             aws.String(key),
        UploadId:        aws.String(uploadID),
        MultipartUpload: &types.CompletedMultipartUpload{Parts: completed},
    })
    if err != nil {
        return fmt.Errorf("complete multipart: %w", err)
    }
    return nil
}

func (s *S3Store) AbortMultipart(ctx context.Context, key, uploadID string) error {
    _, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
        Bucket:   aws.String(s.bucket),
        Key:      aws.String(key),
        UploadId: aws.String(uploadID),
    })
    if err != nil {
        return fmt.Errorf("abort multipart: %w", err)
    }
    return nil
}
```

Add `"github.com/aws/aws-sdk-go-v2/service/s3/types"` to the import block.

- [ ] **Step 4: Run tests — pass + build check**

Run: `go test ./pkg/blobstore/ -v && go build ./pkg/blobstore/`
Expected: PASS + no build errors.

- [ ] **Step 5: Commit**

```bash
git add pkg/blobstore/blobstore.go pkg/blobstore/blobstore_test.go
git commit -m "feat(blobstore): multipart initiate/presign-part/complete/abort"
```

---

## Task 2: Migration `000012_attachment_upload_id`

**Files:**
- Create: `migrations/todo/000012_attachment_upload_id.up.sql`
- Create: `migrations/todo/000012_attachment_upload_id.down.sql`
- Copy: both into `deploy/k8s/base/todo/`
- Modify: `deploy/k8s/base/todo/kustomization.yaml`

- [ ] **Step 1: Write the up migration**

```sql
-- migrations/todo/000012_attachment_upload_id.up.sql
-- Stores the S3 multipart upload_id for in-progress multipart uploads.
-- NULL means single-PUT (the original flow); non-NULL means multipart.
ALTER TABLE todo.attachments
    ADD COLUMN upload_id TEXT;
```

- [ ] **Step 2: Write the down migration**

```sql
-- migrations/todo/000012_attachment_upload_id.down.sql
ALTER TABLE todo.attachments DROP COLUMN IF EXISTS upload_id;
```

- [ ] **Step 3: Mirror into k8s configmap source and register it**

```bash
cp migrations/todo/000012_attachment_upload_id.up.sql \
   migrations/todo/000012_attachment_upload_id.down.sql \
   deploy/k8s/base/todo/
```

In `deploy/k8s/base/todo/kustomization.yaml`, append to the `files:` list under `todo-migrations`:
```yaml
        - 000012_attachment_upload_id.up.sql
        - 000012_attachment_upload_id.down.sql
```

- [ ] **Step 4: Verify migration applies**

Run: `TESTCONTAINERS_RYUK_DISABLED=true go test ./services/todo/internal/store/ -run TestMigrations -timeout 120s -v`
If `TestMigrations` doesn't yet check version 12, hand-verify:
`psql "$DB_URL" -c "\d todo.attachments"` — the `upload_id` column should appear.

- [ ] **Step 5: Commit**

```bash
git add migrations/todo/000012_attachment_upload_id.*.sql deploy/k8s/base/todo/
git commit -m "feat(todo): migration 000012 — add upload_id to attachments"
```

---

## Task 3: Add `UploadID` to `domain.Attachment`

**Files:**
- Modify: `services/todo/internal/domain/attachment.go`

- [ ] **Step 1: Add the field**

Replace the `Attachment` struct in `services/todo/internal/domain/attachment.go` with:

```go
type Attachment struct {
	ID          string
	UserID      string
	TaskID      string
	Filename    string
	ContentType string
	SizeBytes   int64
	ObjectKey   string
	UploadID    string // empty for single-PUT; S3 multipart upload ID otherwise
	ScanStatus  string
	Status      string
	CreatedAt   time.Time
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./services/todo/...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add services/todo/internal/domain/attachment.go
git commit -m "feat(todo): add UploadID field to domain.Attachment"
```

---

## Task 4: Update store — scan `upload_id`, add `CreateMultipartPending`

**Files:**
- Modify: `services/todo/internal/store/attachments.go`
- Modify: `services/todo/internal/store/attachments_test.go`

- [ ] **Step 1: Write the failing integration test**

Append to `services/todo/internal/store/attachments_test.go`:

```go
func TestMultipart_CreateAndGetUploadID(t *testing.T) {
	st := newStoreForAttachments(t) // truncates + returns *store.Store
	ctx := context.Background()

	const (
		userID   = "user_mp"
		taskID   = "task_mp"
		attID    = "att_mp"
		uploadID = "minio-upload-abc123"
	)
	seedTask(t, st, userID, taskID)

	att := &domain.Attachment{
		ID:          attID,
		UserID:      userID,
		TaskID:      taskID,
		Filename:    "big.bin",
		ContentType: "application/octet-stream",
		ObjectKey:   "eu/" + userID + "/" + taskID + "/" + attID + "/big.bin",
		CreatedAt:   time.Now().UTC(),
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
		t.Fatalf("Status = %q, want pending", got.Status)
	}
}
```

*(Uses the existing `newStoreForAttachments(t)` helper which calls `testutil.Truncate` + `store.New(testPool)`. `seedTask(t, st, userID, taskID)` matches the real 4-argument signature in `attachments_test.go`.)*

- [ ] **Step 2: Run — fails**

Run: `TESTCONTAINERS_RYUK_DISABLED=true go test ./services/todo/internal/store/ -run TestMultipart -timeout 120s -v`
Expected: FAIL — `undefined: (*Store).CreateMultipartPending`

- [ ] **Step 3: Update `attachmentCols` and `scanAttachment` in `attachments.go`**

Replace `attachmentCols` with (insert `upload_id` after `object_key`, keeping `status` before `scan_status` to match the original order):

```go
const attachmentCols = `id, user_id, task_id, filename, content_type, size_bytes, object_key, upload_id, status, scan_status, created_at`
```

Replace `scanAttachment` to handle the nullable `upload_id`:

```go
func scanAttachment(row interface {
	Scan(dest ...any) error
}, a *domain.Attachment) error {
	var uploadID *string
	err := row.Scan(
		&a.ID, &a.UserID, &a.TaskID, &a.Filename,
		&a.ContentType, &a.SizeBytes, &a.ObjectKey,
		&uploadID,
		&a.Status, &a.ScanStatus, &a.CreatedAt,
	)
	if uploadID != nil {
		a.UploadID = *uploadID
	}
	return err
}
```

- [ ] **Step 4: Add `CreateMultipartPending`**

Append to `services/todo/internal/store/attachments.go`:

```go
// CreateMultipartPending inserts a pending attachment row with the S3
// multipart upload_id stored for later part-presign and abort operations.
func (s *Store) CreateMultipartPending(ctx context.Context, a *domain.Attachment, uploadID string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO todo.attachments
			(id, user_id, task_id, filename, content_type, size_bytes, object_key, upload_id, scan_status, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		a.ID, a.UserID, a.TaskID, a.Filename,
		a.ContentType, a.SizeBytes, a.ObjectKey,
		uploadID,
		domain.ScanClean, domain.AttachmentPending, a.CreatedAt,
	)
	return err
}
```

Also update `CreateAttachmentPending` (single-PUT) to include `upload_id` in the INSERT so it explicitly sets NULL, keeping the columns in sync with `attachmentCols`:

```go
func (s *Store) CreateAttachmentPending(ctx context.Context, a *domain.Attachment) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO todo.attachments
			(id, user_id, task_id, filename, content_type, size_bytes, object_key, upload_id, scan_status, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULL,$8,$9,$10)`,
		a.ID, a.UserID, a.TaskID, a.Filename,
		a.ContentType, a.SizeBytes, a.ObjectKey,
		domain.ScanClean, domain.AttachmentPending, a.CreatedAt,
	)
	return err
}
```

- [ ] **Step 5: Run — passes**

Run: `TESTCONTAINERS_RYUK_DISABLED=true go test ./services/todo/internal/store/ -timeout 120s -v`
Expected: all store tests PASS.

- [ ] **Step 6: Commit**

```bash
git add services/todo/internal/store/attachments.go services/todo/internal/store/attachments_test.go
git commit -m "feat(todo/store): scan upload_id + CreateMultipartPending"
```

---

## Task 5: Update reaper to abort in-progress multipart uploads

**Files:**
- Modify: `services/todo/internal/reaper/reaper.go`

- [ ] **Step 1: Write the failing test**

Create `services/todo/internal/reaper/reaper_test.go`:

```go
package reaper

import (
	"context"
	"testing"
	"time"

	"github.com/fsociety/todo/services/todo/internal/domain"
)

type fakeBlob struct {
	deleted []string
	aborted []string // "key:uploadID"
}

func (f *fakeBlob) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}

func (f *fakeBlob) AbortMultipart(_ context.Context, key, uploadID string) error {
	f.aborted = append(f.aborted, key+":"+uploadID)
	return nil
}

type fakeStore struct {
	atts []*domain.Attachment
}

func (f *fakeStore) ListAbandonedAttachments(_ context.Context, _ time.Duration, _ int) ([]*domain.Attachment, error) {
	return f.atts, nil
}

func (f *fakeStore) PurgeAttachmentRow(_ context.Context, _ string) error { return nil }

func TestReap_SinglePUT_Delete(t *testing.T) {
	blob := &fakeBlob{}
	st := &fakeStore{atts: []*domain.Attachment{
		{ID: "a1", ObjectKey: "k/a1", UploadID: ""},
	}}
	r := New(st, blob, nil)
	if err := r.reap(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(blob.deleted) != 1 || blob.deleted[0] != "k/a1" {
		t.Fatalf("deleted=%v, want [k/a1]", blob.deleted)
	}
	if len(blob.aborted) != 0 {
		t.Fatalf("unexpected aborts: %v", blob.aborted)
	}
}

func TestReap_Multipart_Abort(t *testing.T) {
	blob := &fakeBlob{}
	st := &fakeStore{atts: []*domain.Attachment{
		{ID: "a2", ObjectKey: "k/a2", UploadID: "uid-123"},
	}}
	r := New(st, blob, nil)
	if err := r.reap(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(blob.aborted) != 1 || blob.aborted[0] != "k/a2:uid-123" {
		t.Fatalf("aborted=%v, want [k/a2:uid-123]", blob.aborted)
	}
	if len(blob.deleted) != 0 {
		t.Fatalf("unexpected deletes: %v", blob.deleted)
	}
}
```

- [ ] **Step 2: Run — fails**

Run: `go test ./services/todo/internal/reaper/ -v`
Expected: FAIL — `(*fakeBlob).AbortMultipart` not in `reaper.Blob` interface.

- [ ] **Step 3: Update `reaper.go`**

Replace the `Blob` interface, `New` constructor (accept `nil` log), and `reap` method:

```go
// Blob is the subset of blobstore.Store the reaper needs.
type Blob interface {
	Delete(ctx context.Context, key string) error
	// AbortMultipart cancels an in-progress multipart upload, freeing its staged
	// parts. Called instead of Delete when UploadID is non-empty.
	AbortMultipart(ctx context.Context, key, uploadID string) error
}

// New returns a Reaper with sensible defaults.
// log may be nil (falls back to slog.Default()).
func New(store Store, blob Blob, log *slog.Logger) *Reaper {
	if log == nil {
		log = slog.Default()
	}
	return &Reaper{
		store:    store,
		blob:     blob,
		ttl:      defaultTTL,
		batch:    defaultBatch,
		interval: defaultInterval,
		log:      log,
	}
}

func (r *Reaper) reap(ctx context.Context) error {
	atts, err := r.store.ListAbandonedAttachments(ctx, r.ttl, r.batch)
	if err != nil {
		return err
	}
	for _, a := range atts {
		if a.UploadID != "" {
			// In-progress multipart: abort releases staged parts in object storage.
			if err := r.blob.AbortMultipart(ctx, a.ObjectKey, a.UploadID); err != nil {
				r.log.Warn("reaper abort multipart", "key", a.ObjectKey, "err", err)
			}
		} else {
			if err := r.blob.Delete(ctx, a.ObjectKey); err != nil {
				r.log.Warn("reaper blob delete", "key", a.ObjectKey, "err", err)
			}
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
```

- [ ] **Step 4: Run — passes + full build**

Run: `go test ./services/todo/internal/reaper/ -v && go build ./services/todo/...`
Expected: all PASS, build succeeds.
*(The build of `services/todo` now requires `*blobstore.S3Store` to implement `AbortMultipart` — added in Task 1, so this should pass.)*

- [ ] **Step 5: Commit**

```bash
git add services/todo/internal/reaper/reaper.go services/todo/internal/reaper/reaper_test.go
git commit -m "feat(todo/reaper): abort in-progress multipart uploads on reap"
```

---

## Task 6: Proto RPCs + codegen

**Files:**
- Modify: `proto/todo/v1/todo.proto`
- Run: `./scripts/proto-gen.sh`

- [ ] **Step 1: Add RPCs to the service**

Inside `service TodoService`, after the existing attachment RPCs, add:

```proto
  // Multipart upload
  rpc InitiateMultipartUpload(InitiateMultipartUploadRequest)   returns (InitiateMultipartUploadResponse);
  rpc GetUploadPartURL(GetUploadPartURLRequest)                 returns (GetUploadPartURLResponse);
  rpc CompleteMultipartUpload(CompleteMultipartUploadRequest)   returns (CompleteMultipartUploadResponse);
```

- [ ] **Step 2: Add messages**

Append to the end of `proto/todo/v1/todo.proto` (after `DeleteAttachmentResponse`):

```proto
// ── Multipart upload ─────────────────────────────────────────────────────────

// CompletedPart pairs a part number with the ETag returned by object storage
// after a successful part PUT. Both fields are required for CompleteMultipartUpload.
message CompletedPart {
  int32  part_number = 1;
  string etag        = 2;
}

message InitiateMultipartUploadRequest {
  string user_id      = 1;
  string task_id      = 2;
  string filename     = 3;
  string content_type = 4;
  int64  size_bytes   = 5;
  string plan         = 6; // "free" | "pro"
}
message InitiateMultipartUploadResponse {
  string attachment_id      = 1;
  int32  part_count         = 2; // suggested part count (size_bytes / part_size, rounded up)
  int64  part_size_bytes    = 3; // suggested part size
  int64  expires_in_seconds = 4; // TTL of each presigned part URL
}

message GetUploadPartURLRequest {
  string user_id       = 1;
  string attachment_id = 2;
  int32  part_number   = 3; // 1-based
}
message GetUploadPartURLResponse {
  string upload_url         = 1;
  int64  expires_in_seconds = 2;
}

message CompleteMultipartUploadRequest {
  string user_id       = 1;
  string attachment_id = 2;
  repeated CompletedPart parts = 3;
}
message CompleteMultipartUploadResponse {
  Attachment attachment = 1;
}
```

- [ ] **Step 3: Regenerate and verify build**

Run: `./scripts/proto-gen.sh && go build ./proto/... ./services/todo/... ./services/gateway/...`
Expected: success. (New RPCs have `Unimplemented` fallbacks from the embedded struct, so the build is not broken before Task 7.)

- [ ] **Step 4: Commit**

```bash
git add proto/todo/v1/todo.proto proto/gen/
git commit -m "feat(proto): multipart initiate/part-url/complete RPCs"
```

---

## Task 7: gRPC handlers + single-PUT threshold cap

**Files:**
- Create: `services/todo/internal/server/attachments_multipart.go`
- Modify: `services/todo/internal/server/attachments.go`

- [ ] **Step 1: Write the failing tests**

Create `services/todo/internal/server/attachments_multipart_test.go`:

```go
package server

import (
	"testing"

	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInitiateMultipart_FreePlanRejected(t *testing.T) {
	s := &Server{}
	_, err := s.InitiateMultipartUpload(t.Context(), &todov1.InitiateMultipartUploadRequest{
		UserId: "u1", TaskId: "t1", Filename: "f.bin",
		SizeBytes: 50 << 20, Plan: "free",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}
}

func TestInitiateMultipart_BelowThresholdRejected(t *testing.T) {
	s := &Server{}
	_, err := s.InitiateMultipartUpload(t.Context(), &todov1.InitiateMultipartUploadRequest{
		UserId: "u1", TaskId: "t1", Filename: "f.bin",
		SizeBytes: 10 << 20, Plan: "pro", // 10 MiB < threshold
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for sub-threshold multipart, got %v", err)
	}
}

func TestCreateUploadURL_ExceedsThreshold_ProRejected(t *testing.T) {
	s := &Server{}
	_, err := s.CreateAttachmentUploadURL(t.Context(), &todov1.CreateAttachmentUploadURLRequest{
		UserId: "u1", TaskId: "t1", Filename: "f.bin",
		SizeBytes: 200 << 20, Plan: "pro", // 200 MiB > threshold
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for oversized single-PUT, got %v", err)
	}
}
```

- [ ] **Step 2: Run — fails**

Run: `go test ./services/todo/internal/server/ -run "TestInitiateMultipart|TestCreateUploadURL_ExceedsThreshold" -v`
Expected: FAIL — methods undefined / wrong behavior.

- [ ] **Step 3: Add `multipartThreshold` cap to `CreateAttachmentUploadURL` in `attachments.go`**

Add the constant at the top of `attachments.go`:

```go
// multipartThreshold is the maximum file size for single-PUT uploads. Pro users
// with files larger than this must use the multipart upload flow instead.
const multipartThreshold int64 = 100 << 20 // 100 MiB
```

In `CreateAttachmentUploadURL`, inside the `else` (pro plan) branch, after the `proMaxFileBytes` check, add:

```go
	if req.SizeBytes > multipartThreshold {
		return nil, status.Errorf(codes.InvalidArgument,
			"file exceeds single-PUT limit of 100 MiB; use the multipart upload flow")
	}
```

- [ ] **Step 4: Implement the three multipart handlers**

Create `services/todo/internal/server/attachments_multipart.go`:

```go
package server

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/fsociety/todo/pkg/blobstore"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	partSize     int64         = 50 << 20        // 50 MiB per part
	partURLTTL   time.Duration = 15 * time.Minute
)

// InitiateMultipartUpload is the entry point for the multipart upload flow. It:
//  1. Validates plan (pro only) and file size (must be above multipartThreshold).
//  2. Calls S3 CreateMultipartUpload to obtain an upload_id.
//  3. Stores a pending attachment row with the upload_id.
//  4. Returns the attachment_id and suggested part layout to the client.
func (s *Server) InitiateMultipartUpload(ctx context.Context, req *todov1.InitiateMultipartUploadRequest) (*todov1.InitiateMultipartUploadResponse, error) {
	if req.UserId == "" || req.TaskId == "" || req.Filename == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id, task_id, and filename are required")
	}
	if req.Plan != "pro" {
		return nil, status.Error(codes.PermissionDenied, "multipart upload is available on pro plan only")
	}
	if req.SizeBytes <= multipartThreshold {
		return nil, status.Errorf(codes.InvalidArgument,
			"file is below multipart threshold (%d bytes); use single-PUT upload instead", multipartThreshold)
	}
	if req.SizeBytes > proMaxFileBytes {
		return nil, status.Errorf(codes.ResourceExhausted, "file exceeds 100 GiB soft cap")
	}

	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	attID := uuid.New().String()
	objectKey := blobstore.Key(s.region, req.UserId, req.TaskId, attID, req.Filename)

	uploadID, err := s.blob.InitiateMultipart(ctx, objectKey, contentType)
	if err != nil {
		s.log.Error("initiate multipart", "err", err)
		return nil, status.Error(codes.Internal, "failed to initiate multipart upload")
	}

	att := &domain.Attachment{
		ID:          attID,
		UserID:      req.UserId,
		TaskID:      req.TaskId,
		Filename:    req.Filename,
		ContentType: contentType,
		SizeBytes:   req.SizeBytes,
		ObjectKey:   objectKey,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.store.CreateMultipartPending(ctx, att, uploadID); err != nil {
		// Best-effort abort so we don't leak a multipart upload in S3.
		_ = s.blob.AbortMultipart(ctx, objectKey, uploadID)
		s.log.Error("create multipart pending", "err", err)
		return nil, status.Error(codes.Internal, "failed to create attachment record")
	}

	partCount := int32(math.Ceil(float64(req.SizeBytes) / float64(partSize)))
	return &todov1.InitiateMultipartUploadResponse{
		AttachmentId:    attID,
		PartCount:       partCount,
		PartSizeBytes:   partSize,
		ExpiresInSeconds: int64(partURLTTL.Seconds()),
	}, nil
}

// GetUploadPartURL returns a fresh presigned PUT URL for one part of a
// multipart upload. The client calls this per part (or re-calls on failure to
// resume). The S3 upload_id never leaves the server.
func (s *Server) GetUploadPartURL(ctx context.Context, req *todov1.GetUploadPartURLRequest) (*todov1.GetUploadPartURLResponse, error) {
	if req.UserId == "" || req.AttachmentId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and attachment_id are required")
	}
	if req.PartNumber < 1 {
		return nil, status.Error(codes.InvalidArgument, "part_number must be >= 1")
	}

	att, err := s.store.GetAttachment(ctx, req.UserId, req.AttachmentId)
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "attachment not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get attachment")
	}
	if att.Status != domain.AttachmentPending {
		return nil, status.Error(codes.FailedPrecondition, "attachment is no longer pending")
	}
	if att.UploadID == "" {
		return nil, status.Error(codes.FailedPrecondition, "attachment is not a multipart upload")
	}

	url, err := s.blob.PresignUploadPart(ctx, att.ObjectKey, att.UploadID, req.PartNumber, partURLTTL)
	if err != nil {
		s.log.Error("presign upload part", "err", err)
		return nil, status.Error(codes.Internal, "failed to generate part upload URL")
	}
	return &todov1.GetUploadPartURLResponse{
		UploadUrl:        url,
		ExpiresInSeconds: int64(partURLTTL.Seconds()),
	}, nil
}

// CompleteMultipartUpload finalises the multipart upload:
//  1. Looks up object key + upload_id from the DB.
//  2. Calls S3 CompleteMultipartUpload with the client-supplied (part_number, etag) list.
//  3. HEAD-verifies the assembled object to get the final size.
//  4. Calls ConfirmAttachment (existing) to flip the row to committed and bump counters.
func (s *Server) CompleteMultipartUpload(ctx context.Context, req *todov1.CompleteMultipartUploadRequest) (*todov1.CompleteMultipartUploadResponse, error) {
	if req.UserId == "" || req.AttachmentId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and attachment_id are required")
	}
	if len(req.Parts) == 0 {
		return nil, status.Error(codes.InvalidArgument, "parts must not be empty")
	}

	att, err := s.store.GetAttachment(ctx, req.UserId, req.AttachmentId)
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "attachment not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get attachment")
	}
	if att.Status == domain.AttachmentCommitted {
		// Idempotent: already confirmed (e.g. duplicate request).
		return &todov1.CompleteMultipartUploadResponse{Attachment: attachmentToProto(att)}, nil
	}
	if att.UploadID == "" {
		return nil, status.Error(codes.FailedPrecondition, "not a multipart upload")
	}

	parts := make([]blobstore.CompletedPart, len(req.Parts))
	for i, p := range req.Parts {
		parts[i] = blobstore.CompletedPart{PartNumber: p.PartNumber, ETag: p.Etag}
	}
	if err := s.blob.CompleteMultipart(ctx, att.ObjectKey, att.UploadID, parts); err != nil {
		s.log.Error("complete multipart", "err", err)
		return nil, status.Error(codes.Internal, "failed to complete multipart upload in storage")
	}

	storedSize, err := s.blob.Head(ctx, att.ObjectKey)
	if err != nil {
		return nil, status.Error(codes.Internal, "HEAD after complete failed — storage may be unavailable")
	}

	confirmed, err := s.store.ConfirmAttachment(ctx, req.UserId, req.AttachmentId, storedSize)
	if errors.Is(err, store.ErrNotFound) {
		// Race: already confirmed by a concurrent request.
		if att, _ = s.store.GetAttachment(ctx, req.UserId, req.AttachmentId); att != nil {
			return &todov1.CompleteMultipartUploadResponse{Attachment: attachmentToProto(att)}, nil
		}
		return nil, status.Error(codes.NotFound, "attachment not found")
	}
	if err != nil {
		s.log.Error("confirm attachment after multipart", "err", err)
		return nil, status.Error(codes.Internal, "failed to confirm attachment")
	}
	return &todov1.CompleteMultipartUploadResponse{Attachment: attachmentToProto(confirmed)}, nil
}
```

- [ ] **Step 5: Run — passes + full build**

Run: `go test ./services/todo/internal/server/ -run "TestInitiateMultipart|TestCreateUploadURL_ExceedsThreshold" -v && go build ./services/todo/...`
Expected: all PASS, build succeeds.

- [ ] **Step 6: Commit**

```bash
git add services/todo/internal/server/attachments_multipart.go \
        services/todo/internal/server/attachments_multipart_test.go \
        services/todo/internal/server/attachments.go
git commit -m "feat(todo): multipart gRPC handlers + single-PUT 100 MiB threshold"
```

---

## Task 8: Gateway HTTP routes + handlers

**Files:**
- Create: `services/gateway/internal/handler/attachments_multipart.go`
- Modify: `services/gateway/internal/server/server.go`

- [ ] **Step 1: Write the failing test**

Create `services/gateway/internal/handler/attachments_multipart_test.go`:

```go
package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInitiateMultipart_BadJSON(t *testing.T) {
	h := NewMultipartAttachmentHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/t1/attachments/multipart/initiate", strings.NewReader("{"))
	rec := httptest.NewRecorder()
	h.Initiate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCompleteMultipart_BadJSON(t *testing.T) {
	h := NewMultipartAttachmentHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/t1/attachments/att1/multipart/complete", strings.NewReader("{"))
	rec := httptest.NewRecorder()
	h.Complete(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
```

- [ ] **Step 2: Run — fails**

Run: `go test ./services/gateway/internal/handler/ -run "TestInitiateMultipart_BadJSON|TestCompleteMultipart_BadJSON" -v`
Expected: FAIL — `undefined: NewMultipartAttachmentHandler`

- [ ] **Step 3: Implement the handlers**

Create `services/gateway/internal/handler/attachments_multipart.go`:

```go
package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
)

// MultipartAttachmentHandler bridges HTTP ↔ todo-service multipart RPCs.
type MultipartAttachmentHandler struct {
	client todov1.TodoServiceClient
}

func NewMultipartAttachmentHandler(c todov1.TodoServiceClient) *MultipartAttachmentHandler {
	return &MultipartAttachmentHandler{client: c}
}

// POST /api/v1/tasks/{taskID}/attachments/multipart/initiate
// Body: {filename, content_type, size_bytes}
func (h *MultipartAttachmentHandler) Initiate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Filename == "" || body.SizeBytes <= 0 {
		jsonError(w, "filename and size_bytes are required", http.StatusBadRequest)
		return
	}
	resp, err := h.client.InitiateMultipartUpload(r.Context(), &todov1.InitiateMultipartUploadRequest{
		UserId:      mw.UserIDFromContext(r.Context()),
		TaskId:      chi.URLParam(r, "taskID"),
		Filename:    body.Filename,
		ContentType: body.ContentType,
		SizeBytes:   body.SizeBytes,
		Plan:        mw.SubscriptionPlanFromContext(r.Context()),
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoStatus(w, resp, http.StatusCreated)
}

// GET /api/v1/tasks/{taskID}/attachments/{attachmentID}/parts/{partNumber}
func (h *MultipartAttachmentHandler) GetPartURL(w http.ResponseWriter, r *http.Request) {
	partStr := chi.URLParam(r, "partNumber")
	partNum, err := strconv.ParseInt(partStr, 10, 32)
	if err != nil || partNum < 1 {
		jsonError(w, "partNumber must be a positive integer", http.StatusBadRequest)
		return
	}
	resp, err := h.client.GetUploadPartURL(r.Context(), &todov1.GetUploadPartURLRequest{
		UserId:       mw.UserIDFromContext(r.Context()),
		AttachmentId: chi.URLParam(r, "attachmentID"),
		PartNumber:   int32(partNum),
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

// POST /api/v1/tasks/{taskID}/attachments/{attachmentID}/multipart/complete
// Body: {parts: [{part_number, etag}, …]}
func (h *MultipartAttachmentHandler) Complete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Parts []struct {
			PartNumber int32  `json:"part_number"`
			ETag       string `json:"etag"`
		} `json:"parts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if len(body.Parts) == 0 {
		jsonError(w, "parts must not be empty", http.StatusBadRequest)
		return
	}
	parts := make([]*todov1.CompletedPart, len(body.Parts))
	for i, p := range body.Parts {
		parts[i] = &todov1.CompletedPart{PartNumber: p.PartNumber, Etag: p.ETag}
	}
	resp, err := h.client.CompleteMultipartUpload(r.Context(), &todov1.CompleteMultipartUploadRequest{
		UserId:       mw.UserIDFromContext(r.Context()),
		AttachmentId: chi.URLParam(r, "attachmentID"),
		Parts:        parts,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.Attachment)
}
```

*(`jsonOK(w, v, code)` and `jsonError`, `handleGRPCError` are the same helpers used in `attachments.go` and `todo.go`. Check `handler/` for the exact signature — they're package-level helpers.)*

- [ ] **Step 4: Mount the routes in `server.go`**

Inside the `r.Route("/api/v1/tasks", ...)` group in `services/gateway/internal/server/server.go`, after constructing `att`, add:

```go
mp := handler.NewMultipartAttachmentHandler(todoClient)
```

Then inside the same `r.Route("/{taskID}/attachments", ...)` sub-router, add the three routes. Place the literal `/multipart/initiate` route **before** the `/{attachmentID}` wildcard group:

```go
r.Post("/multipart/initiate", mp.Initiate)
r.Get("/{attachmentID}/parts/{partNumber}", mp.GetPartURL)
r.Post("/{attachmentID}/multipart/complete", mp.Complete)
```

- [ ] **Step 5: Run — passes + full build**

Run: `go test ./services/gateway/internal/handler/ -run "TestInitiateMultipart_BadJSON|TestCompleteMultipart_BadJSON" -v && go build ./services/gateway/...`
Expected: PASS + build success.

- [ ] **Step 6: Add stub methods to `fakeTodoClient` in gateway tests**

Open `services/gateway/internal/handler/fake_test.go` and add the three new stub methods so existing gateway tests keep compiling:

```go
func (f *fakeTodoClient) InitiateMultipartUpload(_ context.Context, _ *todov1.InitiateMultipartUploadRequest, _ ...grpc.CallOption) (*todov1.InitiateMultipartUploadResponse, error) {
	panic("InitiateMultipartUpload not stubbed")
}
func (f *fakeTodoClient) GetUploadPartURL(_ context.Context, _ *todov1.GetUploadPartURLRequest, _ ...grpc.CallOption) (*todov1.GetUploadPartURLResponse, error) {
	panic("GetUploadPartURL not stubbed")
}
func (f *fakeTodoClient) CompleteMultipartUpload(_ context.Context, _ *todov1.CompleteMultipartUploadRequest, _ ...grpc.CallOption) (*todov1.CompleteMultipartUploadResponse, error) {
	panic("CompleteMultipartUpload not stubbed")
}
```

- [ ] **Step 7: Run full gateway test suite**

Run: `go test ./services/gateway/... -v -timeout 60s 2>&1 | tail -30`
Expected: all PASS.

- [ ] **Step 8: Commit**

```bash
git add services/gateway/internal/handler/attachments_multipart.go \
        services/gateway/internal/handler/attachments_multipart_test.go \
        services/gateway/internal/handler/fake_test.go \
        services/gateway/internal/server/server.go
git commit -m "feat(gateway): multipart initiate/part-url/complete HTTP routes"
```

---

## Done criteria

- Pro users can upload files > 100 MiB using the three-step multipart flow (initiate → per-part presigned URLs → complete); bytes never pass through the gateway or todo-service.
- Single-PUT returns `InvalidArgument` for pro files > 100 MiB.
- Free plan returns `PermissionDenied` for multipart initiate.
- Abandoned in-progress multipart uploads are aborted (not just deleted) by the reaper, verified by the `TestReap_Multipart_Abort` unit test.
- All existing tests continue to pass.

## Follow-on plans (create next, in order)
1. **Malware scan pipeline** — `file.uploaded` consumer + ClamAV + download gate on `scan_status` (Plan 3).
2. **Frontend** — upload UI with multipart progress, i18n (EN + ZH), `TaskCard` attachment indicator (Plan 4).
