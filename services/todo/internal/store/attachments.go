package store

import (
	"context"
	"encoding/json"
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

// CreateAttachmentPending inserts an attachment row with status="pending".
// No counters are bumped yet — that happens in ConfirmAttachment.
func (s *Store) CreateAttachmentPending(ctx context.Context, a *domain.Attachment) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO todo.attachments
			(id, user_id, task_id, filename, content_type, size_bytes, object_key, upload_id, status, scan_status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULL,$8,$9,$10)`,
		a.ID, a.UserID, a.TaskID, a.Filename,
		a.ContentType, a.SizeBytes, a.ObjectKey,
		domain.AttachmentPending, domain.ScanClean, a.CreatedAt,
	)
	return err
}

// CreateMultipartPending inserts an attachment row for a multipart upload with
// the S3 upload ID stored so the reaper can abort incomplete uploads.
func (s *Store) CreateMultipartPending(ctx context.Context, att *domain.Attachment, uploadID string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO todo.attachments
			(id, user_id, task_id, filename, content_type, size_bytes, object_key, upload_id, status, scan_status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		att.ID, att.UserID, att.TaskID, att.Filename,
		att.ContentType, att.SizeBytes, att.ObjectKey, uploadID,
		domain.AttachmentPending, domain.ScanClean, att.CreatedAt,
	)
	return err
}

// UserStorageUsed returns the total committed bytes for a user from the
// denormalized user_storage rollup. Returns 0 if no row exists yet.
func (s *Store) UserStorageUsed(ctx context.Context, userID string) (int64, error) {
	var used int64
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(used_bytes, 0) FROM todo.user_storage WHERE user_id = $1`,
		userID,
	).Scan(&used)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("user storage used: %w", err)
	}
	return used, nil
}

// TaskCommittedAttachmentCount returns the number of committed attachments on a task.
func (s *Store) TaskCommittedAttachmentCount(ctx context.Context, taskID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT attachment_count FROM todo.tasks WHERE id = $1`,
		taskID,
	).Scan(&n)
	if err == pgx.ErrNoRows {
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
func (s *Store) ConfirmAttachment(ctx context.Context, userID, attID string, sizeBytes int64) (*domain.Attachment, error) {
	var att domain.Attachment
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		err := scanAttachment(tx.QueryRow(ctx, `
			UPDATE todo.attachments
			SET status = $1
			WHERE id = $2 AND user_id = $3 AND status = $4
			RETURNING `+attachmentCols,
			domain.AttachmentCommitted, attID, userID, domain.AttachmentPending,
		), &att)
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			UPDATE todo.tasks
			SET attachment_count = attachment_count + 1,
			    attachment_bytes = attachment_bytes + $1,
			    updated_at = now()
			WHERE id = $2`,
			sizeBytes, att.TaskID,
		); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO todo.user_storage (user_id, used_bytes)
			VALUES ($1, $2)
			ON CONFLICT (user_id) DO UPDATE SET used_bytes = todo.user_storage.used_bytes + EXCLUDED.used_bytes`,
			userID, sizeBytes,
		); err != nil {
			return err
		}

		return insertFileUploadedOutbox(ctx, tx, &att)
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
		if c, err := decodeAttachmentCursor(cursor); err == nil {
			curCreatedAt = c.CreatedAt
			curID = c.ID
			hasCursor = true
		}
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
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get attachment: %w", err)
	}
	return &a, nil
}

// DeleteAttachment removes a committed attachment atomically:
//   - deletes the row
//   - decrements attachment_count and attachment_bytes on the task row
//   - decrements user_storage used_bytes (floors at 0)
func (s *Store) DeleteAttachment(ctx context.Context, userID, attID string) (*domain.Attachment, error) {
	var att domain.Attachment
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		err := scanAttachment(tx.QueryRow(ctx, `
			DELETE FROM todo.attachments
			WHERE id = $1 AND user_id = $2
			RETURNING `+attachmentCols,
			attID, userID,
		), &att)
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		// Only roll back counters for committed attachments.
		if att.Status == domain.AttachmentCommitted {
			if _, err := tx.Exec(ctx, `
				UPDATE todo.tasks
				SET attachment_count = GREATEST(attachment_count - 1, 0),
				    attachment_bytes = GREATEST(attachment_bytes - $1, 0),
				    updated_at = now()
				WHERE id = $2`,
				att.SizeBytes, att.TaskID,
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

// ListAbandonedAttachments returns pending attachment rows older than ttl,
// used by the reaper to clean up uploads that were never confirmed.
func (s *Store) ListAbandonedAttachments(ctx context.Context, ttl time.Duration, limit int) ([]*domain.Attachment, error) {
	cutoff := time.Now().UTC().Add(-ttl)
	rows, err := s.db.Query(ctx, `
		SELECT `+attachmentCols+`
		FROM todo.attachments
		WHERE status = $1 AND created_at < $2
		ORDER BY created_at
		LIMIT $3`,
		domain.AttachmentPending, cutoff, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list abandoned: %w", err)
	}
	defer rows.Close()

	var atts []*domain.Attachment
	for rows.Next() {
		var a domain.Attachment
		if err := scanAttachment(rows, &a); err != nil {
			return nil, err
		}
		atts = append(atts, &a)
	}
	return atts, rows.Err()
}

// PurgeAttachmentRow hard-deletes an attachment row without touching counters.
// Used by the reaper after the blob has already been deleted from object storage.
func (s *Store) PurgeAttachmentRow(ctx context.Context, attID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM todo.attachments WHERE id = $1`, attID)
	return err
}

// ── outbox helper ─────────────────────────────────────────────────────────────

func insertFileUploadedOutbox(ctx context.Context, tx pgx.Tx, a *domain.Attachment) error {
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
	return string(b)
}

func decodeAttachmentCursor(s string) (attachmentCursor, error) {
	var c attachmentCursor
	return c, json.Unmarshal([]byte(s), &c)
}
