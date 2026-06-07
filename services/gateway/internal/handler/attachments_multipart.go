package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
)

// POST /api/v1/tasks/{taskID}/attachments/multipart/initiate
func (h *AttachmentHandler) InitiateMultipart(w http.ResponseWriter, r *http.Request) {
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

	resp, err := h.client.InitiateMultipartUpload(r.Context(), &todov1.InitiateMultipartUploadRequest{
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

// GET /api/v1/tasks/{taskID}/attachments/{attachmentID}/parts/{partNumber}
func (h *AttachmentHandler) GetPartURL(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	attID := chi.URLParam(r, "attachmentID")

	var partNumber int32
	fmt.Sscanf(chi.URLParam(r, "partNumber"), "%d", &partNumber)
	if partNumber < 1 {
		jsonError(w, "partNumber must be a positive integer", http.StatusBadRequest)
		return
	}

	resp, err := h.client.GetUploadPartURL(r.Context(), &todov1.GetUploadPartURLRequest{
		UserId:       userID,
		AttachmentId: attID,
		PartNumber:   partNumber,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

// POST /api/v1/tasks/{taskID}/attachments/{attachmentID}/multipart/complete
func (h *AttachmentHandler) CompleteMultipart(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	attID := chi.URLParam(r, "attachmentID")

	var body struct {
		Parts []struct {
			PartNumber int32  `json:"part_number"`
			ETag       string `json:"etag"`
		} `json:"parts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if len(body.Parts) == 0 {
		jsonError(w, "parts must not be empty", http.StatusBadRequest)
		return
	}

	parts := make([]*todov1.CompletedPart, len(body.Parts))
	for i, p := range body.Parts {
		parts[i] = &todov1.CompletedPart{PartNumber: p.PartNumber, Etag: p.ETag}
	}

	resp, err := h.client.CompleteMultipartUpload(r.Context(), &todov1.CompleteMultipartUploadRequest{
		UserId:       userID,
		AttachmentId: attID,
		Parts:        parts,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.Attachment)
}
