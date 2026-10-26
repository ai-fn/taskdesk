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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultEmbedRPM is Voyage's free-tier limit; raise it via config for a paid key.
const DefaultEmbedRPM = 3

// Slow handlers need small batches and a rebalance window that outlasts a batch.
const (
	pollRecords      = 5
	minRebalanceWait = 5 * time.Minute
	callTimeout      = 10 * time.Second // per gRPC call, so a hung peer can't stall the batch
)

// backfillPage is how many tasks one Voyage request embeds (one rate-limit token).
const backfillPage = 50

// proPlan is the only plan with semantic search, so only its tasks are embedded.
const proPlan = "pro"

// textEmbedder is the part of *embedder.Embedder the handler uses.
type textEmbedder interface {
	Embed(ctx context.Context, text, inputType string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string, inputType string) ([][]float32, error)
}

// Handler processes task events and upserts/deletes embeddings.
type Handler struct {
	emb        textEmbedder
	todoClient todov1.TodoServiceClient
	authClient authv1.AuthServiceClient
	log        *slog.Logger
	limiter    *rate.Limiter
	// maxLiveWait bounds the limiter wait inside Handle. 2*interval assumes at most one
	// backfill waiter ahead of us; a second backfill goroutine would make live events fail fast.
	maxLiveWait time.Duration
}

// New builds a Handler limited to rpm Voyage requests per minute (per pod); rpm < 1 means DefaultEmbedRPM.
func New(emb *embedder.Embedder, tc todov1.TodoServiceClient, ac authv1.AuthServiceClient, log *slog.Logger, rpm int) *Handler {
	if rpm < 1 {
		rpm = DefaultEmbedRPM
	}
	interval := time.Minute / time.Duration(rpm)
	return &Handler{
		emb: emb, todoClient: tc, authClient: ac, log: log,
		limiter:     rate.NewLimiter(rate.Every(interval), 1),
		maxLiveWait: 2*interval + 5*time.Second,
	}
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
	pro, err := h.isPro(ctx, ev.UserID)
	if err != nil {
		return err
	}
	if !pro {
		return nil
	}

	// Fetch the full task to get title + description for richer embeddings.
	getCtx, cancelGet := context.WithTimeout(ctx, callTimeout)
	defer cancelGet()
	resp, err := h.todoClient.GetTask(getCtx, &todov1.GetTaskRequest{
		UserId: ev.UserID,
		TaskId: ev.TaskID,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound { // purged since the event was published
			return nil
		}
		return fmt.Errorf("get task %s: %w", ev.TaskID, err)
	}

	// Build the text to embed: "title. description" (description optional).
	text := resp.Task.Title
	if resp.Task.Description != "" {
		text += ". " + resp.Task.Description
	}

	waitCtx, cancel := context.WithTimeout(ctx, h.maxLiveWait)
	defer cancel()
	if err := h.limiter.Wait(waitCtx); err != nil {
		return fmt.Errorf("rate limiter: %w", err)
	}
	vec, err := h.emb.Embed(ctx, text, "document")
	if err != nil {
		return fmt.Errorf("embed task %s: %w", ev.TaskID, err)
	}

	upCtx, cancelUp := context.WithTimeout(ctx, callTimeout)
	defer cancelUp()
	_, err = h.todoClient.UpsertTaskEmbedding(upCtx, &todov1.UpsertTaskEmbeddingRequest{
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

// rebalanceTimeout outlasts a full batch of limiter-paced records, whatever EMBED_RPM is.
func (h *Handler) rebalanceTimeout() time.Duration {
	return max(minRebalanceWait, pollRecords*(h.maxLiveWait+30*time.Second))
}

// isPro reports whether the user's plan includes semantic search.
func (h *Handler) isPro(ctx context.Context, userID string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	resp, err := h.authClient.GetUserByID(ctx, &authv1.GetUserByIDRequest{UserId: userID})
	if err != nil {
		if status.Code(err) == codes.NotFound { // deleted user: nothing to embed, don't retry
			return false, nil
		}
		return false, fmt.Errorf("get user %s: %w", userID, err)
	}
	return resp.GetUser().GetPlan() == proPlan, nil
}

// Backfill embeds pro users' tasks that have no embedding yet, so restarts and
// extra replicas cost only the listing. It shares the live consumer's rate limiter.
func (h *Handler) Backfill(ctx context.Context) {
	// Give auth up to 2 minutes to become ready. WaitForReady retries
	// Unavailable errors automatically so a pod-startup race doesn't
	// permanently skip the backfill.
	bCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	usersResp, err := h.authClient.ListUserIDs(bCtx, &authv1.ListUserIDsRequest{PlanFilter: proPlan}, grpc.WaitForReady(true))
	if err != nil {
		h.log.Error("backfill: list users", "err", err)
		return
	}

	total, done := 0, 0
	for _, u := range usersResp.Users {
		userID := u.UserId
		var pageToken string
		for {
			resp, err := h.todoClient.ListUnembeddedTasks(ctx, &todov1.ListUnembeddedTasksRequest{
				UserId:    userID,
				PageSize:  backfillPage,
				PageToken: pageToken,
			})
			if err != nil {
				h.log.Error("backfill: list tasks", "user_id", userID, "err", err)
				break
			}

			if len(resp.Tasks) > 0 {
				total += len(resp.Tasks)
				n, err := h.embedPage(ctx, userID, resp.Tasks)
				if err != nil {
					return // context cancelled
				}
				done += n
			}

			if resp.NextPageToken == "" {
				break
			}
			pageToken = resp.NextPageToken
		}
	}

	h.log.Info("backfill complete", "embedded", done, "total", total)
}

// embedPage embeds a page of tasks in one Voyage request and stores the vectors.
// It returns an error only when ctx ends; a failed page is logged and skipped this pass.
func (h *Handler) embedPage(ctx context.Context, userID string, tasks []*todov1.Task) (int, error) {
	texts := make([]string, len(tasks))
	for i, t := range tasks {
		texts[i] = t.Title
		if t.Description != "" {
			texts[i] += ". " + t.Description
		}
	}

	if err := h.limiter.Wait(ctx); err != nil {
		return 0, err
	}
	vecs, err := h.emb.EmbedBatch(ctx, texts, "document")
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		h.log.Error("backfill: embed page", "user_id", userID, "tasks", len(tasks), "err", err)
		return 0, nil
	}

	done := 0
	for i, t := range tasks {
		if _, err := h.todoClient.UpsertTaskEmbedding(ctx, &todov1.UpsertTaskEmbeddingRequest{
			UserId:    userID,
			TaskId:    t.Id,
			Embedding: vecs[i],
		}); err != nil {
			if ctx.Err() != nil {
				return done, ctx.Err()
			}
			h.log.Error("backfill: upsert embedding", "task_id", t.Id, "err", err)
			continue
		}
		done++
	}
	h.log.Info("backfill: embedded page", "user_id", userID, "embedded", done, "tasks", len(tasks))
	return done, nil
}

// Runner wraps pkgkafka.Consumer and wires it to Handler.
type Runner struct {
	consumer *pkgkafka.Consumer
	handler  *Handler
}

func NewRunner(brokers []string, h *Handler, log *slog.Logger) (*Runner, error) {
	c, err := pkgkafka.NewConsumer(brokers, "ai-embedder", Topics(), log,
		pkgkafka.WithMaxPollRecords(pollRecords), pkgkafka.WithRebalanceTimeout(h.rebalanceTimeout()))
	if err != nil {
		return nil, err
	}
	return &Runner{consumer: c, handler: h}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	return r.consumer.Run(ctx, r.handler.Handle)
}
