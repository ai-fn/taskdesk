package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TodoHandler bridges HTTP ↔ todo-service gRPC.
type TodoHandler struct {
	client todov1.TodoServiceClient
}

func NewTodoHandler(c todov1.TodoServiceClient) *TodoHandler {
	return &TodoHandler{client: c}
}

// maxTaskPageSize is the documented cap on ?page_size.
const maxTaskPageSize = 200

// GET /api/v1/tasks
func (h *TodoHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	q := r.URL.Query()

	req := &todov1.ListTasksRequest{
		UserId:    userID,
		PageToken: q.Get("page_token"),
	}
	if n, err := strconv.Atoi(q.Get("page_size")); err == nil && n > 0 {
		req.PageSize = int32(min(n, maxTaskPageSize))
	}
	if s := q.Get("status"); s != "" {
		var n int32
		_, _ = fmt.Sscanf(s, "%d", &n) // invalid input leaves n at its zero value
		req.Status = todov1.Status(n)
	}
	if q.Get("list_id") != "" {
		req.ListIdFilter = q.Get("list_id")
	}
	if q.Get("flagged_only") == "true" {
		req.FlaggedOnly = true
	}
	if q.Get("include_deleted") == "true" {
		req.IncludeDeleted = true
	}
	if q.Get("due_today") == "true" {
		req.DueTodayOnly = true
	}
	if q.Get("has_due_date") == "true" {
		req.HasDueDate = true
	}
	if q.Get("parent_task_id") != "" {
		req.ParentTaskId = q.Get("parent_task_id")
	}
	if q.Get("exclude_blocked") == "true" {
		req.ExcludeBlocked = true
	}

	resp, err := h.client.ListTasks(r.Context(), req)
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	if mw.SubscriptionPlanFromContext(r.Context()) != "pro" {
		stripCompletionScore(resp.Tasks)
	}
	protoOK(w, resp)
}

// POST /api/v1/tasks
func (h *TodoHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	var body struct {
		Title        string     `json:"title"`
		Description  string     `json:"description"`
		Priority     int32      `json:"priority"`
		Tags         []string   `json:"tags"`
		DueAt        *time.Time `json:"due_at"`
		ListID       string     `json:"list_id"`
		Flagged      bool       `json:"flagged"`
		ParentTaskID string     `json:"parent_task_id"`
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
		UserId:       userID,
		Title:        body.Title,
		Description:  body.Description,
		Priority:     todov1.Priority(body.Priority),
		Tags:         body.Tags,
		ListId:       body.ListID,
		Flagged:      body.Flagged,
		ParentTaskId: body.ParentTaskID,
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
	if mw.SubscriptionPlanFromContext(r.Context()) != "pro" {
		stripCompletionScore([]*todov1.Task{resp.Task})
	}
	protoOK(w, resp.Task)
}

// PATCH /api/v1/tasks/{taskID}
func (h *TodoHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")

	var body struct {
		Title       string     `json:"title"`
		Description string     `json:"description"`
		Priority    int32      `json:"priority"`
		Status      int32      `json:"status"`
		Tags        []string   `json:"tags"`
		DueAt       *time.Time `json:"due_at"`
		ListID      *string    `json:"list_id"` // nil = don't change; "" = remove from list
		Flagged     *bool      `json:"flagged"` // nil = don't change
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
		Language:    mw.LanguageFromContext(r.Context()),
		Email:       mw.NotifyEmailFromContext(r.Context()),
	}
	if body.DueAt != nil {
		req.DueAt = timestamppb.New(*body.DueAt)
	}
	if body.Flagged != nil {
		req.Flagged = *body.Flagged
		req.UpdateMask = append(req.UpdateMask, "flagged")
	}
	if body.ListID != nil {
		req.ListId = *body.ListID
		req.UpdateMask = append(req.UpdateMask, "list_id")
	}

	resp, err := h.client.UpdateTask(r.Context(), req)
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.Task)
}

// DELETE /api/v1/tasks/{taskID}  (soft delete)
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
		UserId:   userID,
		TaskId:   taskID,
		Language: mw.LanguageFromContext(r.Context()),
		Email:    mw.NotifyEmailFromContext(r.Context()),
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.Task)
}

// POST /api/v1/tasks/{taskID}/restore
func (h *TodoHandler) Restore(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")

	resp, err := h.client.RestoreTask(r.Context(), &todov1.RestoreTaskRequest{
		UserId: userID,
		TaskId: taskID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.Task)
}

// DELETE /api/v1/tasks/{taskID}/purge  (permanent delete)
func (h *TodoHandler) Purge(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")

	_, err := h.client.PurgeTask(r.Context(), &todov1.PurgeTaskRequest{
		UserId: userID,
		TaskId: taskID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/tasks/{taskID}/dependencies   body: {depends_on_id}
func (h *TodoHandler) AddDependency(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")

	var body struct {
		DependsOnID string `json:"depends_on_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DependsOnID == "" {
		jsonError(w, "depends_on_id is required", http.StatusBadRequest)
		return
	}

	resp, err := h.client.AddDependency(r.Context(), &todov1.AddDependencyRequest{
		UserId:      userID,
		TaskId:      taskID,
		DependsOnId: body.DependsOnID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.Task)
}

// DELETE /api/v1/tasks/{taskID}/dependencies/{dependsOnID}
func (h *TodoHandler) RemoveDependency(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")
	dependsOnID := chi.URLParam(r, "dependsOnID")

	_, err := h.client.RemoveDependency(r.Context(), &todov1.RemoveDependencyRequest{
		UserId:      userID,
		TaskId:      taskID,
		DependsOnId: dependsOnID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// stripCompletionScore zeroes both score and insight fields for non-pro users.
// Neither value reaches the client — score=0 and insight="" regardless of
// what is stored in the DB.
func stripCompletionScore(tasks []*todov1.Task) {
	for _, t := range tasks {
		t.CompletionScore = 0
		t.CompletionInsight = ""
	}
}

// GET /api/v1/tasks/{taskID}/dependencies  → {depends_on: Task[], blocking: Task[]}
func (h *TodoHandler) ListDependencies(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")

	resp, err := h.client.ListDependencies(r.Context(), &todov1.ListDependenciesRequest{
		UserId: userID,
		TaskId: taskID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

// GET /api/v1/tasks/{taskID}/dependency-tree → {root, depends_on[], blocking[]}
// Each tree node has {task, depth, via_task_id}, transitively walked.
func (h *TodoHandler) GetDependencyTree(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")

	resp, err := h.client.GetDependencyTree(r.Context(), &todov1.GetDependencyTreeRequest{
		UserId: userID,
		TaskId: taskID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}
