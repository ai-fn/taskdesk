// Package server implements the AIService gRPC server.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
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
	store      *store.Store
	todoClient todov1.TodoServiceClient
	kafka      *pkgkafka.Producer
	log        *slog.Logger
}

func New(p *planner.Planner, st *store.Store, tc todov1.TodoServiceClient, kc *pkgkafka.Producer, log *slog.Logger) *Server {
	return &Server{planner: p, store: st, todoClient: tc, kafka: kc, log: log}
}

// GeneratePlan fetches the user's tasks, calls Claude, persists the plan, and
// publishes a Kafka event.
func (s *Server) GeneratePlan(ctx context.Context, req *aiv1.GeneratePlanRequest) (*aiv1.GeneratePlanResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Fetch tasks from todo-service.
	listResp, err := s.todoClient.ListTasks(ctx, &todov1.ListTasksRequest{
		UserId:   req.UserId,
		PageSize: 200,
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
	uc := planner.UserContext{
		UserID:            req.UserId,
		Today:             now.Format("2006-01-02"),
		OpenTasks:         tasksToInput(listResp.Tasks),
		RecentlyCompleted: tasksToInput(completedResp.Tasks),
	}

	plan, err := s.planner.Generate(ctx, uc)
	if err != nil {
		s.log.Error("generate plan", "user_id", req.UserId, "err", err)
		return nil, status.Error(codes.Internal, fmt.Sprintf("plan generation failed: %v", err))
	}

	// Build the proto response.
	planID := uuid.New().String()
	dp := &aiv1.DailyPlan{
		PlanId:        planID,
		UserId:        req.UserId,
		Summary:       plan.Summary,
		GeneratedAt:   timestamppb.New(now),
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
	if err := s.store.SavePlan(ctx, planID, req.UserId, plan.Summary, storeItems, now); err != nil {
		s.log.Error("save plan", "plan_id", planID, "err", err)
		// Don't fail — still publish the event.
	}

	// Publish to Kafka.
	if err := s.publish(ctx, planID, req.UserId, dp); err != nil {
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
		UserId:   req.UserId,
		PageSize: 200,
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
	uc := planner.UserContext{
		UserID:            req.UserId,
		Today:             now.Format("2006-01-02"),
		OpenTasks:         tasksToInput(listResp.Tasks),
		RecentlyCompleted: tasksToInput(completedResp.Tasks),
	}

	onText := func(delta string) {
		_ = stream.Send(&aiv1.PlanStreamChunk{
			Payload: &aiv1.PlanStreamChunk_TextDelta{TextDelta: delta},
		})
	}

	plan, err := s.planner.GenerateStream(ctx, uc, onText)
	if err != nil {
		s.log.Error("generate plan stream", "user_id", req.UserId, "err", err)
		_ = stream.Send(&aiv1.PlanStreamChunk{
			Payload: &aiv1.PlanStreamChunk_Error{Error: err.Error()},
		})
		return status.Error(codes.Internal, fmt.Sprintf("plan generation failed: %v", err))
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
	if err := s.store.SavePlan(ctx, planID, req.UserId, plan.Summary, storeItems, now); err != nil {
		s.log.Error("save plan (stream)", "plan_id", planID, "err", err)
	}

	if err := s.publish(ctx, planID, req.UserId, dp); err != nil {
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
	EventID   string           `json:"event_id"`
	UserID    string           `json:"user_id"`
	PlanID    string           `json:"plan_id"`
	Summary   string           `json:"summary"`
	Items     []planner.PlanItem `json:"items"`
	GeneratedAt time.Time      `json:"generated_at"`
}

func (s *Server) publish(ctx context.Context, planID, userID string, dp *aiv1.DailyPlan) error {
	items := make([]planner.PlanItem, len(dp.Items))
	for i, it := range dp.Items {
		items[i] = planner.PlanItem{
			TaskID:    it.TaskId,
			TaskTitle: it.TaskTitle,
			Rank:      int(it.Rank),
			Rationale: it.Rationale,
		}
	}
	ev := DailyPlanEvent{
		EventID:     uuid.New().String(),
		UserID:      userID,
		PlanID:      planID,
		Summary:     dp.Summary,
		Items:       items,
		GeneratedAt: dp.GeneratedAt.AsTime(),
	}
	return s.kafka.Publish(ctx, DailyPlanTopic, userID, ev)
}

// tasksToInput converts proto tasks to the planner input shape.
func tasksToInput(tasks []*todov1.Task) []planner.TaskInput {
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
			s := t.DueAt.AsTime().Format("2006-01-02")
			ti.DueAt = &s
		}
		out = append(out, ti)
	}
	return out
}
