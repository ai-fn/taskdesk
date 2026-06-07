package server

import (
	"context"
	"time"

	"github.com/fsociety/todo/pkg/blobstore"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	uploadURLTTL   = 15 * time.Minute
	downloadURLTTL = 15 * time.Minute

	freeMaxFileBytes    int64 = 10 << 20          // 10 MiB
	freeMaxFilesPerTask       = 10
	freeUserQuotaBytes  int64 = 1 << 30           // 1 GiB
	proMaxFileBytes     int64 = 100 << 30         // 100 GiB soft cap
)

func (s *Server) CreateAttachmentUploadURL(ctx context.Context, req *todov1.CreateAttachmentUploadURLRequest) (*todov1.CreateAttachmentUploadURLResponse, error) {
	if req.UserId == "" || req.TaskId == "" || req.Filename == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id, task_id, and filename are required")
	}
	if req.SizeBytes <= 0 {
		return nil, status.Error(codes.InvalidArgument, "size_bytes must be positive")
	}

	// Enforce plan limits.
	if req.Plan == "free" {
		if req.SizeBytes > freeMaxFileBytes {
			return nil, status.Errorf(codes.ResourceExhausted, "file exceeds 10 MiB limit for free plan")
		}
		count, err := s.store.TaskCommittedAttachmentCount(ctx, req.TaskId)
		if err != nil && err != store.ErrNotFound {
			s.log.Error("attachment count check", "err", err)
			return nil, status.Error(codes.Internal, "failed to check attachment count")
		}
		if count >= freeMaxFilesPerTask {
			return nil, status.Errorf(codes.ResourceExhausted, "task attachment limit (%d) reached for free plan", freeMaxFilesPerTask)
		}
		used, err := s.store.UserStorageUsed(ctx, req.UserId)
		if err != nil {
			s.log.Error("user storage check", "err", err)
			return nil, status.Error(codes.Internal, "failed to check user storage")
		}
		if used+req.SizeBytes > freeUserQuotaBytes {
			return nil, status.Errorf(codes.ResourceExhausted, "storage quota (1 GiB) exceeded for free plan")
		}
	} else {
		// Pro: only the per-file soft cap applies.
		if req.SizeBytes > proMaxFileBytes {
			return nil, status.Errorf(codes.ResourceExhausted, "file exceeds 100 GiB soft cap")
		}
	}

	attID := uuid.New().String()
	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	objectKey := blobstore.Key(s.region, req.UserId, req.TaskId, attID, req.Filename)

	uploadURL, err := s.blob.PresignPut(ctx, objectKey, contentType, uploadURLTTL)
	if err != nil {
		s.log.Error("presign put", "err", err)
		return nil, status.Error(codes.Internal, "failed to generate upload URL")
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
	if err := s.store.CreateAttachmentPending(ctx, att); err != nil {
		s.log.Error("create attachment pending", "err", err)
		return nil, status.Error(codes.Internal, "failed to create attachment record")
	}

	return &todov1.CreateAttachmentUploadURLResponse{
		UploadUrl:    uploadURL,
		AttachmentId: attID,
	}, nil
}

func (s *Server) ConfirmAttachment(ctx context.Context, req *todov1.ConfirmAttachmentRequest) (*todov1.ConfirmAttachmentResponse, error) {
	if req.UserId == "" || req.AttachmentId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and attachment_id are required")
	}

	att, err := s.store.GetAttachment(ctx, req.UserId, req.AttachmentId)
	if err == store.ErrNotFound {
		return nil, status.Error(codes.NotFound, "attachment not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get attachment")
	}
	if att.Status == domain.AttachmentCommitted {
		return &todov1.ConfirmAttachmentResponse{Attachment: attachmentToProto(att)}, nil
	}

	// Verify the object actually landed in blob storage.
	storedSize, err := s.blob.Head(ctx, att.ObjectKey)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "object not found in storage — upload may have failed")
	}

	confirmed, err := s.store.ConfirmAttachment(ctx, req.UserId, req.AttachmentId, storedSize)
	if err == store.ErrNotFound {
		// Race: another request confirmed it first.
		confirmed, _ = s.store.GetAttachment(ctx, req.UserId, req.AttachmentId)
		if confirmed == nil {
			return nil, status.Error(codes.NotFound, "attachment not found")
		}
		return &todov1.ConfirmAttachmentResponse{Attachment: attachmentToProto(confirmed)}, nil
	}
	if err != nil {
		s.log.Error("confirm attachment", "err", err)
		return nil, status.Error(codes.Internal, "failed to confirm attachment")
	}

	return &todov1.ConfirmAttachmentResponse{Attachment: attachmentToProto(confirmed)}, nil
}

func (s *Server) ListAttachments(ctx context.Context, req *todov1.ListAttachmentsRequest) (*todov1.ListAttachmentsResponse, error) {
	if req.UserId == "" || req.TaskId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and task_id are required")
	}
	pageSize := int(req.PageSize)
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 50
	}

	atts, nextToken, err := s.store.ListAttachments(ctx, req.UserId, req.TaskId, pageSize, req.PageToken)
	if err != nil {
		s.log.Error("list attachments", "err", err)
		return nil, status.Error(codes.Internal, "failed to list attachments")
	}

	protos := make([]*todov1.Attachment, len(atts))
	for i, a := range atts {
		protos[i] = attachmentToProto(a)
	}
	return &todov1.ListAttachmentsResponse{
		Attachments:   protos,
		NextPageToken: nextToken,
	}, nil
}

func (s *Server) GetAttachmentDownloadURL(ctx context.Context, req *todov1.GetAttachmentDownloadURLRequest) (*todov1.GetAttachmentDownloadURLResponse, error) {
	if req.UserId == "" || req.AttachmentId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and attachment_id are required")
	}

	att, err := s.store.GetAttachment(ctx, req.UserId, req.AttachmentId)
	if err == store.ErrNotFound {
		return nil, status.Error(codes.NotFound, "attachment not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get attachment")
	}
	if att.Status != domain.AttachmentCommitted {
		return nil, status.Error(codes.FailedPrecondition, "attachment is not yet committed")
	}

	url, err := s.blob.PresignGet(ctx, att.ObjectKey, downloadURLTTL)
	if err != nil {
		s.log.Error("presign get", "err", err)
		return nil, status.Error(codes.Internal, "failed to generate download URL")
	}
	return &todov1.GetAttachmentDownloadURLResponse{DownloadUrl: url}, nil
}

func (s *Server) DeleteAttachment(ctx context.Context, req *todov1.DeleteAttachmentRequest) (*todov1.DeleteAttachmentResponse, error) {
	if req.UserId == "" || req.AttachmentId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and attachment_id are required")
	}

	att, err := s.store.DeleteAttachment(ctx, req.UserId, req.AttachmentId)
	if err == store.ErrNotFound {
		return nil, status.Error(codes.NotFound, "attachment not found")
	}
	if err != nil {
		s.log.Error("delete attachment", "err", err)
		return nil, status.Error(codes.Internal, "failed to delete attachment")
	}

	if err := s.blob.Delete(ctx, att.ObjectKey); err != nil {
		// Log but don't fail: the DB row is gone; reaper can clean orphaned blobs.
		s.log.Warn("delete blob after attachment row deleted", "key", att.ObjectKey, "err", err)
	}

	return &todov1.DeleteAttachmentResponse{}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func attachmentToProto(a *domain.Attachment) *todov1.Attachment {
	return &todov1.Attachment{
		Id:          a.ID,
		UserId:      a.UserID,
		TaskId:      a.TaskID,
		Filename:    a.Filename,
		ContentType: a.ContentType,
		SizeBytes:   a.SizeBytes,
		ObjectKey:   a.ObjectKey,
		Status:      a.Status,
		ScanStatus:  a.ScanStatus,
		CreatedAt:   timestamppb.New(a.CreatedAt),
	}
}
