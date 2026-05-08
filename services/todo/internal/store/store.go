// Package store handles database operations for the todo service.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

func (s *Store) CreateTask(ctx context.Context, t *domain.Task) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO todo.tasks
			(id, user_id, title, description, priority, status, tags, due_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		t.ID, t.UserID, t.Title, t.Description,
		int32(t.Priority), int32(t.Status), t.Tags,
		t.DueAt, t.CreatedAt, t.UpdatedAt,
	)
	return err
}

func (s *Store) GetTask(ctx context.Context, userID, taskID string) (*domain.Task, error) {
	var t domain.Task
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, title, description, priority, status, tags,
		       due_at, created_at, updated_at
		FROM todo.tasks
		WHERE id = $1 AND user_id = $2`, taskID, userID,
	).Scan(&t.ID, &t.UserID, &t.Title, &t.Description,
		&t.Priority, &t.Status, &t.Tags,
		&t.DueAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get task: %w", err)
	}
	return &t, nil
}

type ListFilter struct {
	UserID   string
	Status   *domain.Status
	Priority *domain.Priority
	PageSize int32
	// cursor: last seen (created_at, id) pair for keyset pagination
	CursorCreatedAt *time.Time
	CursorID        *string
}

func (s *Store) ListTasks(ctx context.Context, f ListFilter) ([]*domain.Task, error) {
	if f.PageSize <= 0 || f.PageSize > 200 {
		f.PageSize = 50
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, title, description, priority, status, tags,
		       due_at, created_at, updated_at
		FROM todo.tasks
		WHERE user_id = $1
		  AND ($2::int IS NULL OR status   = $2)
		  AND ($3::int IS NULL OR priority = $3)
		  AND ($4::timestamptz IS NULL OR (created_at, id) < ($4, $5))
		ORDER BY created_at DESC, id DESC
		LIMIT $6`,
		f.UserID,
		f.Status, f.Priority,
		f.CursorCreatedAt, f.CursorID,
		f.PageSize,
	)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*domain.Task
	for rows.Next() {
		var t domain.Task
		if err := rows.Scan(&t.ID, &t.UserID, &t.Title, &t.Description,
			&t.Priority, &t.Status, &t.Tags,
			&t.DueAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

func (s *Store) UpdateTask(ctx context.Context, t *domain.Task) (*domain.Task, error) {
	var updated domain.Task
	err := s.db.QueryRow(ctx, `
		UPDATE todo.tasks
		SET title=$3, description=$4, priority=$5, status=$6,
		    tags=$7, due_at=$8, updated_at=$9
		WHERE id=$1 AND user_id=$2
		RETURNING id, user_id, title, description, priority, status, tags,
		          due_at, created_at, updated_at`,
		t.ID, t.UserID, t.Title, t.Description,
		int32(t.Priority), int32(t.Status), t.Tags,
		t.DueAt, t.UpdatedAt,
	).Scan(&updated.ID, &updated.UserID, &updated.Title, &updated.Description,
		&updated.Priority, &updated.Status, &updated.Tags,
		&updated.DueAt, &updated.CreatedAt, &updated.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update task: %w", err)
	}
	return &updated, nil
}

func (s *Store) DeleteTask(ctx context.Context, userID, taskID string) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM todo.tasks WHERE id = $1 AND user_id = $2`, taskID, userID)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// isUniqueViolation checks for Postgres SQLSTATE 23505.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// keep linter happy — will be used in Phase 2 outbox
var _ = isUniqueViolation
