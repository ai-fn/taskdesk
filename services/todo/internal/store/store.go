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

// taskCols is used for single-row queries (GET, UPDATE RETURNING, etc.).
// Subtask counts and blocked/deps are placeholders; only ListTasks computes real values.
const taskCols = `id, user_id, title, description, priority, status, tags,
                  due_at, created_at, updated_at, list_id, flagged, deleted_at,
                  completion_score, COALESCE(completion_insight, ''),
                  parent_task_id, 0::int, 0::int,
                  false, ARRAY[]::text[]`

func scanTask(row interface {
	Scan(dest ...any) error
}, t *domain.Task) error {
	return row.Scan(
		&t.ID, &t.UserID, &t.Title, &t.Description,
		&t.Priority, &t.Status, &t.Tags,
		&t.DueAt, &t.CreatedAt, &t.UpdatedAt,
		&t.ListID, &t.Flagged, &t.DeletedAt,
		&t.CompletionScore, &t.CompletionInsight,
		&t.ParentTaskID, &t.SubtaskCount, &t.SubtaskDoneCount,
		&t.Blocked, &t.DependsOnTaskIDs,
	)
}

// ── CreateTask ────────────────────────────────────────────────────────────────

func (s *Store) CreateTask(ctx context.Context, t *domain.Task) error {
	tags := t.Tags
	if tags == nil {
		tags = []string{}
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO todo.tasks
			(id, user_id, title, description, priority, status, tags,
			 due_at, created_at, updated_at, list_id, flagged, parent_task_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		t.ID, t.UserID, t.Title, t.Description,
		int32(t.Priority), int32(t.Status), tags,
		t.DueAt, t.CreatedAt, t.UpdatedAt,
		t.ListID, t.Flagged, t.ParentTaskID,
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
	ListIDFilter   *string // nil = all; &"" = tasks with no list; &"<id>" = specific list
	FlaggedOnly    bool
	IncludeDeleted bool // true = show ONLY soft-deleted; false = show only active
	DueTodayOnly   bool
	HasDueDate     bool
	ParentTaskID   *string // nil = root tasks only; set = subtasks of this parent
	ExcludeBlocked bool    // true = omit tasks with any unfinished dependency
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
		SELECT t.id, t.user_id, t.title, t.description, t.priority, t.status, t.tags,
		       t.due_at, t.created_at, t.updated_at, t.list_id, t.flagged, t.deleted_at,
		       t.completion_score, COALESCE(t.completion_insight, ''),
		       t.parent_task_id,
		       COALESCE(sc.subtask_count, 0)::int,
		       COALESCE(sc.subtask_done_count, 0)::int,
		       COALESCE(dep.blocked, false) AS blocked,
		       COALESCE(dep.dep_ids, ARRAY[]::text[]) AS depends_on_task_ids
		FROM todo.tasks t
		LEFT JOIN (
		  SELECT parent_task_id,
		    COUNT(*) FILTER (WHERE deleted_at IS NULL) AS subtask_count,
		    COUNT(*) FILTER (WHERE status = 3 AND deleted_at IS NULL) AS subtask_done_count
		  FROM todo.tasks
		  WHERE parent_task_id IS NOT NULL
		  GROUP BY parent_task_id
		) sc ON sc.parent_task_id = t.id
		LEFT JOIN (
		  SELECT d.task_id,
		         ARRAY_AGG(d.depends_on_id) AS dep_ids,
		         BOOL_OR(t2.status <> 3 AND t2.deleted_at IS NULL) AS blocked
		  FROM todo.task_dependencies d
		  JOIN todo.tasks t2 ON t2.id = d.depends_on_id
		  GROUP BY d.task_id
		) dep ON dep.task_id = t.id
		WHERE t.user_id = $1
		  AND ($2::int IS NULL OR t.status   = $2)
		  AND ($3::int IS NULL OR t.priority = $3)
		  AND ($4::timestamptz IS NULL OR (t.created_at, t.id) < ($4, $5))
		  AND (CASE WHEN $7 THEN t.deleted_at IS NOT NULL ELSE t.deleted_at IS NULL END)
		  AND ($8::text IS NULL OR t.list_id IS NOT DISTINCT FROM NULLIF($8, ''))
		  AND (NOT $9::boolean OR t.flagged = TRUE)
		  AND (NOT $10::boolean OR DATE(t.due_at AT TIME ZONE 'UTC') = CURRENT_DATE)
		  AND (NOT $11::boolean OR t.due_at IS NOT NULL)
		  AND (CASE WHEN $12::text IS NULL THEN t.parent_task_id IS NULL
		            ELSE t.parent_task_id = $12 END)
		  AND (NOT $13::boolean OR COALESCE(dep.blocked, false) = FALSE)
		ORDER BY t.created_at DESC, t.id DESC
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
		f.ParentTaskID,
		f.ExcludeBlocked,
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

// ── AutoCompleteParent ────────────────────────────────────────────────────────

// AutoCompleteParent marks the parent task as DONE if all its non-deleted
// subtasks are now done. It is a no-op if any sibling is still incomplete,
// or if the parent is already done. Returns the updated parent or nil if
// no update was made.
func (s *Store) AutoCompleteParent(ctx context.Context, userID, parentTaskID string) (*domain.Task, error) {
	var t domain.Task
	err := scanTask(s.db.QueryRow(ctx, `
		UPDATE todo.tasks SET status=3, updated_at=now()
		WHERE id=$1 AND user_id=$2 AND status != 3
		  AND NOT EXISTS (
		    SELECT 1 FROM todo.tasks
		    WHERE parent_task_id=$1 AND deleted_at IS NULL AND status != 3
		  )
		RETURNING `+taskCols, parentTaskID, userID), &t)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // nothing to do
		}
		return nil, fmt.Errorf("auto-complete parent: %w", err)
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
