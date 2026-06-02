// Package consumer subscribes to task.events.v1 and keeps the pgvector
// embedding index in sync with task mutations.
//
// Async enrichment pattern: todo-service writes the task first (fast path),
// then publishes an event. We consume it here, call Voyage AI to embed the
// task text, and store the vector via todo-service's UpsertTaskEmbedding RPC.
// Deleted (purged) tasks are removed from the index via DeleteTaskEmbedding.
package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/fsociety/todo/pkg/events"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/ai/internal/embedder"
	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
)

// Handler processes task events and upserts/deletes embeddings.
// limiter enforces Voyage AI free-tier rate limit: 3 RPM.
type Handler struct {
	emb        *embedder.Embedder
	todoClient todov1.TodoServiceClient
	authClient authv1.AuthServiceClient
	log        *slog.Logger
	limiter    *rate.Limiter
}

func New(emb *embedder.Embedder, tc todov1.TodoServiceClient, ac authv1.AuthServiceClient, log *slog.Logger) *Handler {
	// 3 requests per 60 seconds, burst of 1 — matches Voyage AI free-tier RPM.
	return &Handler{emb: emb, todoClient: tc, authClient: ac, log: log, limiter: rate.NewLimiter(rate.Every(20), 1)}
}

// Topics returns the Kafka topics this handler requires.
func Topics() []string { return []string{events.Topic} }

// Handle dispatches a single Kafka record.
func (h *Handler) Handle(ctx context.Context, r *kgo.Record) error {
	var ev events.TaskEvent
	if err := json.Unmarshal(r.Value, &ev); err != nil {
		h.log.Warn("skip malformed task event", "err", err)
		return nil // poison pill — don't block the partition
	}

	switch ev.EventType {
	case events.TypeTaskCreated, events.TypeTaskUpdated:
		return h.upsertEmbedding(ctx, ev)
	case events.TypeTaskDeleted:
		// Soft delete — keep the embedding; the search query already excludes
		// soft-deleted tasks via the JOIN filter in SearchTasksByEmbedding.
		return nil
	default:
		// TypeTaskCompleted and unknown types — nothing to do for embeddings.
		return nil
	}
}

func (h *Handler) upsertEmbedding(ctx context.Context, ev events.TaskEvent) error {
	// Fetch the full task to get title + description for richer embeddings.
	resp, err := h.todoClient.GetTask(ctx, &todov1.GetTaskRequest{
		UserId: ev.UserID,
		TaskId: ev.TaskID,
	})
	if err != nil {
		return fmt.Errorf("get task %s: %w", ev.TaskID, err)
	}

	// Build the text to embed: "title. description" (description optional).
	text := resp.Task.Title
	if resp.Task.Description != "" {
		text += ". " + resp.Task.Description
	}

	if err := h.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter: %w", err)
	}
	vec, err := h.emb.Embed(ctx, text, "document")
	if err != nil {
		return fmt.Errorf("embed task %s: %w", ev.TaskID, err)
	}

	_, err = h.todoClient.UpsertTaskEmbedding(ctx, &todov1.UpsertTaskEmbeddingRequest{
		UserId:    ev.UserID,
		TaskId:    ev.TaskID,
		Embedding: vec,
	})
	if err != nil {
		return fmt.Errorf("upsert embedding %s: %w", ev.TaskID, err)
	}

	h.log.Info("task embedded", "task_id", ev.TaskID, "user_id", ev.UserID)
	return nil
}

// Backfill embeds all existing tasks that don't yet have an embedding.
// Runs once at startup in a background goroutine. Shares the same rate limiter
// as the live consumer so both together stay within Voyage AI's RPM cap.
func (h *Handler) Backfill(ctx context.Context) {
	// Give auth up to 2 minutes to become ready. WaitForReady retries
	// Unavailable errors automatically so a pod-startup race doesn't
	// permanently skip the backfill.
	bCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	usersResp, err := h.authClient.ListUserIDs(bCtx, &authv1.ListUserIDsRequest{}, grpc.WaitForReady(true))
	if err != nil {
		h.log.Error("backfill: list users", "err", err)
		return
	}

	total, done := 0, 0
	for _, u := range usersResp.Users {
		userID := u.UserId
		var pageToken string
		for {
			resp, err := h.todoClient.ListTasks(ctx, &todov1.ListTasksRequest{
				UserId:    userID,
				PageSize:  50,
				PageToken: pageToken,
			})
			if err != nil {
				h.log.Error("backfill: list tasks", "user_id", userID, "err", err)
				break
			}

			for _, t := range resp.Tasks {
				total++
				text := t.Title
				if t.Description != "" {
					text += ". " + t.Description
				}

				if err := h.limiter.Wait(ctx); err != nil {
					return // context cancelled
				}

				vec, err := h.emb.Embed(ctx, text, "document")
				if err != nil {
					h.log.Error("backfill: embed", "task_id", t.Id, "err", err)
					continue
				}

				if _, err := h.todoClient.UpsertTaskEmbedding(ctx, &todov1.UpsertTaskEmbeddingRequest{
					UserId:    userID,
					TaskId:    t.Id,
					Embedding: vec,
				}); err != nil {
					h.log.Error("backfill: upsert embedding", "task_id", t.Id, "err", err)
					continue
				}

				done++
				h.log.Info("backfill: embedded task", "task_id", t.Id, "progress", fmt.Sprintf("%d/?", done))
			}

			if resp.NextPageToken == "" {
				break
			}
			pageToken = resp.NextPageToken
		}
	}

	h.log.Info("backfill complete", "embedded", done, "total", total)
}

// Runner wraps pkgkafka.Consumer and wires it to Handler.
type Runner struct {
	consumer *pkgkafka.Consumer
	handler  *Handler
}

func NewRunner(brokers []string, h *Handler, log *slog.Logger) (*Runner, error) {
	c, err := pkgkafka.NewConsumer(brokers, "ai-embedder", Topics(), log)
	if err != nil {
		return nil, err
	}
	return &Runner{consumer: c, handler: h}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	return r.consumer.Run(ctx, r.handler.Handle)
}
