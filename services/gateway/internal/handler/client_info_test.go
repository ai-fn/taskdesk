package handler

import (
	"net/http/httptest"
	"testing"
)

func TestClientInfoFromRequest_XRealIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Real-IP", "1.2.3.4")
	r.Header.Set("X-Forwarded-For", "9.9.9.9")
	r.Header.Set("User-Agent", "TestAgent/1.0")

	info := clientInfoFromRequest(r)

	if info.IpAddress != "1.2.3.4" {
		t.Errorf("IP = %q, want %q", info.IpAddress, "1.2.3.4")
	}
	if info.UserAgent != "TestAgent/1.0" {
		t.Errorf("UA = %q, want %q", info.UserAgent, "TestAgent/1.0")
	}
}

func TestClientInfoFromRequest_XForwardedFor(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	// No X-Real-IP; multi-hop XFF — only the first hop matters.
	r.Header.Set("X-Forwarded-For", "  5.6.7.8 , 10.0.0.1")

	info := clientInfoFromRequest(r)

	if info.IpAddress != "5.6.7.8" {
		t.Errorf("IP = %q, want %q (first XFF hop)", info.IpAddress, "5.6.7.8")
	}
}

func TestClientInfoFromRequest_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	// httptest sets RemoteAddr to "192.0.2.1:1234" by default.
	r.RemoteAddr = "7.8.9.10:4321"

	info := clientInfoFromRequest(r)

	if info.IpAddress != "7.8.9.10" {
		t.Errorf("IP = %q, want %q (from RemoteAddr)", info.IpAddress, "7.8.9.10")
	}
}

func TestClientInfoFromRequest_Precedence(t *testing.T) {
	// X-Real-IP must win over XFF and RemoteAddr.
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Real-IP", "11.22.33.44")
	r.Header.Set("X-Forwarded-For", "55.66.77.88")
	r.RemoteAddr = "99.0.0.1:80"

	info := clientInfoFromRequest(r)
	if info.IpAddress != "11.22.33.44" {
		t.Errorf("IP = %q, want X-Real-IP %q", info.IpAddress, "11.22.33.44")
	}
}
