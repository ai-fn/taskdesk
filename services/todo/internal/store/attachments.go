package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fsociety/todo/pkg/events"
	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const attachmentCols = `id, user_id, task_id, filename, content_type, size_bytes, object_key, upload_id, status, scan_status, created_at`

func scanAttachment(row interface {
	Scan(dest ...any) error
}, a *domain.Attachment) error {
	var uploadID *string
	err := row.Scan(
		&a.ID, &a.UserID, &a.TaskID, &a.Filename,
		&a.ContentType, &a.SizeBytes, &a.ObjectKey, &uploadID,
		&a.Status, &a.ScanStatus, &a.CreatedAt,
	)
	if err != nil {
		return err
	}
	if uploadID != nil {
		a.UploadID = *uploadID
	}
	return nil
}

var (
	ErrQuotaExceeded       = errors.New("storage quota exceeded")
	ErrTaskAttachmentFull  = errors.New("task attachment limit reached")
	ErrSizeExceedsDeclared = errors.New("stored object is larger than the declared size")
	ErrInvalidCursor       = errors.New("invalid cursor")
)

// ReserveLimits are enforced inside ReserveAttachment; a zero field means unlimited.
type ReserveLimits struct {
	MaxFilesPerTask int
	QuotaBytes      int64
}

// lockUserStorage takes the per-user row lock that serialises quota-affecting writes (reserve,
// confirm, delete, purge). Every such tx takes it first, so lock order is user_storage → attachment → task.
func lockUserStorage(ctx context.Context, tx pgx.Tx, userID string) (used int64, err error) {
	err = tx.QueryRow(ctx, `
		INSERT INTO todo.user_storage (user_id, used_bytes) VALUES ($1, 0)
		ON CONFLICT (user_id) DO UPDATE SET used_bytes = todo.user_storage.used_bytes
		RETURNING used_bytes`, userID,
	).Scan(&used)
	if err != nil {
		return 0, fmt.Errorf("lock user storage: %w", err)
	}
	return used, nil
}

// ReserveAttachment checks ownership and limits, then inserts the pending row. The per-user
// user_storage row lock serialises concurrent reservations, and pending rows count toward both limits.
// uploadID is empty for a single-PUT upload.
func (s *Store) ReserveAttachment(ctx context.Context, a *domain.Attachment, uploadID string, lim ReserveLimits) error {
	return withTx(ctx, s.db, func(tx pgx.Tx) error {
		used, err := lockUserStorage(ctx, tx, a.UserID)
		if err != nil {
			return err
		}

		var committed int
		err = tx.QueryRow(ctx,
			`SELECT attachment_count FROM todo.tasks WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
			a.TaskID, a.UserID,
		).Scan(&committed)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("check task: %w", err)
		}

		if lim.MaxFilesPerTask > 0 {
			var pending int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM todo.attachments WHERE user_id = $1 AND task_id = $2 AND status = 'pending'`,
				a.UserID, a.TaskID,
			).Scan(&pending); err != nil {
				return fmt.Errorf("count pending attachments: %w", err)
			}
			if committed+pending >= lim.MaxFilesPerTask {
				return ErrTaskAttachmentFull
			}
		}
		if lim.QuotaBytes > 0 {
			var pendingBytes int64
			if err := tx.QueryRow(ctx,
				`SELECT COALESCE(sum(size_bytes), 0) FROM todo.attachments WHERE user_id = $1 AND status = 'pending'`,
				a.UserID,
			).Scan(&pendingBytes); err != nil {
				return fmt.Errorf("sum pending bytes: %w", err)
			}
			if used+pendingBytes+a.SizeBytes > lim.QuotaBytes {
				return ErrQuotaExceeded
			}
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO todo.attachments
				(id, user_id, task_id, filename, content_type, size_bytes, object_key, upload_id, status, scan_status, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10,$11)`,
			a.ID, a.UserID, a.TaskID, a.Filename,
			a.ContentType, a.SizeBytes, a.ObjectKey, uploadID,
			domain.AttachmentPending, domain.ScanClean, a.CreatedAt,
		)
		return err
	})
}

// TouchAttachment stamps activity on a pending upload so the reaper does not collect a slow multipart.
// It returns ErrNotFound when the upload is no longer pending (committed, deleted or reaped).
func (s *Store) TouchAttachment(ctx context.Context, userID, attID string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE todo.attachments SET last_activity_at = now() WHERE id = $1 AND user_id = $2 AND status = 'pending'`,
		attID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UserStorageUsed returns the user's committed bytes from the user_storage rollup (0 if none yet).
func (s *Store) UserStorageUsed(ctx context.Context, userID string) (int64, error) {
	var used int64
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(used_bytes, 0) FROM todo.user_storage WHERE user_id = $1`,
		userID,
	).Scan(&used)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("user storage used: %w", err)
	}
	return used, nil
}

// TaskCommittedAttachmentCount returns the number of committed attachments on one of the user's tasks.
func (s *Store) TaskCommittedAttachmentCount(ctx context.Context, userID, taskID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT attachment_count FROM todo.tasks WHERE id = $1 AND user_id = $2`,
		taskID, userID,
	).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("attachment count: %w", err)
	}
	return n, nil
}

// ConfirmAttachment transitions an attachment from pending → committed atomically:
//   - updates status on the attachment row
//   - bumps attachment_count and attachment_bytes on the task row
//   - upserts user_storage used_bytes
//   - writes a file.uploaded outbox event in the same transaction
//
// sizeBytes is the stored object's size; it may not exceed the size reserved (and limit-checked)
// at upload start, otherwise ErrSizeExceedsDeclared.
func (s *Store) ConfirmAttachment(ctx context.Context, userID, attID string, sizeBytes int64, language string) (*domain.Attachment, error) {
	var att domain.Attachment
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := lockUserStorage(ctx, tx, userID); err != nil {
			return err
		}
		err := scanAttachment(tx.QueryRow(ctx, `
			SELECT `+attachmentCols+` FROM todo.attachments
			WHERE id = $1 AND user_id = $2 AND status = $3
			FOR UPDATE`,
			attID, userID, domain.AttachmentPending,
		), &att)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if sizeBytes > att.SizeBytes {
			return ErrSizeExceedsDeclared
		}

		att.Status, att.ScanStatus, att.SizeBytes = domain.AttachmentCommitted, domain.ScanPending, sizeBytes
		if _, err := tx.Exec(ctx, `
			UPDATE todo.attachments SET status = $1, scan_status = $2, size_bytes = $3 WHERE id = $4`,
			att.Status, att.ScanStatus, att.SizeBytes, att.ID,
		); err != nil {
			return err
		}

		tag, err := tx.Exec(ctx, `
			UPDATE todo.tasks
			SET attachment_count = attachment_count + 1,
			    attachment_bytes = attachment_bytes + $1,
			    updated_at = now()
			WHERE id = $2 AND user_id = $3`,
			sizeBytes, att.TaskID, userID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO todo.user_storage (user_id, used_bytes)
			VALUES ($1, $2)
			ON CONFLICT (user_id) DO UPDATE SET used_bytes = todo.user_storage.used_bytes + EXCLUDED.used_bytes`,
			userID, sizeBytes,
		); err != nil {
			return err
		}

		return insertFileUploadedOutbox(ctx, tx, &att, language)
	})
	if err != nil {
		return nil, err
	}
	return &att, nil
}

// ListAttachments returns committed attachments for a task using keyset pagination.
// Cursor encodes (created_at, id) of the last seen row.
func (s *Store) ListAttachments(ctx context.Context, userID, taskID string, limit int, cursor string) ([]*domain.Attachment, string, error) {
	var (
		curCreatedAt time.Time
		curID        string
		hasCursor    bool
	)
	if cursor != "" {
		c, err := decodeAttachmentCursor(cursor)
		if err != nil {
			return nil, "", ErrInvalidCursor
		}
		curCreatedAt, curID, hasCursor = c.CreatedAt, c.ID, true
	}

	var rows pgx.Rows
	var err error

	if hasCursor {
		rows, err = s.db.Query(ctx, `
			SELECT `+attachmentCols+`
			FROM todo.attachments
			WHERE user_id = $1 AND task_id = $2 AND status = $3
			  AND (created_at, id) < ($4, $5)
			ORDER BY created_at DESC, id DESC
			LIMIT $6`,
			userID, taskID, domain.AttachmentCommitted,
			curCreatedAt, curID, limit+1,
		)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT `+attachmentCols+`
			FROM todo.attachments
			WHERE user_id = $1 AND task_id = $2 AND status = $3
			ORDER BY created_at DESC, id DESC
			LIMIT $4`,
			userID, taskID, domain.AttachmentCommitted, limit+1,
		)
	}
	if err != nil {
		return nil, "", fmt.Errorf("list attachments: %w", err)
	}
	defer rows.Close()

	var atts []*domain.Attachment
	for rows.Next() {
		var a domain.Attachment
		if err := scanAttachment(rows, &a); err != nil {
			return nil, "", err
		}
		atts = append(atts, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(atts) > limit {
		last := atts[limit-1]
		atts = atts[:limit]
		nextCursor = encodeAttachmentCursor(last.CreatedAt, last.ID)
	}
	return atts, nextCursor, nil
}

// GetAttachment returns a single attachment owned by userID.
func (s *Store) GetAttachment(ctx context.Context, userID, attID string) (*domain.Attachment, error) {
	var a domain.Attachment
	err := scanAttachment(s.db.QueryRow(ctx, `
		SELECT `+attachmentCols+`
		FROM todo.attachments
		WHERE id = $1 AND user_id = $2`,
		attID, userID,
	), &a)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get attachment: %w", err)
	}
	return &a, nil
}

// DeleteAttachment removes an attachment atomically:
//   - deletes the row and queues its blob for removal
//   - for a committed one, decrements the task counters and user_storage (floors at 0)
func (s *Store) DeleteAttachment(ctx context.Context, userID, attID string) (*domain.Attachment, error) {
	var att domain.Attachment
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := lockUserStorage(ctx, tx, userID); err != nil {
			return err
		}
		err := scanAttachment(tx.QueryRow(ctx, `
			DELETE FROM todo.attachments
			WHERE id = $1 AND user_id = $2
			RETURNING `+attachmentCols,
			attID, userID,
		), &att)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := enqueueBlobGC(ctx, tx, &att); err != nil {
			return err
		}

		if att.Status == domain.AttachmentCommitted {
			if _, err := tx.Exec(ctx, `
				UPDATE todo.tasks
				SET attachment_count = GREATEST(attachment_count - 1, 0),
				    attachment_bytes = GREATEST(attachment_bytes - $1, 0),
				    updated_at = now()
				WHERE id = $2 AND user_id = $3`,
				att.SizeBytes, att.TaskID, userID,
			); err != nil {
				return err
			}

			if _, err := tx.Exec(ctx, `
				UPDATE todo.user_storage
				SET used_bytes = GREATEST(used_bytes - $1, 0)
				WHERE user_id = $2`,
				att.SizeBytes, userID,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &att, nil
}

// PurgeTask hard-deletes a soft-deleted task. Its attachments cascade away, so their blobs are queued
// for removal and their committed bytes are released from user_storage in the same tx.
func (s *Store) PurgeTask(ctx context.Context, userID, taskID string) error {
	return withTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := lockUserStorage(ctx, tx, userID); err != nil {
			return err
		}
		var found string
		err := tx.QueryRow(ctx,
			`SELECT id FROM todo.tasks WHERE id = $1 AND user_id = $2 AND deleted_at IS NOT NULL FOR UPDATE`,
			taskID, userID).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("purge task: %w", err)
		}

		// Subtasks cascade too, so release every attachment under the subtree.
		if _, err := tx.Exec(ctx, `
			WITH RECURSIVE tree AS (
				SELECT id FROM todo.tasks WHERE id = $1
				UNION ALL
				SELECT t.id FROM todo.tasks t JOIN tree ON t.parent_task_id = tree.id)
			INSERT INTO todo.blob_gc (object_key, upload_id)
			SELECT a.object_key, a.upload_id FROM todo.attachments a JOIN tree ON a.task_id = tree.id`,
			taskID); err != nil {
			return fmt.Errorf("purge task: queue blobs: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			WITH RECURSIVE tree AS (
				SELECT id FROM todo.tasks WHERE id = $1
				UNION ALL
				SELECT t.id FROM todo.tasks t JOIN tree ON t.parent_task_id = tree.id)
			UPDATE todo.user_storage u
			SET used_bytes = GREATEST(u.used_bytes - COALESCE(
				(SELECT sum(a.size_bytes) FROM todo.attachments a JOIN tree ON a.task_id = tree.id
				 WHERE a.user_id = $2 AND a.status = $3), 0), 0)
			WHERE u.user_id = $2`,
			taskID, userID, domain.AttachmentCommitted); err != nil {
			return fmt.Errorf("purge task: release storage: %w", err)
		}

		if _, err := tx.Exec(ctx, `DELETE FROM todo.tasks WHERE id = $1`, taskID); err != nil {
			return fmt.Errorf("purge task: %w", err)
		}
		return nil
	})
}

// ReapAbandoned deletes up to limit pending uploads with no activity for ttl (singlePutTTL for a
// single PUT, multipartTTL for a multipart one) and queues their blobs. Rows a concurrent confirm
// holds are skipped, and rows already committed no longer match, so a live upload is never reaped.
func (s *Store) ReapAbandoned(ctx context.Context, singlePutTTL, multipartTTL time.Duration, limit int) (int, error) {
	var n int
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			DELETE FROM todo.attachments WHERE id IN (
				SELECT id FROM todo.attachments
				WHERE status = 'pending'
				  AND COALESCE(last_activity_at, created_at) <
				      now() - make_interval(secs => CASE WHEN upload_id IS NULL THEN $1::float8 ELSE $2::float8 END)
				ORDER BY COALESCE(last_activity_at, created_at)
				LIMIT $3
				FOR UPDATE SKIP LOCKED)
			RETURNING `+attachmentCols,
			singlePutTTL.Seconds(), multipartTTL.Seconds(), limit)
		if err != nil {
			return fmt.Errorf("reap abandoned: %w", err)
		}
		var reaped []*domain.Attachment
		for rows.Next() {
			var a domain.Attachment
			if err := scanAttachment(rows, &a); err != nil {
				rows.Close()
				return err
			}
			reaped = append(reaped, &a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, a := range reaped {
			if err := enqueueBlobGC(ctx, tx, a); err != nil {
				return err
			}
		}
		n = len(reaped)
		return nil
	})
	return n, err
}

// BlobGCEntry is a blob queued for removal; a non-empty UploadID means abort that multipart upload too.
type BlobGCEntry struct {
	ID        int64
	ObjectKey string
	UploadID  string
}

func enqueueBlobGC(ctx context.Context, tx pgx.Tx, a *domain.Attachment) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO todo.blob_gc (object_key, upload_id) VALUES ($1, NULLIF($2, ''))`,
		a.ObjectKey, a.UploadID)
	return err
}

// ClaimBlobGC leases up to n queued blobs for lease; an entry whose lease lapses is claimable again.
func (s *Store) ClaimBlobGC(ctx context.Context, n int, lease time.Duration) ([]BlobGCEntry, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE todo.blob_gc SET claimed_until = now() + make_interval(secs => $2)
		WHERE id IN (
			SELECT id FROM todo.blob_gc
			WHERE claimed_until IS NULL OR claimed_until < now()
			ORDER BY id LIMIT $1
			FOR UPDATE SKIP LOCKED)
		RETURNING id, object_key, COALESCE(upload_id, '')`, n, lease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("claim blob gc: %w", err)
	}
	defer rows.Close()
	var out []BlobGCEntry
	for rows.Next() {
		var e BlobGCEntry
		if err := rows.Scan(&e.ID, &e.ObjectKey, &e.UploadID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DoneBlobGC removes entries whose blob is gone.
func (s *Store) DoneBlobGC(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `DELETE FROM todo.blob_gc WHERE id = ANY($1)`, ids)
	return err
}

// UpdateScanStatus sets the scan_status field on an attachment row.
// Returns ErrNotFound when no row with the given ID exists. The value is
// validated against the known scan states so an unexpected string can never
// reach the column and silently bypass the download gate.
func (s *Store) UpdateScanStatus(ctx context.Context, attID, scanStatus string) error {
	switch scanStatus {
	case domain.ScanClean, domain.ScanPending, domain.ScanQuarantined:
	default:
		return fmt.Errorf("update scan status: invalid scan_status %q", scanStatus)
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE todo.attachments SET scan_status = $1 WHERE id = $2`,
		scanStatus, attID,
	)
	if err != nil {
		return fmt.Errorf("update scan status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── outbox helper ─────────────────────────────────────────────────────────────

func insertFileUploadedOutbox(ctx context.Context, tx pgx.Tx, a *domain.Attachment, language string) error {
	ev := events.FileEvent{
		EventID:      uuid.New().String(),
		EventType:    events.TypeFileUploaded,
		UserID:       a.UserID,
		TaskID:       a.TaskID,
		AttachmentID: a.ID,
		Filename:     a.Filename,
		ContentType:  a.ContentType,
		SizeBytes:    a.SizeBytes,
		ObjectKey:    a.ObjectKey,
		Language:     language,
		OccurredAt:   time.Now().UTC(),
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal file event: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO todo.outbox (event_type, payload) VALUES ($1, $2)`,
		events.TypeFileUploaded, payload,
	)
	return err
}

// ── cursor encoding ───────────────────────────────────────────────────────────

type attachmentCursor struct {
	CreatedAt time.Time `json:"c"`
	ID        string    `json:"i"`
}

func encodeAttachmentCursor(createdAt time.Time, id string) string {
	b, _ := json.Marshal(attachmentCursor{CreatedAt: createdAt, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeAttachmentCursor(s string) (attachmentCursor, error) {
	var c attachmentCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.ID == "" {
		return c, ErrInvalidCursor
	}
	return c, nil
}
