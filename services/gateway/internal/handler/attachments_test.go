package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

// attClient embeds the interface so only the RPCs a test configures can be called.
type attClient struct {
	todov1.TodoServiceClient
	createUpload func(*todov1.CreateAttachmentUploadURLRequest) (*todov1.CreateAttachmentUploadURLResponse, error)
	partURL      func(*todov1.GetUploadPartURLRequest) (*todov1.GetUploadPartURLResponse, error)
	list         func(*todov1.ListAttachmentsRequest) (*todov1.ListAttachmentsResponse, error)
	calls        int
}

func (c *attClient) CreateAttachmentUploadURL(_ context.Context, in *todov1.CreateAttachmentUploadURLRequest, _ ...grpc.CallOption) (*todov1.CreateAttachmentUploadURLResponse, error) {
	c.calls++
	return c.createUpload(in)
}
func (c *attClient) GetUploadPartURL(_ context.Context, in *todov1.GetUploadPartURLRequest, _ ...grpc.CallOption) (*todov1.GetUploadPartURLResponse, error) {
	c.calls++
	return c.partURL(in)
}
func (c *attClient) ListAttachments(_ context.Context, in *todov1.ListAttachmentsRequest, _ ...grpc.CallOption) (*todov1.ListAttachmentsResponse, error) {
	c.calls++
	return c.list(in)
}

func buildAttRouter(plan string, c *attClient) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Authenticate(authVerifier(validTokenWithPlan(plan))))
	h := handler.NewAttachmentHandler(c)
	r.Post("/api/v1/tasks/{taskID}/attachments/upload-url", h.CreateUploadURL)
	r.Get("/api/v1/tasks/{taskID}/attachments", h.List)
	r.Get("/api/v1/tasks/{taskID}/attachments/{attachmentID}/parts/{partNumber}", h.GetPartURL)
	r.Post("/api/v1/tasks/{taskID}/attachments/{attachmentID}/multipart/complete", h.CompleteMultipart)
	return r
}

func attDo(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", u1Token())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCreateUploadURL_ForwardsPlanAndValidates(t *testing.T) {
	var got *todov1.CreateAttachmentUploadURLRequest
	c := &attClient{createUpload: func(in *todov1.CreateAttachmentUploadURLRequest) (*todov1.CreateAttachmentUploadURLResponse, error) {
		got = in
		return &todov1.CreateAttachmentUploadURLResponse{AttachmentId: "a1"}, nil
	}}
	r := buildAttRouter("pro", c)

	if rec := attDo(r, "POST", "/api/v1/tasks/t1/attachments/upload-url", `{"filename":"a.txt","size_bytes":5}`); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	if got.Plan != "pro" || got.TaskId != "t1" || got.UserId == "" {
		t.Errorf("forwarded %+v", got)
	}

	c.calls = 0
	for name, body := range map[string]string{
		"long name":   `{"filename":"` + strings.Repeat("a", 256) + `","size_bytes":5}`,
		"long type":   `{"filename":"a","content_type":"` + strings.Repeat("a", 256) + `","size_bytes":5}`,
		"zero size":   `{"filename":"a","size_bytes":0}`,
		"no filename": `{"size_bytes":5}`,
		"not json":    `nope`,
		"huge body":   `{"filename":"a","size_bytes":5,"pad":"` + strings.Repeat("x", 2<<20) + `"}`,
	} {
		if rec := attDo(r, "POST", "/api/v1/tasks/t1/attachments/upload-url", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
	if c.calls != 0 {
		t.Errorf("invalid requests reached todo-service %d times", c.calls)
	}
}

func TestCreateUploadURL_RateLimitedPerUser(t *testing.T) {
	c := &attClient{createUpload: func(*todov1.CreateAttachmentUploadURLRequest) (*todov1.CreateAttachmentUploadURLResponse, error) {
		return &todov1.CreateAttachmentUploadURLResponse{}, nil
	}}
	r := buildAttRouter("free", c)
	var limited *httptest.ResponseRecorder
	for range 40 {
		if rec := attDo(r, "POST", "/api/v1/tasks/t1/attachments/upload-url", `{"filename":"a","size_bytes":1}`); rec.Code == http.StatusTooManyRequests {
			limited = rec
			break
		}
	}
	if limited == nil {
		t.Fatal("40 rapid upload starts were never rate limited")
	}
	if limited.Header().Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}
}

func TestGetPartURL_PartNumberRange(t *testing.T) {
	var got int32
	c := &attClient{partURL: func(in *todov1.GetUploadPartURLRequest) (*todov1.GetUploadPartURLResponse, error) {
		got = in.PartNumber
		return &todov1.GetUploadPartURLResponse{UploadUrl: "u"}, nil
	}}
	r := buildAttRouter("pro", c)
	for _, n := range []string{"abc", "0", "-1", "10001", "99999999999", "1.5", ""} {
		if rec := attDo(r, "GET", "/api/v1/tasks/t1/attachments/a1/parts/"+n, ""); rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
			t.Errorf("part %q: status = %d, want 400", n, rec.Code)
		}
	}
	if c.calls != 0 {
		t.Fatalf("bad part numbers reached todo-service")
	}
	for _, n := range []int{1, 10000} {
		if rec := attDo(r, "GET", "/api/v1/tasks/t1/attachments/a1/parts/"+strconv.Itoa(n), ""); rec.Code != http.StatusOK || got != int32(n) {
			t.Errorf("part %d: status %d, forwarded %d", n, rec.Code, got)
		}
	}
}

func TestListAttachments_PageSizeValidated(t *testing.T) {
	var got int32
	c := &attClient{list: func(in *todov1.ListAttachmentsRequest) (*todov1.ListAttachmentsResponse, error) {
		got = in.PageSize
		return &todov1.ListAttachmentsResponse{}, nil
	}}
	r := buildAttRouter("free", c)
	for _, q := range []string{"abc", "0", "-3", "101", "1e9"} {
		if rec := attDo(r, "GET", "/api/v1/tasks/t1/attachments?page_size="+q, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("page_size=%s: status = %d, want 400", q, rec.Code)
		}
	}
	if rec := attDo(r, "GET", "/api/v1/tasks/t1/attachments?page_size=25", ""); rec.Code != http.StatusOK || got != 25 {
		t.Errorf("page_size=25: status %d, forwarded %d", rec.Code, got)
	}
	if rec := attDo(r, "GET", "/api/v1/tasks/t1/attachments", ""); rec.Code != http.StatusOK {
		t.Errorf("no page_size: status = %d", rec.Code)
	}
}

func TestCompleteMultipart_RejectsOversizedPartList(t *testing.T) {
	r := buildAttRouter("pro", &attClient{})
	parts := strings.Repeat(`{"part_number":1,"etag":"e"},`, 10001)
	body := `{"parts":[` + strings.TrimSuffix(parts, ",") + `]}`
	if rec := attDo(r, "POST", "/api/v1/tasks/t1/attachments/a1/multipart/complete", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
