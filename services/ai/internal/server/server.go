// Package server implements the AIService gRPC server.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	pki18n "github.com/fsociety/todo/pkg/i18n"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	"github.com/fsociety/todo/pkg/tz"
	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/ai/internal/embedder"
	"github.com/fsociety/todo/services/ai/internal/parser"
	"github.com/fsociety/todo/services/ai/internal/planner"
	"github.com/fsociety/todo/services/ai/internal/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DailyPlanTopic is the Kafka topic for generated plans.
const DailyPlanTopic = "daily-plan.generated.v1"

type Server struct {
	aiv1.UnimplementedAIServiceServer
	planner    *planner.Planner
	parser     *parser.Parser
	embedder   *embedder.Embedder
	store      *store.Store
	todoClient todov1.TodoServiceClient
	kafka      *pkgkafka.Producer
	log        *slog.Logger
}

func New(p *planner.Planner, pa *parser.Parser, emb *embedder.Embedder, st *store.Store, tc todov1.TodoServiceClient, kc *pkgkafka.Producer, log *slog.Logger) *Server {
	return &Server{planner: p, parser: pa, embedder: emb, store: st, todoClient: tc, kafka: kc, log: log}
}

// GeneratePlan fetches the user's tasks, calls Claude, persists the plan, and
// publishes a Kafka event.
func (s *Server) GeneratePlan(ctx context.Context, req *aiv1.GeneratePlanRequest) (*aiv1.GeneratePlanResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Fetch tasks from todo-service. Blocked tasks are skipped — they can't be
	// worked on until their dependencies clear, so they don't belong in today's plan.
	listResp, err := s.todoClient.ListTasks(ctx, &todov1.ListTasksRequest{
		UserId:         req.UserId,
		PageSize:       200,
		ExcludeBlocked: true,
	})
	if err != nil {
		s.log.Error("list tasks", "user_id", req.UserId, "err", err)
		return nil, status.Error(codes.Internal, "fetch tasks failed")
	}
	if len(listResp.Tasks) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "no tasks to prioritize")
	}

	// Fetch recently completed tasks (last 7 days) for context.
	completedResp, err := s.todoClient.ListTasks(ctx, &todov1.ListTasksRequest{
		UserId:   req.UserId,
		Status:   todov1.Status_STATUS_DONE,
		PageSize: 50,
	})
	if err != nil {
		// Non-fatal — plan generation can proceed without completion history.
		s.log.Warn("list completed tasks", "user_id", req.UserId, "err", err)
		completedResp = &todov1.ListTasksResponse{}
	}

	now := time.Now().UTC()
	loc := tz.Load(req.TimeZone)
	uc := planner.UserContext{
		UserID:            req.UserId,
		Today:             localDate(now, loc),
		OpenTasks:         tasksToInput(listResp.Tasks, loc),
		RecentlyCompleted: tasksToInput(completedResp.Tasks, loc),
	}

	lang := pki18n.Normalize(req.Language)
	plan, err := s.planner.Generate(ctx, uc, lang)
	if err != nil {
		s.log.Error("generate plan", "user_id", req.UserId, "err", err)
		return nil, status.Error(codes.Internal, "plan generation failed")
	}

	// Build the proto response.
	planID := uuid.New().String()
	dp := &aiv1.DailyPlan{
		PlanId:      planID,
		UserId:      req.UserId,
		Summary:     plan.Summary,
		GeneratedAt: timestamppb.New(now),
	}
	for _, item := range plan.Items {
		dp.Items = append(dp.Items, &aiv1.PlanItem{
			TaskId:    item.TaskID,
			TaskTitle: item.TaskTitle,
			Rank:      int32(item.Rank),
			Rationale: item.Rationale,
		})
		dp.OrderedTaskIds = append(dp.OrderedTaskIds, item.TaskID)
	}

	// Persist to Postgres.
	storeItems := make([]store.PlanItem, len(plan.Items))
	for i, it := range plan.Items {
		storeItems[i] = store.PlanItem{
			TaskID:    it.TaskID,
			TaskTitle: it.TaskTitle,
			Rank:      it.Rank,
			Rationale: it.Rationale,
		}
	}
	if err := s.store.SavePlan(ctx, planID, req.UserId, plan.Summary, storeItems, now, loc); err != nil {
		s.log.Error("save plan", "plan_id", planID, "err", err)
		// Don't fail — still publish the event.
	}

	// Publish to Kafka.
	if err := s.publish(ctx, planID, req.UserId, lang, req.Email, dp); err != nil {
		s.log.Error("publish plan event", "plan_id", planID, "err", err)
	}

	s.log.Info("plan generated", "user_id", req.UserId, "plan_id", planID, "tasks", len(dp.Items))
	return &aiv1.GeneratePlanResponse{Plan: dp}, nil
}

// GeneratePlanStream calls Claude with streaming, forwarding text deltas as
// PlanStreamChunk messages before sending the final plan chunk.
func (s *Server) GeneratePlanStream(req *aiv1.GeneratePlanRequest, stream aiv1.AIService_GeneratePlanStreamServer) error {
	if req.UserId == "" {
		return status.Error(codes.InvalidArgument, "user_id is required")
	}
	ctx := stream.Context()

	listResp, err := s.todoClient.ListTasks(ctx, &todov1.ListTasksRequest{
		UserId:         req.UserId,
		PageSize:       200,
		ExcludeBlocked: true,
	})
	if err != nil {
		s.log.Error("list tasks (stream)", "user_id", req.UserId, "err", err)
		return status.Error(codes.Internal, "fetch tasks failed")
	}
	if len(listResp.Tasks) == 0 {
		return status.Error(codes.FailedPrecondition, "no tasks to prioritize")
	}

	completedResp, err := s.todoClient.ListTasks(ctx, &todov1.ListTasksRequest{
		UserId:   req.UserId,
		Status:   todov1.Status_STATUS_DONE,
		PageSize: 50,
	})
	if err != nil {
		s.log.Warn("list completed tasks (stream)", "user_id", req.UserId, "err", err)
		completedResp = &todov1.ListTasksResponse{}
	}

	now := time.Now().UTC()
	loc := tz.Load(req.TimeZone)
	uc := planner.UserContext{
		UserID:            req.UserId,
		Today:             localDate(now, loc),
		OpenTasks:         tasksToInput(listResp.Tasks, loc),
		RecentlyCompleted: tasksToInput(completedResp.Tasks, loc),
	}

	onText := func(delta string) {
		_ = stream.Send(&aiv1.PlanStreamChunk{
			Payload: &aiv1.PlanStreamChunk_TextDelta{TextDelta: delta},
		})
	}

	lang := pki18n.Normalize(req.Language)
	plan, err := s.planner.GenerateStream(ctx, uc, lang, onText)
	if err != nil {
		s.log.Error("generate plan stream", "user_id", req.UserId, "err", err)
		_ = stream.Send(&aiv1.PlanStreamChunk{
			Payload: &aiv1.PlanStreamChunk_Error{Error: "plan generation failed"},
		})
		return status.Error(codes.Internal, "plan generation failed")
	}

	planID := uuid.New().String()
	dp := &aiv1.DailyPlan{
		PlanId:      planID,
		UserId:      req.UserId,
		Summary:     plan.Summary,
		GeneratedAt: timestamppb.New(now),
	}
	for _, item := range plan.Items {
		dp.Items = append(dp.Items, &aiv1.PlanItem{
			TaskId:    item.TaskID,
			TaskTitle: item.TaskTitle,
			Rank:      int32(item.Rank),
			Rationale: item.Rationale,
		})
		dp.OrderedTaskIds = append(dp.OrderedTaskIds, item.TaskID)
	}

	storeItems := make([]store.PlanItem, len(plan.Items))
	for i, it := range plan.Items {
		storeItems[i] = store.PlanItem{
			TaskID:    it.TaskID,
			TaskTitle: it.TaskTitle,
			Rank:      it.Rank,
			Rationale: it.Rationale,
		}
	}
	if err := s.store.SavePlan(ctx, planID, req.UserId, plan.Summary, storeItems, now, loc); err != nil {
		s.log.Error("save plan (stream)", "plan_id", planID, "err", err)
	}

	if err := s.publish(ctx, planID, req.UserId, lang, req.Email, dp); err != nil {
		s.log.Error("publish plan event (stream)", "plan_id", planID, "err", err)
	}

	s.log.Info("plan generated (stream)", "user_id", req.UserId, "plan_id", planID, "tasks", len(dp.Items))

	return stream.Send(&aiv1.PlanStreamChunk{
		Payload: &aiv1.PlanStreamChunk_Plan{Plan: dp},
	})
}

// GetLatestPlan retrieves the most recent plan for a user.
func (s *Server) GetLatestPlan(ctx context.Context, req *aiv1.GetLatestPlanRequest) (*aiv1.GetLatestPlanResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	p, err := s.store.GetLatestPlan(ctx, req.UserId)
	if err != nil {
		s.log.Error("get latest plan", "user_id", req.UserId, "err", err)
		return nil, status.Error(codes.Internal, "get plan failed")
	}
	if p == nil {
		return &aiv1.GetLatestPlanResponse{}, nil
	}

	dp := &aiv1.DailyPlan{
		PlanId:      p.PlanID,
		UserId:      p.UserID,
		Summary:     p.Summary,
		GeneratedAt: timestamppb.New(p.GeneratedAt),
	}
	var items []store.PlanItem
	if err := json.Unmarshal(p.ItemsJSON, &items); err == nil {
		for _, it := range items {
			dp.Items = append(dp.Items, &aiv1.PlanItem{
				TaskId:    it.TaskID,
				TaskTitle: it.TaskTitle,
				Rank:      int32(it.Rank),
				Rationale: it.Rationale,
			})
			dp.OrderedTaskIds = append(dp.OrderedTaskIds, it.TaskID)
		}
	}
	return &aiv1.GetLatestPlanResponse{Plan: dp}, nil
}

// DailyPlanEvent is the Kafka payload for daily-plan.generated.v1.
type DailyPlanEvent struct {
	EventID     string             `json:"event_id"`
	UserID      string             `json:"user_id"`
	PlanID      string             `json:"plan_id"`
	Summary     string             `json:"summary"`
	Items       []planner.PlanItem `json:"items"`
	GeneratedAt time.Time          `json:"generated_at"`
	Language    string             `json:"language"`
	Email       string             `json:"email,omitempty"` // verified address only
}

func (s *Server) publish(ctx context.Context, planID, userID, lang, email string, dp *aiv1.DailyPlan) error {
	return s.kafka.Publish(ctx, DailyPlanTopic, userID, newPlanEvent(planID, userID, lang, email, dp))
}

func newPlanEvent(planID, userID, lang, email string, dp *aiv1.DailyPlan) DailyPlanEvent {
	items := make([]planner.PlanItem, len(dp.Items))
	for i, it := range dp.Items {
		items[i] = planner.PlanItem{
			TaskID:    it.TaskId,
			TaskTitle: it.TaskTitle,
			Rank:      int(it.Rank),
			Rationale: it.Rationale,
		}
	}
	return DailyPlanEvent{
		EventID:     uuid.New().String(),
		UserID:      userID,
		PlanID:      planID,
		Summary:     dp.Summary,
		Items:       items,
		GeneratedAt: dp.GeneratedAt.AsTime(),
		Language:    lang,
		Email:       email,
	}
}

// localDate formats t as a calendar date in loc, the user's "today" for plans and parsing.
func localDate(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02") }

// tasksToInput converts proto tasks to the planner input shape; due dates are calendar dates in loc.
func tasksToInput(tasks []*todov1.Task, loc *time.Location) []planner.TaskInput {
	out := make([]planner.TaskInput, 0, len(tasks))
	for _, t := range tasks {
		ti := planner.TaskInput{
			ID:          t.Id,
			Title:       t.Title,
			Description: t.Description,
			Priority:    t.Priority.String(),
			Status:      t.Status.String(),
			Tags:        t.Tags,
		}
		if t.DueAt != nil {
			s := localDate(t.DueAt.AsTime(), loc)
			ti.DueAt = &s
		}
		out = append(out, ti)
	}
	return out
}

// ── ParseTask ─────────────────────────────────────────────────────────────────

// ParseTask extracts structured task fields from a natural language description.
func (s *Server) ParseTask(ctx context.Context, req *aiv1.ParseTaskRequest) (*aiv1.ParseTaskResponse, error) {
	if req.Text == "" {
		return nil, status.Error(codes.InvalidArgument, "text is required")
	}

	if os.Getenv("MOCK_PARSE") == "true" {
		return &aiv1.ParseTaskResponse{Task: &aiv1.ParsedTask{
			Title:       "Fix login redirect loop on OAuth callback",
			Description: "After OAuth sign-in the callback handler redirects to /login instead of /dashboard, causing an infinite loop. Affects all OAuth providers.",
			Priority:    3, // high
			Tags:        []string{"bug", "auth", "oauth"},
			Subtasks: []string{
				"Reproduce the loop locally with Google OAuth",
				"Trace the callback handler redirect logic",
				"Check session/cookie state after token exchange",
				"Write a regression test for the redirect path",
				"Deploy fix and verify in staging",
			},
		}}, nil
	}

	zone := tz.Normalize(req.TimeZone)
	today := req.Today
	if today == "" {
		today = localDate(time.Now(), tz.Load(zone))
	}

	result, err := s.parser.Parse(ctx, req.Text, today, zone, pki18n.Normalize(req.Language), req.ListNames)
	if err != nil {
		s.log.Error("parse task", "err", err)
		return nil, status.Error(codes.Internal, "parse failed")
	}

	pt := &aiv1.ParsedTask{
		Title:       result.Title,
		Description: result.Description,
		Priority:    result.PriorityInt(),
		ListName:    result.ListName,
		Tags:        result.Tags,
		Flagged:     result.Flagged,
		Subtasks:    result.Subtasks,
	}
	if t := result.DueAtTime(tz.Load(zone)); t != nil {
		pt.DueAt = timestamppb.New(*t)
	}

	s.log.Info("task parsed", "priority", result.Priority, "has_due", result.DueAt != "")
	return &aiv1.ParseTaskResponse{Task: pt}, nil
}

// ── EmbedText ─────────────────────────────────────────────────────────────────

// EmbedText returns a voyage-3-lite embedding for the provided text.
// Called by the gateway when embedding a search query.
func (s *Server) EmbedText(ctx context.Context, req *aiv1.EmbedTextRequest) (*aiv1.EmbedTextResponse, error) {
	if req.Text == "" {
		return nil, status.Error(codes.InvalidArgument, "text is required")
	}
	inputType := req.InputType
	if inputType == "" {
		inputType = "query"
	}
	vec, err := s.embedder.Embed(ctx, req.Text, inputType)
	if err != nil {
		s.log.Error("embed text", "err", err)
		return nil, status.Error(codes.Internal, "embedding failed")
	}
	return &aiv1.EmbedTextResponse{Embedding: vec}, nil
}
