package scorer

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
	"github.com/twmb/franz-go/pkg/kgo"
)

// Consumer listens for task.created events and enriches each new task with a
// completion-probability score. It runs as a separate Kafka consumer group
// ("ai-scorer") so it doesn't share offset state with the embedding consumer.
type Consumer struct {
	scorer     *Scorer
	todoClient todov1.TodoServiceClient
	authClient authv1.AuthServiceClient
	log        *slog.Logger
}

func NewConsumer(sc *Scorer, tc todov1.TodoServiceClient, ac authv1.AuthServiceClient, log *slog.Logger) *Consumer {
	return &Consumer{scorer: sc, todoClient: tc, authClient: ac, log: log}
}

// Handle dispatches a single Kafka record.
func (c *Consumer) Handle(ctx context.Context, r *kgo.Record) error {
	var ev events.TaskEvent
	if err := json.Unmarshal(r.Value, &ev); err != nil {
		c.log.Warn("scorer: skip malformed event", "err", err)
		return nil
	}

	// Only score newly created tasks.
	if ev.EventType != events.TypeTaskCreated {
		return nil
	}

	return c.scoreTask(ctx, ev)
}

func (c *Consumer) scoreTask(ctx context.Context, ev events.TaskEvent) error {
	// Fetch the full task.
	taskResp, err := c.todoClient.GetTask(ctx, &todov1.GetTaskRequest{
		UserId: ev.UserID,
		TaskId: ev.TaskID,
	})
	if err != nil {
		return fmt.Errorf("get task %s: %w", ev.TaskID, err)
	}
	t := taskResp.Task

	// Fetch recent task history (last 30 days) for behavioural context.
	// We fetch completed + open tasks separately to build a richer picture.
	histResp, err := c.todoClient.ListTasks(ctx, &todov1.ListTasksRequest{
		UserId:   ev.UserID,
		PageSize: 50,
	})
	if err != nil {
		c.log.Warn("scorer: list tasks for history", "user_id", ev.UserID, "err", err)
		histResp = &todov1.ListTasksResponse{}
	}
	doneResp, err := c.todoClient.ListTasks(ctx, &todov1.ListTasksRequest{
		UserId:   ev.UserID,
		Status:   todov1.Status_STATUS_DONE,
		PageSize: 50,
	})
	if err != nil {
		c.log.Warn("scorer: list done tasks", "user_id", ev.UserID, "err", err)
		doneResp = &todov1.ListTasksResponse{}
	}

	now := time.Now().UTC()
	history := buildHistory(histResp.Tasks, doneResp.Tasks, now)

	// Build the task input for the scorer.
	input := ScoreInput{
		Title:       t.Title,
		Description: t.Description,
		Priority:    t.Priority.String(),
		Tags:        t.Tags,
		CreatedAt:   t.CreatedAt.AsTime(),
	}
	if t.DueAt != nil {
		s := t.DueAt.AsTime().Format("2006-01-02")
		input.DueAt = &s
	}

	result, err := c.scorer.Score(ctx, now.Format("2006-01-02"), input, history)
	if err != nil {
		return fmt.Errorf("score task %s: %w", ev.TaskID, err)
	}

	// Write score back to todo-service.
	_, err = c.todoClient.UpdateTaskScore(ctx, &todov1.UpdateTaskScoreRequest{
		UserId:            ev.UserID,
		TaskId:            ev.TaskID,
		CompletionScore:   int32(result.Score),
		CompletionInsight: result.Insight,
	})
	if err != nil {
		return fmt.Errorf("update task score %s: %w", ev.TaskID, err)
	}

	c.log.Info("task scored", "task_id", ev.TaskID, "score", result.Score, "insight", result.Insight)
	return nil
}

// buildHistory merges open + done task lists into a compact history slice.
// Deduplicates by task ID (done list takes precedence over open).
func buildHistory(open, done []*todov1.Task, now time.Time) []HistoryTask {
	seen := make(map[string]bool)
	var out []HistoryTask

	add := func(t *todov1.Task, statusStr string) {
		if seen[t.Id] {
			return
		}
		seen[t.Id] = true
		days := int(now.Sub(t.CreatedAt.AsTime()).Hours() / 24)
		out = append(out, HistoryTask{
			Title:       t.Title,
			Priority:    t.Priority.String(),
			Status:      statusStr,
			DaysElapsed: days,
		})
	}

	for _, t := range done {
		add(t, "done")
	}
	for _, t := range open {
		add(t, "open")
	}
	return out
}

// Backfill scores all existing tasks that don't yet have a completion score.
// Runs once at startup in a background goroutine. Tasks with a non-empty
// insight are considered already scored and are skipped.
func (c *Consumer) Backfill(ctx context.Context) {
	usersResp, err := c.authClient.ListUserIDs(ctx, &authv1.ListUserIDsRequest{})
	if err != nil {
		c.log.Error("scorer backfill: list users", "err", err)
		return
	}

	total, done := 0, 0
	for _, u := range usersResp.Users {
		userID := u.UserId
		var pageToken string
		for {
			resp, err := c.todoClient.ListTasks(ctx, &todov1.ListTasksRequest{
				UserId:    userID,
				PageSize:  50,
				PageToken: pageToken,
			})
			if err != nil {
				c.log.Error("scorer backfill: list tasks", "user_id", userID, "err", err)
				break
			}

			for _, t := range resp.Tasks {
				total++
				// Skip tasks that already have a score (insight is non-empty when scored).
				if t.CompletionInsight != "" {
					continue
				}

				ev := events.TaskEvent{UserID: userID, TaskID: t.Id}
				if err := c.scoreTask(ctx, ev); err != nil {
					c.log.Error("scorer backfill: score task", "task_id", t.Id, "err", err)
					continue
				}
				done++
				c.log.Info("scorer backfill: scored task", "task_id", t.Id, "progress", fmt.Sprintf("%d/%d", done, total))
			}

			if resp.NextPageToken == "" {
				break
			}
			pageToken = resp.NextPageToken
		}
	}

	c.log.Info("scorer backfill complete", "scored", done, "total", total)
}

// Runner wraps pkgkafka.Consumer for the scorer consumer group.
type Runner struct {
	consumer *pkgkafka.Consumer
	handler  *Consumer
}

func NewRunner(brokers []string, h *Consumer, log *slog.Logger) (*Runner, error) {
	c, err := pkgkafka.NewConsumer(brokers, "ai-scorer", []string{events.Topic}, log)
	if err != nil {
		return nil, err
	}
	return &Runner{consumer: c, handler: h}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	return r.consumer.Run(ctx, r.handler.Handle)
}
