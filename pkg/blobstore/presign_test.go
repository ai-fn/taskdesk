package blobstore

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/config"
)

func TestPresignPut_SignsContentLength(t *testing.T) {
	st := New(Config{Endpoint: "http://localhost:9000", Region: "auto", Bucket: "b", AccessKey: "k", SecretKey: "s"})
	raw, err := st.PresignPut(context.Background(), "eu/u/t/a/f.bin", "text/plain", 1234, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	signed := strings.Split(u.Query().Get("X-Amz-SignedHeaders"), ";")
	has := func(h string) bool {
		for _, s := range signed {
			if s == h {
				return true
			}
		}
		return false
	}
	if !has("content-length") || !has("content-type") {
		t.Errorf("signed headers = %v, want content-length and content-type", signed)
	}
}

func newTestStore(public string) *S3Store {
	return New(Config{Endpoint: "http://minio.internal:9000", PublicEndpoint: public, Region: "auto", Bucket: "b", AccessKey: "k", SecretKey: "s"})
}

func TestPresign_UsesPublicEndpoint(t *testing.T) {
	st := newTestStore("https://files.example.com")
	raw, err := st.PresignGet(context.Background(), "k", "a.pdf", "application/pdf", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := url.Parse(raw); u.Host != "files.example.com" {
		t.Errorf("download host = %q, want the public endpoint", u.Host)
	}
	raw, _ = newTestStore("").PresignPut(context.Background(), "k", "text/plain", 1, time.Minute)
	if u, _ := url.Parse(raw); u.Host != "minio.internal:9000" {
		t.Errorf("without a public endpoint the host = %q, want the internal one", u.Host)
	}
}

func TestPresignGet_ForcesAttachmentAndSafeType(t *testing.T) {
	st := newTestStore("")
	raw, err := st.PresignGet(context.Background(), "k", "evil.html", "text/html", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.Parse(raw)
	if got := q.Query().Get("response-content-disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("content-disposition = %q, want attachment", got)
	}
	if got := q.Query().Get("response-content-type"); got != "application/octet-stream" {
		t.Errorf("content-type = %q, want octet-stream for html", got)
	}
}

func TestSafeDownloadType(t *testing.T) {
	cases := map[string]string{
		"application/pdf":              "application/pdf",
		"image/png":                    "image/png",
		"text/plain; charset=utf-8":    "text/plain",
		"IMAGE/JPEG":                   "image/jpeg",
		"text/html":                    "application/octet-stream",
		"image/svg+xml":                "application/octet-stream",
		"application/xhtml+xml":        "application/octet-stream",
		"application/javascript":       "application/octet-stream",
		"":                             "application/octet-stream",
		"not a type":                   "application/octet-stream",
		"application/pdf\r\nX-Evil: 1": "application/octet-stream",
	}
	for in, want := range cases {
		if got := SafeDownloadType(in); got != want {
			t.Errorf("SafeDownloadType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContentDisposition(t *testing.T) {
	for _, name := range []string{"report.pdf", "任务.pdf", "a\r\nSet-Cookie: x=1", `we"ird;name.txt`, "\x00\x01", ""} {
		got := contentDisposition(name)
		if strings.ContainsAny(got, "\r\n") || !strings.HasPrefix(got, "attachment") {
			t.Errorf("contentDisposition(%q) = %q", name, got)
		}
		for _, r := range got {
			if r > 127 {
				t.Errorf("contentDisposition(%q) has raw non-ASCII: %q", name, got)
				break
			}
		}
	}
}

func TestConfig_CredentialsAreRequired(t *testing.T) {
	t.Setenv("BLOB_ACCESS_KEY", "")
	t.Setenv("BLOB_SECRET_KEY", "")
	if _, err := config.Load[Config](); err == nil {
		t.Fatal("Load succeeded without credentials; there must be no default minioadmin")
	}
	t.Setenv("BLOB_ACCESS_KEY", "a")
	t.Setenv("BLOB_SECRET_KEY", "b")
	if _, err := config.Load[Config](); err != nil {
		t.Fatalf("Load with credentials: %v", err)
	}
}
