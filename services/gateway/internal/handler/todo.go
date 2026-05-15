package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TodoHandler bridges HTTP ↔ todo-service gRPC.
type TodoHandler struct {
	client todov1.TodoServiceClient
}

func NewTodoHandler(c todov1.TodoServiceClient) *TodoHandler {
	return &TodoHandler{client: c}
}

// GET /api/v1/tasks
func (h *TodoHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	resp, err := h.client.ListTasks(r.Context(), &todov1.ListTasksRequest{
		UserId:    userID,
		PageToken: r.URL.Query().Get("page_token"),
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

// POST /api/v1/tasks
func (h *TodoHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	var body struct {
		Title       string    `json:"title"`
		Description string    `json:"description"`
		Priority    int32     `json:"priority"`
		Tags        []string  `json:"tags"`
		DueAt       *time.Time `json:"due_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Title == "" {
		jsonError(w, "title is required", http.StatusBadRequest)
		return
	}

	req := &todov1.CreateTaskRequest{
		UserId:      userID,
		Title:       body.Title,
		Description: body.Description,
		Priority:    todov1.Priority(body.Priority),
		Tags:        body.Tags,
	}
	if body.DueAt != nil {
		req.DueAt = timestamppb.New(*body.DueAt)
	}

	resp, err := h.client.CreateTask(r.Context(), req)
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoStatus(w, resp.Task, http.StatusCreated)
}

// GET /api/v1/tasks/{taskID}
func (h *TodoHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")

	resp, err := h.client.GetTask(r.Context(), &todov1.GetTaskRequest{
		UserId: userID,
		TaskId: taskID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.Task)
}

// PATCH /api/v1/tasks/{taskID}
func (h *TodoHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")

	var body struct {
		Title       string    `json:"title"`
		Description string    `json:"description"`
		Priority    int32     `json:"priority"`
		Status      int32     `json:"status"`
		Tags        []string  `json:"tags"`
		DueAt       *time.Time `json:"due_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	req := &todov1.UpdateTaskRequest{
		UserId:      userID,
		TaskId:      taskID,
		Title:       body.Title,
		Description: body.Description,
		Priority:    todov1.Priority(body.Priority),
		Status:      todov1.Status(body.Status),
		Tags:        body.Tags,
	}
	if body.DueAt != nil {
		req.DueAt = timestamppb.New(*body.DueAt)
	}

	resp, err := h.client.UpdateTask(r.Context(), req)
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.Task)
}

// DELETE /api/v1/tasks/{taskID}
func (h *TodoHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")

	_, err := h.client.DeleteTask(r.Context(), &todov1.DeleteTaskRequest{
		UserId: userID,
		TaskId: taskID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/tasks/{taskID}/complete
func (h *TodoHandler) Complete(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")

	resp, err := h.client.CompleteTask(r.Context(), &todov1.CompleteTaskRequest{
		UserId: userID,
		TaskId: taskID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.Task)
}
