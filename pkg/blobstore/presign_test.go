package blobstore

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
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
