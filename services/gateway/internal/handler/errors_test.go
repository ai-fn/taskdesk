package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHandleGRPCError(t *testing.T) {
	cases := []struct {
		code       codes.Code
		wantStatus int
	}{
		{codes.InvalidArgument, http.StatusBadRequest},
		{codes.AlreadyExists, http.StatusConflict},
		{codes.Unauthenticated, http.StatusUnauthorized},
		{codes.NotFound, http.StatusNotFound},
		{codes.Unavailable, http.StatusServiceUnavailable},
		{codes.Internal, http.StatusInternalServerError},
		{codes.Unknown, http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.code.String(), func(t *testing.T) {
			w := httptest.NewRecorder()
			handleGRPCError(w, status.Error(tc.code, "test message"))

			if w.Code != tc.wantStatus {
				t.Errorf("HTTP status = %d, want %d", w.Code, tc.wantStatus)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var body map[string]string
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("body is not JSON: %v", err)
			}
			if _, ok := body["error"]; !ok {
				t.Error(`body missing "error" key`)
			}
		})
	}
}

func TestHandleGRPCError_MessageStripped(t *testing.T) {
	// The gRPC error message should be passed through as-is (not the verbose
	// "rpc error: code = X desc = " prefix — that's a grpc.Status message).
	w := httptest.NewRecorder()
	handleGRPCError(w, status.Error(codes.NotFound, "session not found"))

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["error"] != "session not found" {
		t.Errorf(`body["error"] = %q, want "session not found"`, body["error"])
	}
}
