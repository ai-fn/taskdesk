package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/jackc/pgx/v5"
)

// ErrCycleDetected is returned by AddDependency when the proposed edge would
// introduce a directed cycle in the user's dependency graph.
var ErrCycleDetected = errors.New("dependency would create a cycle")

// ErrDependencyExists is returned by AddDependency when the same edge is
// already present (matches the PK unique-violation).
var ErrDependencyExists = errors.New("dependency already exists")

// AddDependency inserts (taskID → dependsOnID), rejecting self-loops, cycles,
// and tasks not owned by the user. All checks run in a single transaction so
// concurrent AddDependency calls cannot race past the cycle check.
func (s *Store) AddDependency(ctx context.Context, userID, taskID, dependsOnID string) error {
	if taskID == dependsOnID {
		return ErrCycleDetected
	}
	return withTx(ctx, s.db, func(tx pgx.Tx) error {
		// 1. Both tasks must exist and belong to the user.
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM todo.tasks
			WHERE user_id = $1 AND id = ANY($2::text[]) AND deleted_at IS NULL`,
			userID, []string{taskID, dependsOnID},
		).Scan(&count); err != nil {
			return fmt.Errorf("verify tasks: %w", err)
		}
		if count != 2 {
			return ErrNotFound
		}

		// 2. Load all existing edges for this user; run DFS with proposed edge.
		rows, err := tx.Query(ctx,
			`SELECT task_id, depends_on_id FROM todo.task_dependencies WHERE user_id = $1`,
			userID)
		if err != nil {
			return fmt.Errorf("load edges: %w", err)
		}
		var edges []Edge
		for rows.Next() {
			var e Edge
			if err := rows.Scan(&e.From, &e.To); err != nil {
				rows.Close()
				return err
			}
			edges = append(edges, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		if HasCycle(edges, Edge{From: taskID, To: dependsOnID}) {
			return ErrCycleDetected
		}

		// 3. Insert (PK violation = duplicate edge).
		if _, err := tx.Exec(ctx, `
			INSERT INTO todo.task_dependencies (task_id, depends_on_id, user_id)
			VALUES ($1, $2, $3)`,
			taskID, dependsOnID, userID,
		); err != nil {
			if isUniqueViolation(err) {
				return ErrDependencyExists
			}
			return fmt.Errorf("insert dependency: %w", err)
		}
		return nil
	})
}

// RemoveDependency deletes the (taskID → dependsOnID) edge if it exists.
func (s *Store) RemoveDependency(ctx context.Context, userID, taskID, dependsOnID string) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM todo.task_dependencies
		 WHERE task_id = $1 AND depends_on_id = $2 AND user_id = $3`,
		taskID, dependsOnID, userID)
	if err != nil {
		return fmt.Errorf("remove dependency: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListDependencies returns:
//   - dependsOn: tasks that `taskID` is blocked by
//   - blocking:  tasks blocked by `taskID`
//
// Both lists go through the same JOIN pipeline as ListTasks so the Blocked
// and DependsOnTaskIDs computed fields are populated correctly.
func (s *Store) ListDependencies(ctx context.Context, userID, taskID string) (dependsOn, blocking []*domain.Task, err error) {
	dependsOn, err = s.queryTasksByDep(ctx, userID,
		`SELECT depends_on_id FROM todo.task_dependencies WHERE task_id = $1 AND user_id = $2`,
		taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("list deps: %w", err)
	}
	blocking, err = s.queryTasksByDep(ctx, userID,
		`SELECT task_id FROM todo.task_dependencies WHERE depends_on_id = $1 AND user_id = $2`,
		taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("list blocking: %w", err)
	}
	return dependsOn, blocking, nil
}

// queryTasksByDep runs the standard ListTasks SELECT for any subset of task
// IDs identified by a subquery. The subquery's SELECT must yield a single
// task-id column.
func (s *Store) queryTasksByDep(ctx context.Context, userID, idSubquery, paramTaskID string) ([]*domain.Task, error) {
	q := `
		SELECT t.id, t.user_id, t.title, t.description, t.priority, t.status, t.tags,
		       t.due_at, t.created_at, t.updated_at, t.list_id, t.flagged, t.deleted_at,
		       t.completion_score, COALESCE(t.completion_insight, ''),
		       t.parent_task_id,
		       COALESCE(sc.subtask_count, 0)::int,
		       COALESCE(sc.subtask_done_count, 0)::int,
		       COALESCE(dep.blocked, false),
		       COALESCE(dep.dep_ids, ARRAY[]::text[])
		FROM todo.tasks t
		LEFT JOIN (
		  SELECT parent_task_id,
		    COUNT(*) FILTER (WHERE deleted_at IS NULL) AS subtask_count,
		    COUNT(*) FILTER (WHERE status = 3 AND deleted_at IS NULL) AS subtask_done_count
		  FROM todo.tasks WHERE parent_task_id IS NOT NULL GROUP BY parent_task_id
		) sc ON sc.parent_task_id = t.id
		LEFT JOIN (
		  SELECT d.task_id,
		         ARRAY_AGG(d.depends_on_id) AS dep_ids,
		         BOOL_OR(t2.status <> 3 AND t2.deleted_at IS NULL) AS blocked
		  FROM todo.task_dependencies d
		  JOIN todo.tasks t2 ON t2.id = d.depends_on_id
		  GROUP BY d.task_id
		) dep ON dep.task_id = t.id
		WHERE t.id IN (` + idSubquery + `)
		  AND t.user_id = $2
		  AND t.deleted_at IS NULL
		ORDER BY t.created_at DESC`
	rows, err := s.db.Query(ctx, q, paramTaskID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Task
	for rows.Next() {
		var t domain.Task
		if err := scanTask(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// TreeNode is a single entry in a transitive dependency walk.
//   Depth=1 ⇒ direct dep of the starting task. Depth>1 ⇒ reached via ViaTaskID.
//   ViaTaskID is the immediate predecessor on the chain — the root task at depth=1.
type TreeNode struct {
	Task  *domain.Task
	Depth int
	Via   string
}

// GetDependencyTree walks the transitive dependency graph in both directions
// from `taskID`. Uses Postgres WITH RECURSIVE — the canonical SQL idiom for
// adjacency-list traversal. Cycles are prevented at write-time via DFS in
// AddDependency, but the recursive query also tracks visited IDs defensively.
//
//	dependsOn → tasks `taskID` is blocked by, transitively (upstream)
//	blocking  → tasks blocked by `taskID`, transitively (downstream)
func (s *Store) GetDependencyTree(ctx context.Context, userID, taskID string) (dependsOn, blocking []*TreeNode, err error) {
	dependsOn, err = s.walkTree(ctx, userID, taskID, true)
	if err != nil {
		return nil, nil, fmt.Errorf("walk upstream: %w", err)
	}
	blocking, err = s.walkTree(ctx, userID, taskID, false)
	if err != nil {
		return nil, nil, fmt.Errorf("walk downstream: %w", err)
	}
	return dependsOn, blocking, nil
}

// walkTree runs a recursive CTE in the chosen direction.
//   upstream=true  → follow d.task_id → d.depends_on_id (what `start` is blocked by)
//   upstream=false → follow d.depends_on_id → d.task_id (what `start` blocks)
func (s *Store) walkTree(ctx context.Context, userID, start string, upstream bool) ([]*TreeNode, error) {
	// Two near-identical recursive CTEs differ only in which column they
	// "follow" through the dependencies table. Keep separate to stay readable.
	var q string
	if upstream {
		q = `
		WITH RECURSIVE walk(task_id, depth, via, path) AS (
		  -- base: direct deps of the starting task
		  SELECT d.depends_on_id, 1, d.task_id, ARRAY[d.task_id, d.depends_on_id]
		  FROM todo.task_dependencies d
		  WHERE d.task_id = $1 AND d.user_id = $2
		  UNION ALL
		  -- recursive: walk one more upstream level
		  SELECT d.depends_on_id, walk.depth + 1, d.task_id, walk.path || d.depends_on_id
		  FROM todo.task_dependencies d
		  JOIN walk ON d.task_id = walk.task_id
		  WHERE d.user_id = $2
		    AND NOT (d.depends_on_id = ANY(walk.path))
		    AND walk.depth < 32
		)
		SELECT w.depth, w.via,
		       t.id, t.user_id, t.title, t.description, t.priority, t.status, t.tags,
		       t.due_at, t.created_at, t.updated_at, t.list_id, t.flagged, t.deleted_at,
		       t.completion_score, COALESCE(t.completion_insight, ''),
		       t.parent_task_id, 0::int, 0::int,
		       false, ARRAY[]::text[]
		FROM walk w
		JOIN todo.tasks t ON t.id = w.task_id
		WHERE t.deleted_at IS NULL
		ORDER BY w.depth, t.title`
	} else {
		q = `
		WITH RECURSIVE walk(task_id, depth, via, path) AS (
		  SELECT d.task_id, 1, d.depends_on_id, ARRAY[d.depends_on_id, d.task_id]
		  FROM todo.task_dependencies d
		  WHERE d.depends_on_id = $1 AND d.user_id = $2
		  UNION ALL
		  SELECT d.task_id, walk.depth + 1, d.depends_on_id, walk.path || d.task_id
		  FROM todo.task_dependencies d
		  JOIN walk ON d.depends_on_id = walk.task_id
		  WHERE d.user_id = $2
		    AND NOT (d.task_id = ANY(walk.path))
		    AND walk.depth < 32
		)
		SELECT w.depth, w.via,
		       t.id, t.user_id, t.title, t.description, t.priority, t.status, t.tags,
		       t.due_at, t.created_at, t.updated_at, t.list_id, t.flagged, t.deleted_at,
		       t.completion_score, COALESCE(t.completion_insight, ''),
		       t.parent_task_id, 0::int, 0::int,
		       false, ARRAY[]::text[]
		FROM walk w
		JOIN todo.tasks t ON t.id = w.task_id
		WHERE t.deleted_at IS NULL
		ORDER BY w.depth, t.title`
	}

	rows, err := s.db.Query(ctx, q, start, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*TreeNode
	for rows.Next() {
		var n TreeNode
		var t domain.Task
		if err := rows.Scan(
			&n.Depth, &n.Via,
			&t.ID, &t.UserID, &t.Title, &t.Description,
			&t.Priority, &t.Status, &t.Tags,
			&t.DueAt, &t.CreatedAt, &t.UpdatedAt,
			&t.ListID, &t.Flagged, &t.DeletedAt,
			&t.CompletionScore, &t.CompletionInsight,
			&t.ParentTaskID, &t.SubtaskCount, &t.SubtaskDoneCount,
			&t.Blocked, &t.DependsOnTaskIDs,
		); err != nil {
			return nil, err
		}
		n.Task = &t
		out = append(out, &n)
	}
	return out, rows.Err()
}

// HasUnfinishedDependencies reports whether `taskID` has any dependency
// (depends_on_id) that is not yet status=DONE (3) and not soft-deleted.
// Used as a precondition for CompleteTask.
func (s *Store) HasUnfinishedDependencies(ctx context.Context, taskID string) (bool, error) {
	var blocked bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM todo.task_dependencies d
		  JOIN todo.tasks t ON t.id = d.depends_on_id
		  WHERE d.task_id = $1
		    AND t.status <> 3
		    AND t.deleted_at IS NULL
		)`, taskID).Scan(&blocked)
	if err != nil {
		return false, fmt.Errorf("check unfinished deps: %w", err)
	}
	return blocked, nil
}
