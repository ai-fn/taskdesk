package handler

import (
	"net/http"
	"strings"
	"unicode/utf8"

	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
)

// maxSearchRunes caps the query that gets embedded (Voyage bills per token).
const maxSearchRunes = 500

// SearchHandler handles task search.
// Pro users get semantic vector search (Voyage AI embedding + pgvector).
// Free users get fulltext/trigram search via Postgres pg_trgm.
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
	if utf8.RuneCountInString(query) > maxSearchRunes {
		jsonError(w, "q too long", http.StatusBadRequest)
		return
	}

	userID := mw.UserIDFromContext(r.Context())
	plan := mw.SubscriptionPlanFromContext(r.Context())

	var searchReq *todov1.SearchTasksRequest

	if plan == "pro" {
		embedResp, err := h.aiClient.EmbedText(r.Context(), &aiv1.EmbedTextRequest{
			Text:      query,
			InputType: "query",
		})
		if err != nil {
			handleGRPCError(w, err)
			return
		}
		searchReq = &todov1.SearchTasksRequest{
			UserId:    userID,
			Embedding: embedResp.Embedding,
			Limit:     20,
		}
	} else {
		searchReq = &todov1.SearchTasksRequest{
			UserId: userID,
			Query:  query,
			Limit:  20,
		}
	}

	searchResp, err := h.todoClient.SearchTasks(r.Context(), searchReq)
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	stripUnlessPro(r, searchResp.Tasks...)

	protoOK(w, searchResp)
}
