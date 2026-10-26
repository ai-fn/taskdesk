package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
		        false, ARRAY[]::text[], t.attachment_count`

// SearchTasksByEmbedding returns the top-k tasks for a user ordered by cosine
// similarity to the query embedding. Soft-deleted tasks are excluded.
// <=> is pgvector's cosine distance operator; ORDER BY ASC = most similar first.
//
// When the planner picks the HNSW index it post-filters by user, so a user with few tasks can get
// empty results if the top ef_search candidates belong to others; iterative scan (pgvector >= 0.8)
// keeps scanning until enough rows match. At small scale the planner drives from tasks by user_id
// instead, so this is insurance. Older pgvector lacks the setting: we fall back to the plain query.
func (s *Store) SearchTasksByEmbedding(ctx context.Context, userID string, queryEmbedding []float32, limit int) ([]*domain.Task, error) {
	if limit <= 0 {
		limit = 20
	}

	tasks, err := s.searchByEmbeddingIterative(ctx, userID, queryEmbedding, limit)
	if isUnknownSetting(err) {
		return s.queryTasksByEmbedding(ctx, s.db, userID, queryEmbedding, limit)
	}
	return tasks, err
}

// isUnknownSetting reports a SET of a parameter the server doesn't know: 42602 once pgvector is loaded
// (the "hnsw" prefix is reserved), 42704 otherwise.
func isUnknownSetting(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "42602" || pgErr.Code == "42704")
}

func (s *Store) searchByEmbeddingIterative(ctx context.Context, userID string, queryEmbedding []float32, limit int) ([]*domain.Task, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("search tasks: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL hnsw.iterative_scan = strict_order`); err != nil {
		return nil, fmt.Errorf("search tasks: enable iterative scan: %w", err)
	}
	return s.queryTasksByEmbedding(ctx, tx, userID, queryEmbedding, limit)
}

type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (s *Store) queryTasksByEmbedding(ctx context.Context, q queryer, userID string, queryEmbedding []float32, limit int) ([]*domain.Task, error) {
	rows, err := q.Query(ctx,
		`SELECT `+qualifiedTaskCols+`
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
