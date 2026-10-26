package embedder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(t *testing.T, status int, resp any, gotInputs *[]string) *Embedder {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if gotInputs != nil {
			*gotInputs = req.Input
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	e := New("key")
	e.url = srv.URL
	return e
}

func TestEmbedBatch_ReturnsVectorsInInputOrder(t *testing.T) {
	var inputs []string
	// Voyage may return data out of order; index says where each vector belongs.
	e := serve(t, 200, map[string]any{"data": []map[string]any{
		{"index": 1, "embedding": []float32{2}},
		{"index": 0, "embedding": []float32{1}},
	}}, &inputs)

	got, err := e.EmbedBatch(context.Background(), []string{"a", "b"}, "document")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || got[0][0] != 1 || got[1][0] != 2 {
		t.Errorf("inputs=%v vectors=%v, want one request, order restored by index", inputs, got)
	}
}

func TestEmbedBatch_Errors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		resp   any
	}{
		{"http error", 429, map[string]any{"detail": "rate limited"}},
		{"too few embeddings", 200, map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float32{1}}}}},
		{"empty embedding", 200, map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float32{}}, {"index": 1, "embedding": []float32{1}}}}},
		{"index out of range", 200, map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float32{1}}, {"index": 5, "embedding": []float32{2}}}}},
		{"negative index", 200, map[string]any{"data": []map[string]any{{"index": -1, "embedding": []float32{1}}, {"index": 1, "embedding": []float32{2}}}}},
		{"duplicate index", 200, map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float32{1}}, {"index": 0, "embedding": []float32{2}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := serve(t, tc.status, tc.resp, nil)
			if _, err := e.EmbedBatch(context.Background(), []string{"a", "b"}, "document"); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestEmbedBatch_RejectsBadBatchSizes(t *testing.T) {
	e := New("key")
	if _, err := e.EmbedBatch(context.Background(), nil, "document"); err == nil {
		t.Error("empty batch accepted")
	}
	if _, err := e.EmbedBatch(context.Background(), make([]string, MaxBatch+1), "document"); err == nil {
		t.Error("oversized batch accepted")
	}
}

func TestEmbed_SingleText(t *testing.T) {
	e := serve(t, 200, map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float32{7}}}}, nil)
	v, err := e.Embed(context.Background(), "x", "query")
	if err != nil || len(v) != 1 || v[0] != 7 {
		t.Errorf("Embed = %v, %v", v, err)
	}
}
