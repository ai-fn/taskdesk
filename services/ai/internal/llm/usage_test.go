package llm

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestLogUsage_ReportsCacheCounters(t *testing.T) {
	var buf bytes.Buffer
	LogUsage(slog.New(slog.NewJSONHandler(&buf, nil)), "plan", "claude-test", anthropic.Usage{
		InputTokens: 10, OutputTokens: 20, CacheCreationInputTokens: 30, CacheReadInputTokens: 40,
	})
	for _, want := range []string{`"call":"plan"`, `"model":"claude-test"`, `"cache_creation_input_tokens":30`, `"cache_read_input_tokens":40`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log %s missing %s", buf.String(), want)
		}
	}
}

// The streaming path reads usage from an accumulated message: input and cache counters arrive
// with message_start, output tokens with message_delta.
func TestAccumulatedStreamCarriesUsage(t *testing.T) {
	var acc anthropic.Message
	for _, raw := range []string{
		`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":11,"output_tokens":1,"cache_creation_input_tokens":22,"cache_read_input_tokens":33}}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":44}}`,
	} {
		var ev anthropic.MessageStreamEventUnion
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatal(err)
		}
		if err := acc.Accumulate(ev); err != nil {
			t.Fatal(err)
		}
	}
	u := acc.Usage
	if u.InputTokens != 11 || u.CacheCreationInputTokens != 22 || u.CacheReadInputTokens != 33 || u.OutputTokens != 44 {
		t.Errorf("usage = %+v", u)
	}
}
