// Package embedder calls the Voyage AI embeddings API to produce dense vectors.
//
// Voyage AI (voyageai.com) is Anthropic's recommended embedding provider.
// We use voyage-4-lite (1024 dimensions) — fast, free tier (200M tokens).
//
// API reference: POST https://api.voyageai.com/v1/embeddings
//
//	input_type: "document" when indexing, "query" when searching.
//	The distinction lets Voyage apply asymmetric search optimisation.
package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	voyageURL = "https://api.voyageai.com/v1/embeddings"
	model     = "voyage-4-lite" // 512 dimensions; fast, free tier (200M tokens)
)

type Embedder struct {
	apiKey string
	client *http.Client
}

func New(apiKey string) *Embedder {
	return &Embedder{
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// ── Voyage API request / response shapes ─────────────────────────────────────

type embedRequest struct {
	Input     []string `json:"input"`
	Model     string   `json:"model"`
	InputType string   `json:"input_type,omitempty"` // "document" | "query"
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed returns a 1024-dim float32 slice for the given text.
// inputType should be "document" when indexing a task, "query" when searching.
func (e *Embedder) Embed(ctx context.Context, text, inputType string) ([]float32, error) {
	reqBody, err := json.Marshal(embedRequest{
		Input:     []string{text},
		Model:     model,
		InputType: inputType,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, voyageURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyage request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("voyage API error %d: %s", resp.StatusCode, string(body))
	}

	var result embedResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding in response")
	}

	return result.Data[0].Embedding, nil
}
