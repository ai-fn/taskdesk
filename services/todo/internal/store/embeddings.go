package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/fsociety/todo/services/todo/internal/domain"
)

// formatVec serialises a float32 slice into pgvector's text literal "[a,b,c]".
// PostgreSQL accepts this via a ::vector cast, so we need no special Go codec.
func formatVec(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'f', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// UpsertTaskEmbedding stores or replaces the embedding for a task.
func (s *Store) UpsertTaskEmbedding(ctx context.Context, taskID, userID string, embedding []float32) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO todo.task_embeddings (task_id, user_id, embedding, updated_at)
		 VALUES ($1, $2, $3::vector, now())
		 ON CONFLICT (task_id)
		 DO UPDATE SET embedding = EXCLUDED.embedding, updated_at = now()`,
		taskID, userID, formatVec(embedding),
	)
	if err != nil {
		return fmt.Errorf("upsert embedding: %w", err)
	}
	return nil
}

// DeleteTaskEmbedding removes the embedding for a task (called on purge).
func (s *Store) DeleteTaskEmbedding(ctx context.Context, taskID, userID string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM todo.task_embeddings WHERE task_id=$1 AND user_id=$2`,
		taskID, userID,
	)
	if err != nil {
		return fmt.Errorf("delete embedding: %w", err)
	}
	return nil
}

// ListUnembeddedTasks pages a user's live tasks without an embedding by id (keyset on afterID,
// empty = first page). Index: idx_tasks_user_id narrows to the user; task_embeddings PK anti-joins.
func (s *Store) ListUnembeddedTasks(ctx context.Context, userID, afterID string, limit int) ([]*domain.Task, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+qualifiedTaskCols+`
		 FROM todo.tasks t
		 LEFT JOIN todo.task_embeddings e ON e.task_id = t.id
		 WHERE t.user_id = $1
		   AND t.deleted_at IS NULL
		   AND e.task_id IS NULL
		   AND t.id > $2
		 ORDER BY t.id
		 LIMIT $3`,
		userID, afterID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list unembedded tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*domain.Task
	for rows.Next() {
		var t domain.Task
		if err := scanTask(rows, &t); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

// qualifiedTaskCols is taskCols for queries that alias tasks as t; scanTask reads it.
const qualifiedTaskCols = `t.id, t.user_id, t.title, t.description, t.priority, t.status, t.tags,
		        t.due_at, t.created_at, t.updated_at, t.list_id, t.flagged, t.deleted_at,
		        t.completion_score, COALESCE(t.completion_insight, ''),
		        t.parent_task_id, 0::int, 0::int,
		        false, ARRAY[]::text[]`

// SearchTasksByEmbedding returns the top-k tasks for a user ordered by cosine
// similarity to the query embedding. Soft-deleted tasks are excluded.
// <=> is pgvector's cosine distance operator; ORDER BY ASC = most similar first.
func (s *Store) SearchTasksByEmbedding(ctx context.Context, userID string, queryEmbedding []float32, limit int) ([]*domain.Task, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Query(ctx,
		`SELECT t.id, t.user_id, t.title, t.description, t.priority, t.status, t.tags,
		        t.due_at, t.created_at, t.updated_at, t.list_id, t.flagged, t.deleted_at,
		        t.completion_score, COALESCE(t.completion_insight, ''),
		        t.parent_task_id, 0::int, 0::int,
		        false, ARRAY[]::text[]
		 FROM todo.tasks t
		 JOIN todo.task_embeddings e ON e.task_id = t.id
		 WHERE t.user_id = $1
		   AND t.deleted_at IS NULL
		 ORDER BY e.embedding <=> $2::vector
		 LIMIT $3`,
		userID, formatVec(queryEmbedding), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*domain.Task
	for rows.Next() {
		var t domain.Task
		if err := scanTask(rows, &t); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

// SearchTasksByText returns tasks matching a free-text query using full-text
// and trigram search (pg_trgm extension, migration 000010).
// Filtering uses:
//   - @@ plainto_tsquery  → hits idx_tasks_text_fts (GIN tsvector)
//   - % operator          → hits idx_tasks_text_trgm (GIN gin_trgm_ops)
//
// Results are ordered by similarity score descending.
func (s *Store) SearchTasksByText(ctx context.Context, userID, query string, limit int) ([]*domain.Task, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Query(ctx,
		`SELECT t.id, t.user_id, t.title, t.description, t.priority, t.status, t.tags,
		        t.due_at, t.created_at, t.updated_at, t.list_id, t.flagged, t.deleted_at,
		        t.completion_score, COALESCE(t.completion_insight, ''),
		        t.parent_task_id, 0::int, 0::int,
		        false, ARRAY[]::text[]
		 FROM todo.tasks t
		 WHERE t.user_id = $1
		   AND t.deleted_at IS NULL
		   AND (
		         to_tsvector('simple', t.title || ' ' || t.description) @@ plainto_tsquery('simple', $2)
		         OR (t.title || ' ' || t.description) % $2
		       )
		 ORDER BY similarity(t.title || ' ' || t.description, $2) DESC
		 LIMIT $3`,
		userID, query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search tasks by text: %w", err)
	}
	defer rows.Close()

	var tasks []*domain.Task
	for rows.Next() {
		var t domain.Task
		if err := scanTask(rows, &t); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}
