package server

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/fsociety/todo/pkg/blobstore"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	partURLTTL time.Duration = 15 * time.Minute
	// S3 allows at most 10000 parts per multipart upload.
	maxMultipartParts = 10000
)

func (s *Server) InitiateMultipartUpload(ctx context.Context, req *todov1.InitiateMultipartUploadRequest) (*todov1.InitiateMultipartUploadResponse, error) {
	if req.UserId == "" || req.TaskId == "" || req.Filename == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id, task_id, and filename are required")
	}
	if err := validateUpload(req.Filename, req.ContentType); err != nil {
		return nil, err
	}
	if req.Plan != planPro {
		return nil, status.Error(codes.PermissionDenied, "multipart upload is available on pro plan only")
	}
	if req.SizeBytes < multipartThreshold {
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
	if err := s.store.ReserveAttachment(ctx, att, uploadID, store.ReserveLimits{}); err != nil {
		if aerr := s.blob.AbortMultipart(context.WithoutCancel(ctx), objectKey, uploadID); aerr != nil {
			s.log.Warn("abort multipart after failed reserve", "key", objectKey, "err", aerr)
		}
		return nil, s.reserveError(err, "reserve multipart attachment")
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
	if req.PartNumber < 1 || req.PartNumber > maxMultipartParts {
		return nil, status.Errorf(codes.InvalidArgument, "part_number must be between 1 and %d", maxMultipartParts)
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
	if err := s.store.TouchAttachment(ctx, req.UserId, req.AttachmentId); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.FailedPrecondition, "attachment is no longer pending")
		}
		s.log.Warn("touch attachment", "attachment_id", req.AttachmentId, "err", err)
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

	parts, err := completedParts(req.Parts)
	if err != nil {
		return nil, err
	}
	if err := s.blob.CompleteMultipart(ctx, att.ObjectKey, att.UploadID, parts); err != nil {
		s.log.Error("complete multipart", "err", err)
		return nil, status.Error(codes.Internal, "failed to complete multipart upload in storage")
	}

	storedSize, err := s.blob.Head(ctx, att.ObjectKey)
	if err != nil {
		return nil, status.Error(codes.Internal, "HEAD after complete failed")
	}

	confirmed, err := s.commit(ctx, att, storedSize, req.Language)
	if err != nil {
		return nil, err
	}
	return &todov1.CompleteMultipartUploadResponse{Attachment: attachmentToProto(confirmed)}, nil
}

// completedParts validates the client's part list and returns it sorted by part number, as S3 requires.
func completedParts(in []*todov1.CompletedPart) ([]blobstore.CompletedPart, error) {
	if len(in) > maxMultipartParts {
		return nil, status.Errorf(codes.InvalidArgument, "at most %d parts", maxMultipartParts)
	}
	parts := make([]blobstore.CompletedPart, len(in))
	for i, p := range in {
		if p.PartNumber < 1 || p.PartNumber > maxMultipartParts {
			return nil, status.Errorf(codes.InvalidArgument, "part_number must be between 1 and %d", maxMultipartParts)
		}
		if p.Etag == "" {
			return nil, status.Errorf(codes.InvalidArgument, "part %d has no etag", p.PartNumber)
		}
		parts[i] = blobstore.CompletedPart{PartNumber: p.PartNumber, ETag: p.Etag}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })
	for i := 1; i < len(parts); i++ {
		if parts[i].PartNumber == parts[i-1].PartNumber {
			return nil, status.Errorf(codes.InvalidArgument, "duplicate part %d", parts[i].PartNumber)
		}
	}
	return parts, nil
}
