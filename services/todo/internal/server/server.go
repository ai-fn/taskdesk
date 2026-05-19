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
		Flagged:     req.Flagged,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if req.DueAt != nil {
		ts := req.DueAt.AsTime()
		t.DueAt = &ts
	}
	if req.ListId != "" {
		t.ListID = &req.ListId
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
		UserID:         req.UserId,
		PageSize:       req.PageSize,
		IncludeDeleted: req.IncludeDeleted,
		FlaggedOnly:    req.FlaggedOnly,
		DueTodayOnly:   req.DueTodayOnly,
		HasDueDate:     req.HasDueDate,
	}
	if req.Status != todov1.Status_STATUS_UNSPECIFIED {
		st := domain.Status(req.Status)
		f.Status = &st
	}
	if req.Priority != todov1.Priority_PRIORITY_UNSPECIFIED {
		p := domain.Priority(req.Priority)
		f.Priority = &p
	}
	if req.ListIdFilter != "" {
		f.ListIDFilter = &req.ListIdFilter
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

	inMask := func(field string) bool {
		for _, f := range req.UpdateMask {
			if f == field {
				return true
			}
		}
		return false
	}
	if inMask("flagged") {
		existing.Flagged = req.Flagged
	}
	if inMask("list_id") {
		if req.ListId == "" {
			existing.ListID = nil
		} else {
			existing.ListID = &req.ListId
		}
	} else if req.ListId != "" {
		existing.ListID = &req.ListId
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

// ── RestoreTask ───────────────────────────────────────────────────────────────

func (s *Server) RestoreTask(ctx context.Context, req *todov1.RestoreTaskRequest) (*todov1.RestoreTaskResponse, error) {
	if req.UserId == "" || req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and task_id are required")
	}
	t, err := s.store.RestoreTask(ctx, req.UserId, req.TaskId)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "task not found or not deleted")
		}
		s.log.Error("restore task", "err", err)
		return nil, status.Error(codes.Internal, "restore task failed")
	}
	return &todov1.RestoreTaskResponse{Task: toProto(t)}, nil
}

// ── PurgeTask ─────────────────────────────────────────────────────────────────

func (s *Server) PurgeTask(ctx context.Context, req *todov1.PurgeTaskRequest) (*todov1.PurgeTaskResponse, error) {
	if req.UserId == "" || req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and task_id are required")
	}
	if err := s.store.PurgeTask(ctx, req.UserId, req.TaskId); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "task not found or not deleted")
		}
		s.log.Error("purge task", "err", err)
		return nil, status.Error(codes.Internal, "purge task failed")
	}
	return &todov1.PurgeTaskResponse{}, nil
}

// ── CreateList ────────────────────────────────────────────────────────────────

func (s *Server) CreateList(ctx context.Context, req *todov1.CreateListRequest) (*todov1.CreateListResponse, error) {
	if req.UserId == "" || req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and name are required")
	}
	color := req.Color
	if color == "" {
		color = "#007AFF"
	}
	now := time.Now().UTC()
	l := &domain.List{
		ID:        uuid.New().String(),
		UserID:    req.UserId,
		Name:      req.Name,
		Color:     color,
		Icon:      req.Icon,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if req.GroupId != "" {
		l.GroupID = &req.GroupId
	}
	if err := s.store.CreateList(ctx, l); err != nil {
		s.log.Error("create list", "err", err)
		return nil, status.Error(codes.Internal, "create list failed")
	}
	return &todov1.CreateListResponse{List: toProtoList(l)}, nil
}

// ── ListLists ─────────────────────────────────────────────────────────────────

func (s *Server) ListLists(ctx context.Context, req *todov1.ListListsRequest) (*todov1.ListListsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	lists, err := s.store.ListLists(ctx, req.UserId)
	if err != nil {
		s.log.Error("list lists", "err", err)
		return nil, status.Error(codes.Internal, "list lists failed")
	}
	resp := &todov1.ListListsResponse{}
	for _, l := range lists {
		resp.Lists = append(resp.Lists, toProtoList(l))
	}
	return resp, nil
}

// ── UpdateList ────────────────────────────────────────────────────────────────

func (s *Server) UpdateList(ctx context.Context, req *todov1.UpdateListRequest) (*todov1.UpdateListResponse, error) {
	if req.UserId == "" || req.ListId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and list_id are required")
	}

	lists, err := s.store.ListLists(ctx, req.UserId)
	if err != nil {
		return nil, status.Error(codes.Internal, "get list failed")
	}
	var existing *domain.List
	for _, l := range lists {
		if l.ID == req.ListId {
			existing = l
			break
		}
	}
	if existing == nil {
		return nil, status.Error(codes.NotFound, "list not found")
	}

	inMask := func(field string) bool {
		for _, f := range req.UpdateMask {
			if f == field {
				return true
			}
		}
		return len(req.UpdateMask) == 0 // no mask = update all non-zero
	}
	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.Color != "" {
		existing.Color = req.Color
	}
	if inMask("is_hidden") {
		existing.IsHidden = req.IsHidden
	}
	if inMask("icon") || req.Icon != "" {
		existing.Icon = req.Icon
	}
	updateGroupID := inMask("group_id")
	if updateGroupID {
		if req.GroupId == "" {
			existing.GroupID = nil
		} else {
			existing.GroupID = &req.GroupId
		}
	}
	existing.UpdatedAt = time.Now().UTC()

	updated, err := s.store.UpdateList(ctx, existing, updateGroupID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "list not found")
		}
		s.log.Error("update list", "err", err)
		return nil, status.Error(codes.Internal, "update list failed")
	}
	updated.TaskCount = existing.TaskCount
	return &todov1.UpdateListResponse{List: toProtoList(updated)}, nil
}

// ── DeleteList ────────────────────────────────────────────────────────────────

func (s *Server) DeleteList(ctx context.Context, req *todov1.DeleteListRequest) (*todov1.DeleteListResponse, error) {
	if req.UserId == "" || req.ListId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and list_id are required")
	}
	if err := s.store.DeleteList(ctx, req.UserId, req.ListId); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "list not found")
		}
		s.log.Error("delete list", "err", err)
		return nil, status.Error(codes.Internal, "delete list failed")
	}
	return &todov1.DeleteListResponse{}, nil
}

// ── ReorderLists ──────────────────────────────────────────────────────────────

func (s *Server) ReorderLists(ctx context.Context, req *todov1.ReorderListsRequest) (*todov1.ReorderListsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if _, err := s.store.ReorderLists(ctx, req.UserId, req.ListIds); err != nil {
		s.log.Error("reorder lists", "err", err)
		return nil, status.Error(codes.Internal, "reorder lists failed")
	}
	// Return updated list order
	lists, err := s.store.ListLists(ctx, req.UserId)
	if err != nil {
		return nil, status.Error(codes.Internal, "fetch lists failed")
	}
	resp := &todov1.ReorderListsResponse{}
	for _, l := range lists {
		resp.Lists = append(resp.Lists, toProtoList(l))
	}
	return resp, nil
}

// ── CreateGroup ───────────────────────────────────────────────────────────────

func (s *Server) CreateGroup(ctx context.Context, req *todov1.CreateGroupRequest) (*todov1.CreateGroupResponse, error) {
	if req.UserId == "" || req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and name are required")
	}
	now := time.Now().UTC()
	g := &domain.ListGroup{
		ID:        uuid.New().String(),
		UserID:    req.UserId,
		Name:      req.Name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateGroup(ctx, g); err != nil {
		s.log.Error("create group", "err", err)
		return nil, status.Error(codes.Internal, "create group failed")
	}
	return &todov1.CreateGroupResponse{Group: toProtoGroup(g)}, nil
}

// ── ListGroups ────────────────────────────────────────────────────────────────

func (s *Server) ListGroups(ctx context.Context, req *todov1.ListGroupsRequest) (*todov1.ListGroupsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	groups, err := s.store.ListGroups(ctx, req.UserId)
	if err != nil {
		s.log.Error("list groups", "err", err)
		return nil, status.Error(codes.Internal, "list groups failed")
	}
	resp := &todov1.ListGroupsResponse{}
	for _, g := range groups {
		resp.Groups = append(resp.Groups, toProtoGroup(g))
	}
	return resp, nil
}

// ── UpdateGroup ───────────────────────────────────────────────────────────────

func (s *Server) UpdateGroup(ctx context.Context, req *todov1.UpdateGroupRequest) (*todov1.UpdateGroupResponse, error) {
	if req.UserId == "" || req.GroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and group_id are required")
	}
	now := time.Now().UTC()
	g := &domain.ListGroup{
		ID:        req.GroupId,
		UserID:    req.UserId,
		Name:      req.Name,
		UpdatedAt: now,
	}
	updated, err := s.store.UpdateGroup(ctx, g)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "group not found")
		}
		s.log.Error("update group", "err", err)
		return nil, status.Error(codes.Internal, "update group failed")
	}
	return &todov1.UpdateGroupResponse{Group: toProtoGroup(updated)}, nil
}

// ── DeleteGroup ───────────────────────────────────────────────────────────────

func (s *Server) DeleteGroup(ctx context.Context, req *todov1.DeleteGroupRequest) (*todov1.DeleteGroupResponse, error) {
	if req.UserId == "" || req.GroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and group_id are required")
	}
	if err := s.store.DeleteGroup(ctx, req.UserId, req.GroupId); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "group not found")
		}
		s.log.Error("delete group", "err", err)
		return nil, status.Error(codes.Internal, "delete group failed")
	}
	return &todov1.DeleteGroupResponse{}, nil
}

// ── ReorderGroups ─────────────────────────────────────────────────────────────

func (s *Server) ReorderGroups(ctx context.Context, req *todov1.ReorderGroupsRequest) (*todov1.ReorderGroupsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if _, err := s.store.ReorderGroups(ctx, req.UserId, req.GroupIds); err != nil {
		s.log.Error("reorder groups", "err", err)
		return nil, status.Error(codes.Internal, "reorder groups failed")
	}
	groups, err := s.store.ListGroups(ctx, req.UserId)
	if err != nil {
		return nil, status.Error(codes.Internal, "fetch groups failed")
	}
	resp := &todov1.ReorderGroupsResponse{}
	for _, g := range groups {
		resp.Groups = append(resp.Groups, toProtoGroup(g))
	}
	return resp, nil
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
		Flagged:     t.Flagged,
		CreatedAt:   timestamppb.New(t.CreatedAt),
		UpdatedAt:   timestamppb.New(t.UpdatedAt),
	}
	if t.DueAt != nil {
		p.DueAt = timestamppb.New(*t.DueAt)
	}
	if t.ListID != nil {
		p.ListId = *t.ListID
	}
	if t.DeletedAt != nil {
		p.DeletedAt = timestamppb.New(*t.DeletedAt)
	}
	return p
}

func toProtoList(l *domain.List) *todov1.TaskList {
	p := &todov1.TaskList{
		Id:        l.ID,
		UserId:    l.UserID,
		Name:      l.Name,
		Color:     l.Color,
		Icon:      l.Icon,
		Position:  int32(l.Position),
		IsHidden:  l.IsHidden,
		TaskCount: int32(l.TaskCount),
		CreatedAt: timestamppb.New(l.CreatedAt),
		UpdatedAt: timestamppb.New(l.UpdatedAt),
	}
	if l.GroupID != nil {
		p.GroupId = *l.GroupID
	}
	return p
}

func toProtoGroup(g *domain.ListGroup) *todov1.ListGroup {
	return &todov1.ListGroup{
		Id:        g.ID,
		UserId:    g.UserID,
		Name:      g.Name,
		Position:  int32(g.Position),
		CreatedAt: timestamppb.New(g.CreatedAt),
		UpdatedAt: timestamppb.New(g.UpdatedAt),
	}
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

// ── Embeddings / Semantic search ─────────────────────────────────────────────

func (s *Server) UpsertTaskEmbedding(ctx context.Context, req *todov1.UpsertTaskEmbeddingRequest) (*todov1.UpsertTaskEmbeddingResponse, error) {
	if req.UserId == "" || req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and task_id are required")
	}
	if err := s.store.UpsertTaskEmbedding(ctx, req.TaskId, req.UserId, req.Embedding); err != nil {
		s.log.Error("upsert embedding", "task_id", req.TaskId, "err", err)
		return nil, status.Error(codes.Internal, "upsert embedding failed")
	}
	return &todov1.UpsertTaskEmbeddingResponse{}, nil
}

func (s *Server) DeleteTaskEmbedding(ctx context.Context, req *todov1.DeleteTaskEmbeddingRequest) (*todov1.DeleteTaskEmbeddingResponse, error) {
	if req.UserId == "" || req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and task_id are required")
	}
	if err := s.store.DeleteTaskEmbedding(ctx, req.TaskId, req.UserId); err != nil {
		s.log.Error("delete embedding", "task_id", req.TaskId, "err", err)
		return nil, status.Error(codes.Internal, "delete embedding failed")
	}
	return &todov1.DeleteTaskEmbeddingResponse{}, nil
}

func (s *Server) SearchTasks(ctx context.Context, req *todov1.SearchTasksRequest) (*todov1.SearchTasksResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if len(req.Embedding) == 0 {
		return nil, status.Error(codes.InvalidArgument, "embedding is required")
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	tasks, err := s.store.SearchTasksByEmbedding(ctx, req.UserId, req.Embedding, limit)
	if err != nil {
		s.log.Error("search tasks", "user_id", req.UserId, "err", err)
		return nil, status.Error(codes.Internal, "search failed")
	}
	proto := make([]*todov1.Task, 0, len(tasks))
	for _, t := range tasks {
		proto = append(proto, toProto(t))
	}
	return &todov1.SearchTasksResponse{Tasks: proto}, nil
}
