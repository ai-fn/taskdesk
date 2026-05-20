// Package store handles database operations for the todo service.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/google/uuid"
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

// ── Task columns helper ───────────────────────────────────────────────────────

const taskCols = `id, user_id, title, description, priority, status, tags,
                  due_at, created_at, updated_at, list_id, flagged, deleted_at,
                  completion_score, COALESCE(completion_insight, '')`

func scanTask(row interface {
	Scan(dest ...any) error
}, t *domain.Task) error {
	return row.Scan(
		&t.ID, &t.UserID, &t.Title, &t.Description,
		&t.Priority, &t.Status, &t.Tags,
		&t.DueAt, &t.CreatedAt, &t.UpdatedAt,
		&t.ListID, &t.Flagged, &t.DeletedAt,
		&t.CompletionScore, &t.CompletionInsight,
	)
}

// ── CreateTask ────────────────────────────────────────────────────────────────

func (s *Store) CreateTask(ctx context.Context, t *domain.Task) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO todo.tasks
			(id, user_id, title, description, priority, status, tags,
			 due_at, created_at, updated_at, list_id, flagged)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		t.ID, t.UserID, t.Title, t.Description,
		int32(t.Priority), int32(t.Status), t.Tags,
		t.DueAt, t.CreatedAt, t.UpdatedAt,
		t.ListID, t.Flagged,
	)
	return err
}

// ── GetTask ───────────────────────────────────────────────────────────────────

// GetTask retrieves a task regardless of soft-delete state (needed for restore).
func (s *Store) GetTask(ctx context.Context, userID, taskID string) (*domain.Task, error) {
	var t domain.Task
	err := scanTask(s.db.QueryRow(ctx, `
		SELECT `+taskCols+`
		FROM todo.tasks
		WHERE id = $1 AND user_id = $2`, taskID, userID), &t)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get task: %w", err)
	}
	return &t, nil
}

// ── ListTasks ─────────────────────────────────────────────────────────────────

type ListFilter struct {
	UserID   string
	Status   *domain.Status
	Priority *domain.Priority
	// List / flag filters
	ListIDFilter  *string // nil = all; &"" = tasks with no list; &"<id>" = specific list
	FlaggedOnly   bool
	IncludeDeleted bool   // true = show ONLY soft-deleted; false = show only active
	DueTodayOnly  bool
	HasDueDate    bool
	// Pagination
	PageSize        int32
	CursorCreatedAt *time.Time
	CursorID        *string
}

func (s *Store) ListTasks(ctx context.Context, f ListFilter) ([]*domain.Task, error) {
	if f.PageSize <= 0 || f.PageSize > 200 {
		f.PageSize = 50
	}

	rows, err := s.db.Query(ctx, `
		SELECT `+taskCols+`
		FROM todo.tasks
		WHERE user_id = $1
		  AND ($2::int IS NULL OR status   = $2)
		  AND ($3::int IS NULL OR priority = $3)
		  AND ($4::timestamptz IS NULL OR (created_at, id) < ($4, $5))
		  AND (CASE WHEN $7 THEN deleted_at IS NOT NULL ELSE deleted_at IS NULL END)
		  AND ($8::text IS NULL OR list_id IS NOT DISTINCT FROM NULLIF($8, ''))
		  AND (NOT $9::boolean OR flagged = TRUE)
		  AND (NOT $10::boolean OR DATE(due_at AT TIME ZONE 'UTC') = CURRENT_DATE)
		  AND (NOT $11::boolean OR due_at IS NOT NULL)
		ORDER BY created_at DESC, id DESC
		LIMIT $6`,
		f.UserID,
		f.Status, f.Priority,
		f.CursorCreatedAt, f.CursorID,
		f.PageSize,
		f.IncludeDeleted,
		f.ListIDFilter,
		f.FlaggedOnly,
		f.DueTodayOnly,
		f.HasDueDate,
	)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*domain.Task
	for rows.Next() {
		var t domain.Task
		if err := scanTask(rows, &t); err != nil {
			return nil, err
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

// ── UpdateTask ────────────────────────────────────────────────────────────────

func (s *Store) UpdateTask(ctx context.Context, t *domain.Task) (*domain.Task, error) {
	var updated domain.Task
	err := scanTask(s.db.QueryRow(ctx, `
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
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update task: %w", err)
	}
	return &updated, nil
}

// ── DeleteTask (soft) ─────────────────────────────────────────────────────────

func (s *Store) DeleteTask(ctx context.Context, userID, taskID string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE todo.tasks SET deleted_at = now(), updated_at = now()
		 WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, taskID, userID)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── RestoreTask ───────────────────────────────────────────────────────────────

func (s *Store) RestoreTask(ctx context.Context, userID, taskID string) (*domain.Task, error) {
	var t domain.Task
	err := scanTask(s.db.QueryRow(ctx, `
		UPDATE todo.tasks SET deleted_at = NULL, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NOT NULL
		RETURNING `+taskCols, taskID, userID), &t)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("restore task: %w", err)
	}
	return &t, nil
}

// ── PurgeTask (hard delete) ───────────────────────────────────────────────────

func (s *Store) PurgeTask(ctx context.Context, userID, taskID string) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM todo.tasks WHERE id = $1 AND user_id = $2 AND deleted_at IS NOT NULL`,
		taskID, userID)
	if err != nil {
		return fmt.Errorf("purge task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── List CRUD ─────────────────────────────────────────────────────────────────

func (s *Store) CreateList(ctx context.Context, l *domain.List) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO todo.lists (id, user_id, name, color, icon, position, group_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5,
		  COALESCE((SELECT MAX(position)+1 FROM todo.lists WHERE user_id=$2), 0),
		  $6, $7, $7)`,
		l.ID, l.UserID, l.Name, l.Color, l.Icon, l.GroupID, l.CreatedAt,
	)
	return err
}

func (s *Store) ListLists(ctx context.Context, userID string) ([]*domain.List, error) {
	rows, err := s.db.Query(ctx, `
		SELECT l.id, l.user_id, l.name, l.color, l.icon, l.position, l.is_hidden, l.group_id,
		       l.created_at, l.updated_at,
		       COUNT(t.id) FILTER (WHERE t.deleted_at IS NULL) AS task_count
		FROM todo.lists l
		LEFT JOIN todo.tasks t ON t.list_id = l.id AND t.user_id = l.user_id
		WHERE l.user_id = $1
		GROUP BY l.id
		ORDER BY l.position, l.created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list lists: %w", err)
	}
	defer rows.Close()

	var lists []*domain.List
	for rows.Next() {
		var l domain.List
		if err := rows.Scan(
			&l.ID, &l.UserID, &l.Name, &l.Color, &l.Icon, &l.Position, &l.IsHidden, &l.GroupID,
			&l.CreatedAt, &l.UpdatedAt, &l.TaskCount,
		); err != nil {
			return nil, err
		}
		lists = append(lists, &l)
	}
	return lists, rows.Err()
}

func (s *Store) UpdateList(ctx context.Context, l *domain.List, updateGroupID bool) (*domain.List, error) {
	var updated domain.List
	err := s.db.QueryRow(ctx, `
		UPDATE todo.lists
		SET name=$3, color=$4, is_hidden=$5, updated_at=$6,
		    group_id = CASE WHEN $7 THEN $8 ELSE group_id END,
		    icon=$9
		WHERE id=$1 AND user_id=$2
		RETURNING id, user_id, name, color, icon, position, is_hidden, group_id, created_at, updated_at`,
		l.ID, l.UserID, l.Name, l.Color, l.IsHidden, l.UpdatedAt,
		updateGroupID, l.GroupID, l.Icon,
	).Scan(
		&updated.ID, &updated.UserID, &updated.Name, &updated.Color, &updated.Icon,
		&updated.Position, &updated.IsHidden, &updated.GroupID,
		&updated.CreatedAt, &updated.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update list: %w", err)
	}
	return &updated, nil
}

func (s *Store) DeleteList(ctx context.Context, userID, listID string) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM todo.lists WHERE id = $1 AND user_id = $2`, listID, userID)
	if err != nil {
		return fmt.Errorf("delete list: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ReorderLists(ctx context.Context, userID string, orderedIDs []string) ([]*domain.List, error) {
	return withTxResult(ctx, s.db, func(tx pgx.Tx) ([]*domain.List, error) {
		for i, id := range orderedIDs {
			tag, err := tx.Exec(ctx,
				`UPDATE todo.lists SET position=$1, updated_at=now() WHERE id=$2 AND user_id=$3`,
				i, id, userID)
			if err != nil {
				return nil, fmt.Errorf("reorder list %s: %w", id, err)
			}
			if tag.RowsAffected() == 0 {
				return nil, fmt.Errorf("list %s not found for user", id)
			}
		}
		return nil, nil // caller refetches
	})
}

// ── Group CRUD ────────────────────────────────────────────────────────────────

func (s *Store) CreateGroup(ctx context.Context, g *domain.ListGroup) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO todo.list_groups (id, user_id, name, position, created_at, updated_at)
		VALUES ($1, $2, $3,
		  COALESCE((SELECT MAX(position)+1 FROM todo.list_groups WHERE user_id=$2), 0),
		  $4, $4)`,
		g.ID, g.UserID, g.Name, g.CreatedAt,
	)
	return err
}

func (s *Store) ListGroups(ctx context.Context, userID string) ([]*domain.ListGroup, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, name, position, created_at, updated_at
		FROM todo.list_groups
		WHERE user_id = $1
		ORDER BY position, created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	var groups []*domain.ListGroup
	for rows.Next() {
		var g domain.ListGroup
		if err := rows.Scan(&g.ID, &g.UserID, &g.Name, &g.Position, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, &g)
	}
	return groups, rows.Err()
}

func (s *Store) UpdateGroup(ctx context.Context, g *domain.ListGroup) (*domain.ListGroup, error) {
	var updated domain.ListGroup
	err := s.db.QueryRow(ctx, `
		UPDATE todo.list_groups SET name=$3, updated_at=$4
		WHERE id=$1 AND user_id=$2
		RETURNING id, user_id, name, position, created_at, updated_at`,
		g.ID, g.UserID, g.Name, g.UpdatedAt,
	).Scan(&updated.ID, &updated.UserID, &updated.Name, &updated.Position, &updated.CreatedAt, &updated.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update group: %w", err)
	}
	return &updated, nil
}

func (s *Store) DeleteGroup(ctx context.Context, userID, groupID string) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM todo.list_groups WHERE id = $1 AND user_id = $2`, groupID, userID)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ReorderGroups(ctx context.Context, userID string, orderedIDs []string) ([]*domain.ListGroup, error) {
	return withTxResult(ctx, s.db, func(tx pgx.Tx) ([]*domain.ListGroup, error) {
		for i, id := range orderedIDs {
			tag, err := tx.Exec(ctx,
				`UPDATE todo.list_groups SET position=$1, updated_at=now() WHERE id=$2 AND user_id=$3`,
				i, id, userID)
			if err != nil {
				return nil, fmt.Errorf("reorder group %s: %w", id, err)
			}
			if tag.RowsAffected() == 0 {
				return nil, fmt.Errorf("group %s not found for user", id)
			}
		}
		return nil, nil
	})
}

// ── UpdateTaskScore ───────────────────────────────────────────────────────────

// UpdateTaskScore stores the AI-generated completion probability on a task.
// Called by ai-service after scoring via Kafka event.
func (s *Store) UpdateTaskScore(ctx context.Context, userID, taskID string, score int, insight string) (*domain.Task, error) {
	var t domain.Task
	err := scanTask(s.db.QueryRow(ctx,
		`UPDATE todo.tasks
		 SET completion_score=$3, completion_insight=$4, updated_at=now()
		 WHERE id=$1 AND user_id=$2
		 RETURNING `+taskCols,
		taskID, userID, score, insight,
	), &t)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update task score: %w", err)
	}
	return &t, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

var _ = isUniqueViolation

func NewID() string { return uuid.New().String() }

func withTxResult[T any](ctx context.Context, db *pgxpool.Pool, fn func(pgx.Tx) (T, error)) (T, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("begin tx: %w", err)
	}
	result, err := fn(tx)
	if err != nil {
		_ = tx.Rollback(ctx)
		return result, err
	}
	return result, tx.Commit(ctx)
}
