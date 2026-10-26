package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
	"golang.org/x/time/rate"
)

const (
	maxAttachmentBodyBytes = 1 << 20
	maxFilenameBytes       = 255
	maxContentTypeBytes    = 255
	maxListPageSize        = 100
)

// AttachmentHandler bridges HTTP ↔ todo-service attachment gRPC RPCs.
type AttachmentHandler struct {
	client todov1.TodoServiceClient
	// uploadLimiter bounds upload starts per user (per gateway pod); each one creates a DB row and a presigned URL.
	uploadLimiter *RateLimiter
}

func NewAttachmentHandler(c todov1.TodoServiceClient) *AttachmentHandler {
	return &AttachmentHandler{
		client:        c,
		uploadLimiter: NewRateLimiter(rate.Limit(0.5), 20, 15*time.Minute, 10*time.Minute),
	}
}

// Stop ends the limiter's cleanup goroutine.
func (h *AttachmentHandler) Stop() { h.uploadLimiter.Stop() }

// allowUpload answers 429 when the user starts uploads too fast.
func (h *AttachmentHandler) allowUpload(w http.ResponseWriter, userID string) bool {
	if h.uploadLimiter.Allow(userID) {
		return true
	}
	w.Header().Set("Retry-After", "2")
	jsonError(w, "too many uploads; slow down", http.StatusTooManyRequests)
	return false
}

type uploadBody struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

// decodeUploadBody reads and validates the start-of-upload request, writing the 400 itself on failure.
func decodeUploadBody(w http.ResponseWriter, r *http.Request) (uploadBody, bool) {
	var body uploadBody
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return body, false
	}
	switch {
	case body.Filename == "":
		jsonError(w, "filename is required", http.StatusBadRequest)
	case len(body.Filename) > maxFilenameBytes:
		jsonError(w, "filename is too long", http.StatusBadRequest)
	case len(body.ContentType) > maxContentTypeBytes:
		jsonError(w, "content_type is too long", http.StatusBadRequest)
	case body.SizeBytes <= 0:
		jsonError(w, "size_bytes must be positive", http.StatusBadRequest)
	default:
		return body, true
	}
	return body, false
}

// POST /api/v1/tasks/{taskID}/attachments/upload-url
// Returns a presigned upload URL and attachment ID.
func (h *AttachmentHandler) CreateUploadURL(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")
	plan := mw.SubscriptionPlanFromContext(r.Context())

	if !h.allowUpload(w, userID) {
		return
	}
	body, ok := decodeUploadBody(w, r)
	if !ok {
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
		Language:     mw.LanguageFromContext(r.Context()),
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
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxListPageSize {
			jsonError(w, "page_size must be between 1 and 100", http.StatusBadRequest)
			return
		}
		pageSize = int32(n)
	}

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
