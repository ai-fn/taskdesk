package llm

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestLogUsage_ReportsCacheCounters(t *testing.T) {
	var buf bytes.Buffer
	LogUsage(slog.New(slog.NewJSONHandler(&buf, nil)), "plan", anthropic.Usage{
		InputTokens: 10, OutputTokens: 20, CacheCreationInputTokens: 30, CacheReadInputTokens: 40,
	})
	for _, want := range []string{`"call":"plan"`, `"cache_creation_input_tokens":30`, `"cache_read_input_tokens":40`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log %s missing %s", buf.String(), want)
		}
	}
}
