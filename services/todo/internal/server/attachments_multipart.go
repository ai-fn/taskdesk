package server

import (
	"context"
	"errors"
	"time"

	"github.com/fsociety/todo/pkg/blobstore"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const partURLTTL time.Duration = 15 * time.Minute

func (s *Server) InitiateMultipartUpload(ctx context.Context, req *todov1.InitiateMultipartUploadRequest) (*todov1.InitiateMultipartUploadResponse, error) {
	if req.UserId == "" || req.TaskId == "" || req.Filename == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id, task_id, and filename are required")
	}
	if req.Plan != "pro" {
		return nil, status.Error(codes.PermissionDenied, "multipart upload is available on pro plan only")
	}
	if req.SizeBytes <= multipartThreshold {
		return nil, status.Errorf(codes.InvalidArgument,
			"file is below multipart threshold; use single-PUT upload instead")
	}
	if req.SizeBytes > proMaxFileBytes {
		return nil, status.Errorf(codes.ResourceExhausted, "file exceeds 100 GiB soft cap")
	}

	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	attID := uuid.New().String()
	objectKey := blobstore.Key(s.region, req.UserId, req.TaskId, attID, req.Filename)

	uploadID, err := s.blob.InitiateMultipart(ctx, objectKey, contentType)
	if err != nil {
		s.log.Error("initiate multipart", "err", err)
		return nil, status.Error(codes.Internal, "failed to initiate multipart upload")
	}

	att := &domain.Attachment{
		ID:          attID,
		UserID:      req.UserId,
		TaskID:      req.TaskId,
		Filename:    req.Filename,
		ContentType: contentType,
		SizeBytes:   req.SizeBytes,
		ObjectKey:   objectKey,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.store.CreateMultipartPending(ctx, att, uploadID); err != nil {
		_ = s.blob.AbortMultipart(ctx, objectKey, uploadID)
		s.log.Error("create multipart pending", "err", err)
		return nil, status.Error(codes.Internal, "failed to create attachment record")
	}

	return &todov1.InitiateMultipartUploadResponse{
		AttachmentId: attID,
		UploadId:     uploadID,
	}, nil
}

func (s *Server) GetUploadPartURL(ctx context.Context, req *todov1.GetUploadPartURLRequest) (*todov1.GetUploadPartURLResponse, error) {
	if req.UserId == "" || req.AttachmentId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and attachment_id are required")
	}
	if req.PartNumber < 1 {
		return nil, status.Error(codes.InvalidArgument, "part_number must be >= 1")
	}

	att, err := s.store.GetAttachment(ctx, req.UserId, req.AttachmentId)
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "attachment not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get attachment")
	}
	if att.Status != domain.AttachmentPending {
		return nil, status.Error(codes.FailedPrecondition, "attachment is no longer pending")
	}
	if att.UploadID == "" {
		return nil, status.Error(codes.FailedPrecondition, "attachment is not a multipart upload")
	}

	url, err := s.blob.PresignUploadPart(ctx, att.ObjectKey, att.UploadID, req.PartNumber, partURLTTL)
	if err != nil {
		s.log.Error("presign upload part", "err", err)
		return nil, status.Error(codes.Internal, "failed to generate part upload URL")
	}
	return &todov1.GetUploadPartURLResponse{UploadUrl: url}, nil
}

func (s *Server) CompleteMultipartUpload(ctx context.Context, req *todov1.CompleteMultipartUploadRequest) (*todov1.CompleteMultipartUploadResponse, error) {
	if req.UserId == "" || req.AttachmentId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and attachment_id are required")
	}
	if len(req.Parts) == 0 {
		return nil, status.Error(codes.InvalidArgument, "parts must not be empty")
	}

	att, err := s.store.GetAttachment(ctx, req.UserId, req.AttachmentId)
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "attachment not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get attachment")
	}
	if att.Status == domain.AttachmentCommitted {
		// Idempotent: already completed.
		return &todov1.CompleteMultipartUploadResponse{Attachment: attachmentToProto(att)}, nil
	}
	if att.UploadID == "" {
		return nil, status.Error(codes.FailedPrecondition, "not a multipart upload")
	}

	parts := make([]blobstore.CompletedPart, len(req.Parts))
	for i, p := range req.Parts {
		parts[i] = blobstore.CompletedPart{PartNumber: p.PartNumber, ETag: p.Etag}
	}
	if err := s.blob.CompleteMultipart(ctx, att.ObjectKey, att.UploadID, parts); err != nil {
		s.log.Error("complete multipart", "err", err)
		return nil, status.Error(codes.Internal, "failed to complete multipart upload in storage")
	}

	storedSize, err := s.blob.Head(ctx, att.ObjectKey)
	if err != nil {
		return nil, status.Error(codes.Internal, "HEAD after complete failed")
	}

	confirmed, err := s.store.ConfirmAttachment(ctx, req.UserId, req.AttachmentId, storedSize, req.Language)
	if errors.Is(err, store.ErrNotFound) {
		// Race: another request confirmed it first; re-fetch the committed row.
		if att, _ = s.store.GetAttachment(ctx, req.UserId, req.AttachmentId); att != nil {
			return &todov1.CompleteMultipartUploadResponse{Attachment: attachmentToProto(att)}, nil
		}
		return nil, status.Error(codes.NotFound, "attachment not found")
	}
	if err != nil {
		s.log.Error("confirm attachment after multipart", "err", err)
		return nil, status.Error(codes.Internal, "failed to confirm attachment")
	}
	return &todov1.CompleteMultipartUploadResponse{Attachment: attachmentToProto(confirmed)}, nil
}
