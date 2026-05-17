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
	"github.com/jackc/pgx/v5/pgxpool"
)

// OutboxEntry is a row from todo.outbox that has not yet been published.
type OutboxEntry struct {
	ID        string
	EventType string
	Payload   json.RawMessage
}

// CreateTaskWithEvent inserts the task and an outbox event in one transaction.
func (s *Store) CreateTaskWithEvent(ctx context.Context, t *domain.Task) error {
	return withTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO todo.tasks
				(id, user_id, title, description, priority, status, tags,
				 due_at, created_at, updated_at, list_id, flagged)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			t.ID, t.UserID, t.Title, t.Description,
			int32(t.Priority), int32(t.Status), t.Tags,
			t.DueAt, t.CreatedAt, t.UpdatedAt,
			t.ListID, t.Flagged,
		); err != nil {
			return err
		}
		return insertOutbox(ctx, tx, events.TypeTaskCreated, t.UserID, t.ID, t.Title)
	})
}

// UpdateTaskWithEvent updates the task and writes an outbox event atomically.
func (s *Store) UpdateTaskWithEvent(ctx context.Context, t *domain.Task, eventType string) (*domain.Task, error) {
	var updated domain.Task
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
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
			return err
		}
		return insertOutbox(ctx, tx, eventType, updated.UserID, updated.ID, updated.Title)
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
		return insertOutbox(ctx, tx, events.TypeTaskDeleted, userID, taskID, title)
	})
}

// UnpublishedBatch returns up to n outbox rows that haven't been published yet.
func (s *Store) UnpublishedBatch(ctx context.Context, n int) ([]OutboxEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, event_type, payload
		FROM todo.outbox
		WHERE published_at IS NULL
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, n)
	if err != nil {
		return nil, fmt.Errorf("outbox fetch: %w", err)
	}
	defer rows.Close()

	var entries []OutboxEntry
	for rows.Next() {
		var e OutboxEntry
		if err := rows.Scan(&e.ID, &e.EventType, &e.Payload); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// MarkPublished stamps published_at on the given outbox IDs.
func (s *Store) MarkPublished(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx,
		`UPDATE todo.outbox SET published_at=$1 WHERE id = ANY($2::uuid[])`,
		time.Now().UTC(), ids,
	)
	return err
}

// ── helpers ───────────────────────────────────────────────────────────────────

func insertOutbox(ctx context.Context, tx pgx.Tx, eventType, userID, taskID, title string) error {
	ev := events.TaskEvent{
		EventID:    uuid.New().String(),
		EventType:  eventType,
		UserID:     userID,
		TaskID:     taskID,
		TaskTitle:  title,
		OccurredAt: time.Now().UTC(),
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
