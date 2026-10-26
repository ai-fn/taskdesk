package server

import (
	"context"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"

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

	freeMaxFileBytes    int64 = 10 << 20 // 10 MiB
	freeMaxFilesPerTask       = 10
	freeUserQuotaBytes  int64 = 1 << 30   // 1 GiB
	proMaxFileBytes     int64 = 100 << 30 // 100 GiB soft cap
	// Pro files of at least this size must use multipart; web/src/hooks/useAttachments.ts mirrors it.
	multipartThreshold int64 = 100 << 20

	planPro = "pro"

	maxFilenameBytes    = 255
	maxContentTypeBytes = 255
	// Unfinished uploads a user may hold at once, on any plan.
	maxPendingUploads = 100
)

// reserveError maps a ReserveAttachment failure to a gRPC status.
func (s *Server) reserveError(err error, op string) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, "task not found")
	case errors.Is(err, store.ErrQuotaExceeded):
		return status.Error(codes.ResourceExhausted, "storage quota (1 GiB) exceeded for free plan")
	case errors.Is(err, store.ErrTooManyPending):
		return status.Errorf(codes.ResourceExhausted, "too many unfinished uploads (%d); finish or cancel some first", maxPendingUploads)
	case errors.Is(err, store.ErrTaskAttachmentFull):
		return status.Errorf(codes.ResourceExhausted, "task attachment limit (%d) reached for free plan", freeMaxFilesPerTask)
	}
	s.log.Error(op, "err", err)
	return status.Error(codes.Internal, "failed to create attachment record")
}

// limitsFor returns the reservation limits for a plan; anything but "pro" gets the free limits.
func limitsFor(plan string) store.ReserveLimits {
	if plan == planPro {
		return store.ReserveLimits{MaxPending: maxPendingUploads}
	}
	return store.ReserveLimits{MaxFilesPerTask: freeMaxFilesPerTask, QuotaBytes: freeUserQuotaBytes, MaxPending: maxPendingUploads}
}

// validateUpload rejects names and types that are unreasonably long or contain control characters.
func validateUpload(filename, contentType string) error {
	if len(filename) > maxFilenameBytes || len(contentType) > maxContentTypeBytes {
		return status.Error(codes.InvalidArgument, "filename or content_type is too long")
	}
	if !utf8.ValidString(filename) || !utf8.ValidString(contentType) {
		return status.Error(codes.InvalidArgument, "filename and content_type must be valid UTF-8")
	}
	for _, r := range filename + contentType {
		// Rejects C0/C1 controls, line separators and bidi overrides (filename spoofing).
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return status.Error(codes.InvalidArgument, "filename and content_type must not contain control characters")
		}
	}
	return nil
}

func (s *Server) CreateAttachmentUploadURL(ctx context.Context, req *todov1.CreateAttachmentUploadURLRequest) (*todov1.CreateAttachmentUploadURLResponse, error) {
	if req.UserId == "" || req.TaskId == "" || req.Filename == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id, task_id, and filename are required")
	}
	if req.SizeBytes <= 0 {
		return nil, status.Error(codes.InvalidArgument, "size_bytes must be positive")
	}
	if err := validateUpload(req.Filename, req.ContentType); err != nil {
		return nil, err
	}

	if req.Plan != planPro && req.SizeBytes > freeMaxFileBytes {
		return nil, status.Errorf(codes.ResourceExhausted, "file exceeds 10 MiB limit for free plan")
	}
	if req.SizeBytes > proMaxFileBytes {
		return nil, status.Errorf(codes.ResourceExhausted, "file exceeds 100 GiB soft cap")
	}
	if req.SizeBytes >= multipartThreshold {
		return nil, status.Errorf(codes.InvalidArgument,
			"file is at or above the 100 MiB single-PUT limit; use the multipart upload flow")
	}

	attID := uuid.New().String()
	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	objectKey := blobstore.Key(s.region, req.UserId, req.TaskId, attID, req.Filename)

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
	if err := s.store.ReserveAttachment(ctx, att, "", limitsFor(req.Plan)); err != nil {
		return nil, s.reserveError(err, "reserve attachment")
	}

	uploadURL, err := s.blob.PresignPut(ctx, objectKey, contentType, req.SizeBytes, uploadURLTTL)
	if err != nil {
		s.log.Error("presign put", "err", err)
		// Drop the reservation now; otherwise it would count against the quota until the reaper runs.
		if _, derr := s.store.DeleteAttachment(context.WithoutCancel(ctx), req.UserId, attID); derr != nil {
			s.log.Warn("release reservation", "attachment_id", attID, "err", derr)
		}
		return nil, status.Error(codes.Internal, "failed to generate upload URL")
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
	if errors.Is(err, store.ErrNotFound) {
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

	confirmed, err := s.commit(ctx, att, storedSize, req.Language)
	if err != nil {
		return nil, err
	}
	return &todov1.ConfirmAttachmentResponse{Attachment: attachmentToProto(confirmed)}, nil
}

// commit flips a pending attachment to committed at the stored size. An object larger than the
// reserved size is discarded; losing a race to another confirm returns the committed row.
func (s *Server) commit(ctx context.Context, att *domain.Attachment, storedSize int64, language string) (*domain.Attachment, error) {
	confirmed, err := s.store.ConfirmAttachment(ctx, att.UserID, att.ID, storedSize, language)
	switch {
	case err == nil:
		return confirmed, nil
	case errors.Is(err, store.ErrSizeExceedsDeclared):
		if _, derr := s.store.DeleteAttachment(context.WithoutCancel(ctx), att.UserID, att.ID); derr != nil {
			s.log.Error("discard oversize upload", "attachment_id", att.ID, "err", derr)
		}
		return nil, status.Error(codes.FailedPrecondition, "uploaded file is larger than the size it was declared with")
	case errors.Is(err, store.ErrNotFound):
		if cur, gerr := s.store.GetAttachment(ctx, att.UserID, att.ID); gerr == nil && cur.Status == domain.AttachmentCommitted {
			return cur, nil
		}
		return nil, status.Error(codes.NotFound, "attachment not found")
	}
	s.log.Error("confirm attachment", "attachment_id", att.ID, "err", err)
	return nil, status.Error(codes.Internal, "failed to confirm attachment")
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
	if errors.Is(err, store.ErrInvalidCursor) {
		return nil, status.Error(codes.InvalidArgument, "invalid page_token")
	}
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

	// Fail closed: only a "clean" verdict permits download. Quarantined and
	// pending get specific messages; any unexpected value is treated as not-yet-scanned.
	if att.ScanStatus != domain.ScanClean {
		if att.ScanStatus == domain.ScanQuarantined {
			return nil, status.Error(codes.FailedPrecondition, "attachment failed malware scan and cannot be downloaded")
		}
		return nil, status.Error(codes.FailedPrecondition, "attachment is pending malware scan; try again shortly")
	}

	url, err := s.blob.PresignGet(ctx, att.ObjectKey, att.Filename, att.ContentType, downloadURLTTL)
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

	_, err := s.store.DeleteAttachment(ctx, req.UserId, req.AttachmentId)
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "attachment not found")
	}
	if err != nil {
		s.log.Error("delete attachment", "err", err)
		return nil, status.Error(codes.Internal, "failed to delete attachment")
	}

	// The blob is queued in the same tx as the row delete; the reaper removes it (and aborts a pending multipart).
	return &todov1.DeleteAttachmentResponse{}, nil
}

// UpdateScanStatus records the malware-scan verdict for an attachment. It is a
// trusted server-to-server callback from notification-service (over
// cluster-internal gRPC), not a user-facing action — so unlike the sibling
// attachment handlers it intentionally has no user_id ownership fence and keys
// solely on attachment_id. If this RPC is ever exposed beyond trusted internal
// callers, gate it with peer auth (mTLS) before relying on the absence here.
func (s *Server) UpdateScanStatus(ctx context.Context, req *todov1.UpdateScanStatusRequest) (*todov1.UpdateScanStatusResponse, error) {
	if req.AttachmentId == "" {
		return nil, status.Error(codes.InvalidArgument, "attachment_id is required")
	}
	if req.ScanStatus != domain.ScanClean && req.ScanStatus != domain.ScanQuarantined {
		return nil, status.Errorf(codes.InvalidArgument,
			"invalid scan_status %q; must be %q or %q",
			req.ScanStatus, domain.ScanClean, domain.ScanQuarantined)
	}
	if err := s.store.UpdateScanStatus(ctx, req.AttachmentId, req.ScanStatus); err == store.ErrNotFound {
		return nil, status.Error(codes.NotFound, "attachment not found")
	} else if err != nil {
		s.log.Error("update scan status", "attachment_id", req.AttachmentId, "err", err)
		return nil, status.Error(codes.Internal, "failed to update scan status")
	}
	return &todov1.UpdateScanStatusResponse{}, nil
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
