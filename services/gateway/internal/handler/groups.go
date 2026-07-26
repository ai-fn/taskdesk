package handler

import (
	"encoding/json"
	"net/http"

	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
)

// GroupHandler bridges HTTP ↔ todo-service group RPCs.
type GroupHandler struct {
	client todov1.TodoServiceClient
}

func NewGroupHandler(c todov1.TodoServiceClient) *GroupHandler {
	return &GroupHandler{client: c}
}

// GET /api/v1/groups
func (h *GroupHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	resp, err := h.client.ListGroups(r.Context(), &todov1.ListGroupsRequest{UserId: userID})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

// POST /api/v1/groups
func (h *GroupHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}

	resp, err := h.client.CreateGroup(r.Context(), &todov1.CreateGroupRequest{
		UserId: userID,
		Name:   body.Name,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoStatus(w, resp.Group, http.StatusCreated)
}

// PATCH /api/v1/groups/{groupID}
func (h *GroupHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	groupID := chi.URLParam(r, "groupID")

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	resp, err := h.client.UpdateGroup(r.Context(), &todov1.UpdateGroupRequest{
		UserId:  userID,
		GroupId: groupID,
		Name:    body.Name,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.Group)
}

// DELETE /api/v1/groups/{groupID}
func (h *GroupHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	groupID := chi.URLParam(r, "groupID")

	_, err := h.client.DeleteGroup(r.Context(), &todov1.DeleteGroupRequest{
		UserId:  userID,
		GroupId: groupID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/groups/reorder
func (h *GroupHandler) Reorder(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	var body struct {
		GroupIDs []string `json:"group_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	resp, err := h.client.ReorderGroups(r.Context(), &todov1.ReorderGroupsRequest{
		UserId:   userID,
		GroupIds: body.GroupIDs,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}
