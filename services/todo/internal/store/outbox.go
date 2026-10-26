package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/fsociety/todo/pkg/events"
	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OutboxEntry is a row claimed from todo.outbox for publishing.
type OutboxEntry struct {
	ID        string
	EventType string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// CreateTaskWithEvent inserts the task and an outbox event in one transaction.
func (s *Store) CreateTaskWithEvent(ctx context.Context, t *domain.Task) error {
	tags := t.Tags
	if tags == nil {
		tags = []string{}
	}
	return withTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := requireTaskRefs(ctx, tx, t.UserID, t.ListID, t.ParentTaskID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO todo.tasks
				(id, user_id, title, description, priority, status, tags,
				 due_at, created_at, updated_at, list_id, flagged, parent_task_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			t.ID, t.UserID, t.Title, t.Description,
			int32(t.Priority), int32(t.Status), tags,
			t.DueAt, t.CreatedAt, t.UpdatedAt,
			t.ListID, t.Flagged, t.ParentTaskID,
		); err != nil {
			return mapRefErr(err)
		}
		// Adding a subtask to a completed parent reopens it.
		if t.ParentTaskID != nil {
			if _, err := tx.Exec(ctx,
				`UPDATE todo.tasks SET status = 1, updated_at = now()
				 WHERE id = $1 AND user_id = $2 AND status = 3 AND deleted_at IS NULL`,
				*t.ParentTaskID, t.UserID,
			); err != nil {
				return err
			}
		}
		return insertOutbox(ctx, tx, events.TypeTaskCreated, t.UserID, t.ID, t.Title, Notify{})
	})
}

// Notify is the recipient data stamped on an outbox event; the zero value is fine
// for events nobody is emailed about.
type Notify struct {
	Language string
	Email    string // verified address only; empty = no email
}

// UpdateTaskWithEvent updates the task and writes an outbox event atomically.
func (s *Store) UpdateTaskWithEvent(ctx context.Context, t *domain.Task, eventType string, n Notify) (*domain.Task, error) {
	var updated domain.Task
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := requireListOrUnchanged(ctx, tx, t.UserID, t.ID, t.ListID); err != nil {
			return err
		}
		err := scanTask(tx.QueryRow(ctx, `
			UPDATE todo.tasks
			SET title=$3, description=$4, priority=$5, status=$6,
			    tags=$7, due_at=$8, updated_at=$9, list_id=$10, flagged=$11
			WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL
			RETURNING `+taskCols,
			t.ID, t.UserID, t.Title, t.Description,
			int32(t.Priority), int32(t.Status), t.Tags,
			t.DueAt, t.UpdatedAt, t.ListID, t.Flagged,
		), &updated)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrNotFound
			}
			return mapRefErr(err)
		}
		return insertOutbox(ctx, tx, eventType, updated.UserID, updated.ID, updated.Title, n)
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// DeleteTaskWithEvent soft-deletes the task and writes an outbox event atomically.
func (s *Store) DeleteTaskWithEvent(ctx context.Context, userID, taskID string) error {
	return withTx(ctx, s.db, func(tx pgx.Tx) error {
		var title string
		if err := tx.QueryRow(ctx, `
			UPDATE todo.tasks
			SET deleted_at = now(), updated_at = now()
			WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL
			RETURNING title`, taskID, userID,
		).Scan(&title); err != nil {
			if err == pgx.ErrNoRows {
				return ErrNotFound
			}
			return fmt.Errorf("delete task: %w", err)
		}
		return insertOutbox(ctx, tx, events.TypeTaskDeleted, userID, taskID, title, Notify{})
	})
}

// ClaimOutbox leases up to n pending rows for lease, oldest first. Claiming is one atomic
// UPDATE, so concurrent publishers never get the same row; an unpublished row becomes
// claimable again once its lease expires.
func (s *Store) ClaimOutbox(ctx context.Context, n int, lease time.Duration) ([]OutboxEntry, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE todo.outbox
		SET claimed_until = now() + make_interval(secs => $2)
		WHERE id IN (
			SELECT id FROM todo.outbox
			WHERE published_at IS NULL AND failed_at IS NULL
			  AND (claimed_until IS NULL OR claimed_until < now())
			ORDER BY created_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED)
		RETURNING id, event_type, payload, created_at`, n, lease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("outbox claim: %w", err)
	}
	defer rows.Close()

	var entries []OutboxEntry
	for rows.Next() {
		var e OutboxEntry
		if err := rows.Scan(&e.ID, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].CreatedAt.Before(entries[j].CreatedAt) }) // RETURNING is unordered
	return entries, nil
}

// MarkPublished stamps published_at on the given outbox IDs.
func (s *Store) MarkPublished(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx,
		`UPDATE todo.outbox SET published_at=$1, claimed_until=NULL WHERE id = ANY($2::uuid[])`,
		time.Now().UTC(), ids,
	)
	return err
}

// ReleaseOutbox ends the lease on rows that were claimed but not sent, so another publisher can take them.
func (s *Store) ReleaseOutbox(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `UPDATE todo.outbox SET claimed_until=NULL WHERE id = ANY($1::uuid[]) AND published_at IS NULL`, ids)
	return err
}

// MarkOutboxFailed parks a row that can never be published so it stops being claimed.
func (s *Store) MarkOutboxFailed(ctx context.Context, id, reason string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE todo.outbox SET failed_at=now(), last_error=$2, claimed_until=NULL WHERE id=$1::uuid`,
		id, reason,
	)
	return err
}

// PruneOutbox deletes up to limit rows published before now-retention and returns how many went.
func (s *Store) PruneOutbox(ctx context.Context, retention time.Duration, limit int) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM todo.outbox WHERE id IN (
			SELECT id FROM todo.outbox
			WHERE published_at IS NOT NULL AND published_at < now() - make_interval(secs => $1)
			LIMIT $2
			FOR UPDATE SKIP LOCKED)`, retention.Seconds(), limit)
	if err != nil {
		return 0, fmt.Errorf("outbox prune: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func insertOutbox(ctx context.Context, tx pgx.Tx, eventType, userID, taskID, title string, n Notify) error {
	ev := events.TaskEvent{
		EventID:    uuid.New().String(),
		EventType:  eventType,
		UserID:     userID,
		TaskID:     taskID,
		TaskTitle:  title,
		OccurredAt: time.Now().UTC(),
		Language:   n.Language,
		Email:      n.Email,
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal outbox event: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO todo.outbox (event_type, payload) VALUES ($1, $2)`,
		eventType, payload,
	)
	return err
}

func withTx(ctx context.Context, db *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}
