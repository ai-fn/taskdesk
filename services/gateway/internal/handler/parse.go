package handler

import (
	"encoding/json"
	"net/http"
	"time"

	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
)

// ParseHandler converts a natural language description into a structured task.
type ParseHandler struct {
	aiClient aiv1.AIServiceClient
}

func NewParseHandler(ai aiv1.AIServiceClient) *ParseHandler {
	return &ParseHandler{aiClient: ai}
}

type parseTaskRequest struct {
	Text      string   `json:"text"`
	ListNames []string `json:"list_names"`
}

// ParsedTaskResponse is the JSON shape returned to the frontend.
// Using plain Go types so we control the field names precisely.
type ParsedTaskResponse struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Priority    int32    `json:"priority"`
	DueAt       string   `json:"due_at,omitempty"` // RFC3339 string
	ListName    string   `json:"list_name,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Flagged     bool     `json:"flagged,omitempty"`
	Subtasks    []string `json:"subtasks,omitempty"`
}

// POST /api/v1/tasks/parse
func (h *ParseHandler) Parse(w http.ResponseWriter, r *http.Request) {
	var body parseTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.Text == "" {
		jsonError(w, "text is required", http.StatusBadRequest)
		return
	}

	userID := mw.UserIDFromContext(r.Context())

	resp, err := h.aiClient.ParseTask(r.Context(), &aiv1.ParseTaskRequest{
		UserId:    userID,
		Text:      body.Text,
		Today:     time.Now().UTC().Format("2006-01-02"),
		ListNames: body.ListNames,
		Language:  mw.LanguageFromContext(r.Context()),
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}

	out := ParsedTaskResponse{
		Title:       resp.Task.Title,
		Description: resp.Task.Description,
		Priority:    resp.Task.Priority,
		ListName:    resp.Task.ListName,
		Tags:        resp.Task.Tags,
		Flagged:     resp.Task.Flagged,
		Subtasks:    resp.Task.Subtasks,
	}
	if resp.Task.DueAt != nil {
		out.DueAt = resp.Task.DueAt.AsTime().Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}
