package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
)

// S3 allows at most 10000 parts per multipart upload.
const maxMultipartParts = 10000

// POST /api/v1/tasks/{taskID}/attachments/multipart/initiate
func (h *AttachmentHandler) InitiateMultipart(w http.ResponseWriter, r *http.Request) {
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

	n, err := strconv.Atoi(chi.URLParam(r, "partNumber"))
	if err != nil || n < 1 || n > maxMultipartParts {
		jsonError(w, "partNumber must be between 1 and 10000", http.StatusBadRequest)
		return
	}
	partNumber := int32(n)

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
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if len(body.Parts) == 0 || len(body.Parts) > maxMultipartParts {
		jsonError(w, "parts must contain between 1 and 10000 entries", http.StatusBadRequest)
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
		Language:     mw.LanguageFromContext(r.Context()),
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp.Attachment)
}
