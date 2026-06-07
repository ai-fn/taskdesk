package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
)

// AttachmentHandler bridges HTTP ↔ todo-service attachment gRPC RPCs.
type AttachmentHandler struct {
	client todov1.TodoServiceClient
}

func NewAttachmentHandler(c todov1.TodoServiceClient) *AttachmentHandler {
	return &AttachmentHandler{client: c}
}

// POST /api/v1/tasks/{taskID}/attachments/upload-url
// Returns a presigned upload URL and attachment ID.
func (h *AttachmentHandler) CreateUploadURL(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")
	plan := mw.SubscriptionPlanFromContext(r.Context())

	var body struct {
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Filename == "" {
		jsonError(w, "filename is required", http.StatusBadRequest)
		return
	}
	if body.SizeBytes <= 0 {
		jsonError(w, "size_bytes must be positive", http.StatusBadRequest)
		return
	}

	resp, err := h.client.CreateAttachmentUploadURL(r.Context(), &todov1.CreateAttachmentUploadURLRequest{
		UserId:      userID,
		TaskId:      taskID,
		Filename:    body.Filename,
		ContentType: body.ContentType,
		SizeBytes:   body.SizeBytes,
		Plan:        plan,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoStatus(w, resp, http.StatusCreated)
}

// POST /api/v1/tasks/{taskID}/attachments/{attachmentID}/confirm
func (h *AttachmentHandler) Confirm(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	attID := chi.URLParam(r, "attachmentID")

	resp, err := h.client.ConfirmAttachment(r.Context(), &todov1.ConfirmAttachmentRequest{
		UserId:       userID,
		AttachmentId: attID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.Attachment)
}

// GET /api/v1/tasks/{taskID}/attachments
func (h *AttachmentHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")
	q := r.URL.Query()

	var pageSize int32
	fmt.Sscanf(q.Get("page_size"), "%d", &pageSize)

	resp, err := h.client.ListAttachments(r.Context(), &todov1.ListAttachmentsRequest{
		UserId:    userID,
		TaskId:    taskID,
		PageSize:  pageSize,
		PageToken: q.Get("page_token"),
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

// GET /api/v1/tasks/{taskID}/attachments/{attachmentID}/download-url
func (h *AttachmentHandler) GetDownloadURL(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	attID := chi.URLParam(r, "attachmentID")

	resp, err := h.client.GetAttachmentDownloadURL(r.Context(), &todov1.GetAttachmentDownloadURLRequest{
		UserId:       userID,
		AttachmentId: attID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

// DELETE /api/v1/tasks/{taskID}/attachments/{attachmentID}
func (h *AttachmentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	attID := chi.URLParam(r, "attachmentID")

	_, err := h.client.DeleteAttachment(r.Context(), &todov1.DeleteAttachmentRequest{
		UserId:       userID,
		AttachmentId: attID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
