// Package server implements the TodoService gRPC server.
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fsociety/todo/pkg/events"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	todov1.UnimplementedTodoServiceServer
	store *store.Store
	log   *slog.Logger
}

func New(st *store.Store, log *slog.Logger) *Server {
	return &Server{store: st, log: log}
}

// ── CreateTask ────────────────────────────────────────────────────────────────

func (s *Server) CreateTask(ctx context.Context, req *todov1.CreateTaskRequest) (*todov1.CreateTaskResponse, error) {
	if req.UserId == "" || req.Title == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and title are required")
	}

	now := time.Now().UTC()
	t := &domain.Task{
		ID:          uuid.New().String(),
		UserID:      req.UserId,
		Title:       req.Title,
		Description: req.Description,
		Priority:    domain.Priority(req.Priority),
		Status:      domain.StatusTodo,
		Tags:        req.Tags,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if req.DueAt != nil {
		ts := req.DueAt.AsTime()
		t.DueAt = &ts
	}

	if err := s.store.CreateTaskWithEvent(ctx, t); err != nil {
		s.log.Error("create task", "err", err)
		return nil, status.Error(codes.Internal, "create task failed")
	}

	return &todov1.CreateTaskResponse{Task: toProto(t)}, nil
}

// ── GetTask ───────────────────────────────────────────────────────────────────

func (s *Server) GetTask(ctx context.Context, req *todov1.GetTaskRequest) (*todov1.GetTaskResponse, error) {
	if req.UserId == "" || req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and task_id are required")
	}

	t, err := s.store.GetTask(ctx, req.UserId, req.TaskId)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		s.log.Error("get task", "err", err)
		return nil, status.Error(codes.Internal, "get task failed")
	}

	return &todov1.GetTaskResponse{Task: toProto(t)}, nil
}

// ── ListTasks ─────────────────────────────────────────────────────────────────

func (s *Server) ListTasks(ctx context.Context, req *todov1.ListTasksRequest) (*todov1.ListTasksResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	f := store.ListFilter{
		UserID:   req.UserId,
		PageSize: req.PageSize,
	}
	if req.Status != todov1.Status_STATUS_UNSPECIFIED {
		s := domain.Status(req.Status)
		f.Status = &s
	}
	if req.Priority != todov1.Priority_PRIORITY_UNSPECIFIED {
		p := domain.Priority(req.Priority)
		f.Priority = &p
	}
	if req.PageToken != "" {
		cur, err := decodeCursor(req.PageToken)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		f.CursorCreatedAt = &cur.CreatedAt
		f.CursorID = &cur.ID
	}

	tasks, err := s.store.ListTasks(ctx, f)
	if err != nil {
		s.log.Error("list tasks", "err", err)
		return nil, status.Error(codes.Internal, "list tasks failed")
	}

	resp := &todov1.ListTasksResponse{}
	for _, t := range tasks {
		resp.Tasks = append(resp.Tasks, toProto(t))
	}

	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 50
	}
	if len(tasks) == pageSize {
		last := tasks[len(tasks)-1]
		resp.NextPageToken = encodeCursor(last.CreatedAt, last.ID)
	}

	return resp, nil
}

// ── UpdateTask ────────────────────────────────────────────────────────────────

func (s *Server) UpdateTask(ctx context.Context, req *todov1.UpdateTaskRequest) (*todov1.UpdateTaskResponse, error) {
	if req.UserId == "" || req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and task_id are required")
	}

	existing, err := s.store.GetTask(ctx, req.UserId, req.TaskId)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Error(codes.Internal, "get task failed")
	}

	// Apply partial update: only non-zero fields change (unless update_mask says otherwise).
	if req.Title != "" {
		existing.Title = req.Title
	}
	if req.Description != "" {
		existing.Description = req.Description
	}
	if req.Priority != todov1.Priority_PRIORITY_UNSPECIFIED {
		existing.Priority = domain.Priority(req.Priority)
	}
	if req.Status != todov1.Status_STATUS_UNSPECIFIED {
		existing.Status = domain.Status(req.Status)
	}
	if len(req.Tags) > 0 {
		existing.Tags = req.Tags
	}
	if req.DueAt != nil {
		ts := req.DueAt.AsTime()
		existing.DueAt = &ts
	}
	existing.UpdatedAt = time.Now().UTC()

	evType := events.TypeTaskUpdated
	if existing.Status == domain.StatusDone {
		evType = events.TypeTaskCompleted
	}

	updated, err := s.store.UpdateTaskWithEvent(ctx, existing, evType)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		s.log.Error("update task", "err", err)
		return nil, status.Error(codes.Internal, "update task failed")
	}

	return &todov1.UpdateTaskResponse{Task: toProto(updated)}, nil
}

// ── DeleteTask ────────────────────────────────────────────────────────────────

func (s *Server) DeleteTask(ctx context.Context, req *todov1.DeleteTaskRequest) (*todov1.DeleteTaskResponse, error) {
	if req.UserId == "" || req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and task_id are required")
	}

	if err := s.store.DeleteTaskWithEvent(ctx, req.UserId, req.TaskId); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		s.log.Error("delete task", "err", err)
		return nil, status.Error(codes.Internal, "delete task failed")
	}

	return &todov1.DeleteTaskResponse{}, nil
}

// ── CompleteTask ──────────────────────────────────────────────────────────────

func (s *Server) CompleteTask(ctx context.Context, req *todov1.CompleteTaskRequest) (*todov1.CompleteTaskResponse, error) {
	resp, err := s.UpdateTask(ctx, &todov1.UpdateTaskRequest{
		UserId: req.UserId,
		TaskId: req.TaskId,
		Status: todov1.Status_STATUS_DONE,
	})
	if err != nil {
		return nil, err
	}
	return &todov1.CompleteTaskResponse{Task: resp.Task}, nil
}

// ── proto ↔ domain ────────────────────────────────────────────────────────────

func toProto(t *domain.Task) *todov1.Task {
	p := &todov1.Task{
		Id:          t.ID,
		UserId:      t.UserID,
		Title:       t.Title,
		Description: t.Description,
		Priority:    todov1.Priority(t.Priority),
		Status:      todov1.Status(t.Status),
		Tags:        t.Tags,
		CreatedAt:   timestamppb.New(t.CreatedAt),
		UpdatedAt:   timestamppb.New(t.UpdatedAt),
	}
	if t.DueAt != nil {
		p.DueAt = timestamppb.New(*t.DueAt)
	}
	return p
}

// ── cursor (keyset pagination) ────────────────────────────────────────────────

type cursor struct {
	CreatedAt time.Time `json:"c"`
	ID        string    `json:"i"`
}

func encodeCursor(createdAt time.Time, id string) string {
	b, _ := json.Marshal(cursor{CreatedAt: createdAt, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(token string) (cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return cursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return cursor{}, fmt.Errorf("unmarshal cursor: %w", err)
	}
	return c, nil
}
