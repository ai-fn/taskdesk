package blobstore

import (
	"strings"
	"testing"
)

func TestKey_SlugifiesFilename(t *testing.T) {
	got := Key("eu", "u_123", "task_9", "att_7", "report final.pdf")
	want := "eu/u_123/task_9/att_7/report-final.pdf"
	if got != want {
		t.Fatalf("Key() = %q, want %q", got, want)
	}
	if strings.Contains(got, " ") {
		t.Fatalf("Key() must not contain spaces: %q", got)
	}
	if got := Key("eu", "u", "t", "a", "a/b c.pdf"); got != "eu/u/t/a/a-b-c.pdf" {
		t.Fatalf("Key() slash/space slug = %q, want %q", got, "eu/u/t/a/a-b-c.pdf")
	}
}

// Compile-time proof that *S3Store satisfies Store (including the four new
// multipart methods). No offline presign test: PresignUploadPart requires a
// live S3-compatible endpoint; coverage lives in the todo-service integration
// tests that run against MinIO via testcontainers.
var _ Store = (*S3Store)(nil)
