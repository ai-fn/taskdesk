package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/fsociety/todo/pkg/events"
	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/jackc/pgx/v5"
)

// ErrBlocked means a task cannot be completed while a dependency is still open.
var ErrBlocked = errors.New("task is blocked by unfinished dependencies")

// ErrNoChange is returned by a ModifyTask callback to commit nothing (and emit no event).
var ErrNoChange = errors.New("no change")

// ModifyTask applies fn to the task under a row lock (NO KEY UPDATE: it serializes editors but doesn't
// block inserts that merely reference the row), so concurrent edits serialize instead of
// overwriting each other. Moving a task to done emits task.completed (stamped with n) and is
// refused with ErrBlocked while a dependency is open; any other change emits task.updated.
// Completing a subtask completes its parent when every sibling is done, in the same transaction.
func (s *Store) ModifyTask(ctx context.Context, userID, taskID string, n Notify, fn func(cur *domain.Task) error) (*domain.Task, error) {
	var out domain.Task
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		// The parent is locked before the child so two siblings completing at once serialize on it,
		// and the second one sees the first as done.
		var parentID *string
		if err := tx.QueryRow(ctx,
			`SELECT parent_task_id FROM todo.tasks WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
			taskID, userID).Scan(&parentID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("modify task: %w", err)
		}
		if parentID != nil {
			if _, err := tx.Exec(ctx,
				`SELECT 1 FROM todo.tasks WHERE id = $1 AND user_id = $2 FOR NO KEY UPDATE`, *parentID, userID); err != nil {
				return fmt.Errorf("lock parent task: %w", err)
			}
		}

		var cur domain.Task
		err := scanTask(tx.QueryRow(ctx, `
			SELECT `+taskCols+`
			FROM todo.tasks
			WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
			FOR NO KEY UPDATE`, taskID, userID), &cur)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock task: %w", err)
		}
		wasDone := cur.Status == domain.StatusDone

		if err := fn(&cur); err != nil {
			if errors.Is(err, ErrNoChange) {
				out = cur
				return nil
			}
			return err
		}
		completing := cur.Status == domain.StatusDone && !wasDone
		if completing {
			blocked, err := hasUnfinishedDependencies(ctx, tx, cur.ID)
			if err != nil {
				return err
			}
			if blocked {
				return ErrBlocked
			}
		}
		if err := requireListOrUnchanged(ctx, tx, cur.UserID, cur.ID, cur.ListID); err != nil {
			return err
		}
		if err := scanTask(tx.QueryRow(ctx, `
			UPDATE todo.tasks
			SET title=$3, description=$4, priority=$5, status=$6,
			    tags=$7, due_at=$8, updated_at=$9, list_id=$10, flagged=$11
			WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL
			RETURNING `+taskCols,
			cur.ID, cur.UserID, cur.Title, cur.Description,
			int32(cur.Priority), int32(cur.Status), cur.Tags,
			cur.DueAt, cur.UpdatedAt, cur.ListID, cur.Flagged,
		), &out); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return mapRefErr(err)
		}

		if !completing {
			return insertOutbox(ctx, tx, events.TypeTaskUpdated, out.UserID, out.ID, out.Title, Notify{})
		}
		if err := insertOutbox(ctx, tx, events.TypeTaskCompleted, out.UserID, out.ID, out.Title, n); err != nil {
			return err
		}
		if parentID != nil {
			// The parent's event carries no recipient: one click must not send two "task completed" emails.
			return completeParentIfDone(ctx, tx, userID, *parentID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// completeParentIfDone marks a (locked) parent done once all its live subtasks are done and its own
// dependencies are met, emitting task.completed for it. Only one level of ancestry is completed: a
// cascade would need root-first locking to avoid deadlocking with a concurrent edit of the parent.
func completeParentIfDone(ctx context.Context, tx pgx.Tx, userID, parentID string) error {
	var title string
	err := tx.QueryRow(ctx, `
		UPDATE todo.tasks SET status = 3, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND status <> 3 AND deleted_at IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM todo.tasks
		    WHERE parent_task_id = $1 AND user_id = $2 AND deleted_at IS NULL AND status <> 3
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM todo.task_dependencies d
		    JOIN todo.tasks dep ON dep.id = d.depends_on_id
		    WHERE d.task_id = $1 AND dep.status <> 3 AND dep.deleted_at IS NULL
		  )
		RETURNING title`, parentID, userID).Scan(&title)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // siblings still open, parent blocked, or already done
		}
		return fmt.Errorf("auto-complete parent: %w", err)
	}
	return insertOutbox(ctx, tx, events.TypeTaskCompleted, userID, parentID, title, Notify{})
}
