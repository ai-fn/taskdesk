package server

import (
	"context"
	"testing"

	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUpdateScanStatus_MissingID(t *testing.T) {
	s := &Server{}
	_, err := s.UpdateScanStatus(context.Background(), &todov1.UpdateScanStatusRequest{})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %s: %v", code, err)
	}
}

func TestUpdateScanStatus_InvalidStatus(t *testing.T) {
	s := &Server{}
	_, err := s.UpdateScanStatus(context.Background(), &todov1.UpdateScanStatusRequest{
		AttachmentId: "att-1",
		ScanStatus:   "infected", // not a valid value
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %s: %v", code, err)
	}
}

func TestUpdateScanStatus_ValidInputs_HitNilStore(t *testing.T) {
	s := &Server{}
	// Valid inputs pass validation and then panic on nil store — we only test
	// the validation gate here; integration coverage lives in store tests.
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil store, got none")
		}
	}()
	_, _ = s.UpdateScanStatus(context.Background(), &todov1.UpdateScanStatusRequest{
		AttachmentId: "att-1",
		ScanStatus:   "clean",
	})
}
