package handler

import (
	"net/http"
	"strings"

	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
)

// SearchHandler handles semantic task search by embedding the query via
// ai-service and searching via todo-service's vector index.
type SearchHandler struct {
	aiClient   aiv1.AIServiceClient
	todoClient todov1.TodoServiceClient
}

func NewSearchHandler(ai aiv1.AIServiceClient, todo todov1.TodoServiceClient) *SearchHandler {
	return &SearchHandler{aiClient: ai, todoClient: todo}
}

// GET /api/v1/tasks/search?q=<query>
func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		jsonError(w, "q is required", http.StatusBadRequest)
		return
	}

	userID := mw.UserIDFromContext(r.Context())

	// Step 1: embed the search query using Voyage AI (via ai-service).
	embedResp, err := h.aiClient.EmbedText(r.Context(), &aiv1.EmbedTextRequest{
		Text:      query,
		InputType: "query",
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}

	// Step 2: nearest-neighbour search in the pgvector index.
	searchResp, err := h.todoClient.SearchTasks(r.Context(), &todov1.SearchTasksRequest{
		UserId:    userID,
		Embedding: embedResp.Embedding,
		Limit:     20,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}

	// Re-use the same protojson serialisation path as the regular task list.
	protoOK(w, searchResp)
}
