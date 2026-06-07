package server

import (
	"testing"

	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInitiateMultipart_FreePlanRejected(t *testing.T) {
	s := &Server{}
	_, err := s.InitiateMultipartUpload(t.Context(), &todov1.InitiateMultipartUploadRequest{
		UserId: "u1", TaskId: "t1", Filename: "f.bin",
		SizeBytes: 150 << 20, Plan: "free",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}
}

func TestInitiateMultipart_BelowThresholdRejected(t *testing.T) {
	s := &Server{}
	_, err := s.InitiateMultipartUpload(t.Context(), &todov1.InitiateMultipartUploadRequest{
		UserId: "u1", TaskId: "t1", Filename: "f.bin",
		SizeBytes: 10 << 20, Plan: "pro", // 10 MiB < 100 MiB threshold
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for sub-threshold multipart, got %v", err)
	}
}

func TestCreateUploadURL_ExceedsThreshold_ProRejected(t *testing.T) {
	s := &Server{}
	_, err := s.CreateAttachmentUploadURL(t.Context(), &todov1.CreateAttachmentUploadURLRequest{
		UserId: "u1", TaskId: "t1", Filename: "f.bin",
		SizeBytes: 200 << 20, Plan: "pro", // 200 MiB > 100 MiB threshold
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for oversized single-PUT, got %v", err)
	}
}
