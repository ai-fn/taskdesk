package handler

import (
	"encoding/json"
	"io"
	"net/http"

	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
)

// ListHandler bridges HTTP ↔ todo-service list RPCs.
type ListHandler struct {
	client todov1.TodoServiceClient
}

func NewListHandler(c todov1.TodoServiceClient) *ListHandler {
	return &ListHandler{client: c}
}

// GET /api/v1/lists
func (h *ListHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	resp, err := h.client.ListLists(r.Context(), &todov1.ListListsRequest{UserId: userID})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

// POST /api/v1/lists
func (h *ListHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	var body struct {
		Name    string `json:"name"`
		Color   string `json:"color"`
		GroupID string `json:"group_id"`
		Icon    string `json:"icon"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}

	resp, err := h.client.CreateList(r.Context(), &todov1.CreateListRequest{
		UserId:  userID,
		Name:    body.Name,
		Color:   body.Color,
		GroupId: body.GroupID,
		Icon:    body.Icon,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoStatus(w, resp.List, http.StatusCreated)
}

// PATCH /api/v1/lists/{listID}
func (h *ListHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	listID := listIDParam(r)

	var body struct {
		Name     string  `json:"name"`
		Color    string  `json:"color"`
		IsHidden *bool   `json:"is_hidden"`
		GroupID  *string `json:"group_id"`
		Icon     *string `json:"icon"`
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil || json.Unmarshal(raw, &body) != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	// The SPA ungroups with an explicit group_id:null, which *string can't tell from absent.
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(raw, &keys)
	_, hasGroup := keys["group_id"]

	req := &todov1.UpdateListRequest{
		UserId: userID,
		ListId: listID,
		Name:   body.Name,
		Color:  body.Color,
	}
	if body.Icon != nil {
		req.Icon = *body.Icon
		req.UpdateMask = append(req.UpdateMask, "icon")
	}
	if body.IsHidden != nil {
		req.IsHidden = *body.IsHidden
		req.UpdateMask = append(req.UpdateMask, "is_hidden")
	}
	if hasGroup {
		if body.GroupID != nil {
			req.GroupId = *body.GroupID
		}
		req.UpdateMask = append(req.UpdateMask, "group_id")
	}

	resp, err := h.client.UpdateList(r.Context(), req)
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.List)
}

// DELETE /api/v1/lists/{listID}
func (h *ListHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	listID := listIDParam(r)

	_, err := h.client.DeleteList(r.Context(), &todov1.DeleteListRequest{
		UserId: userID,
		ListId: listID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/lists/reorder
func (h *ListHandler) Reorder(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	var body struct {
		ListIDs []string `json:"list_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	resp, err := h.client.ReorderLists(r.Context(), &todov1.ReorderListsRequest{
		UserId:  userID,
		ListIds: body.ListIDs,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

func listIDParam(r *http.Request) string {
	return chi.URLParam(r, "listID")
}
