// Package llm holds helpers shared by the Claude-calling packages.
package llm

import (
	"log/slog"

	"github.com/anthropics/anthropic-sdk-go"
)

// LogUsage records token counts; cache_* stay 0 when the prefix is below the model's minimum cacheable length.
func LogUsage(log *slog.Logger, call string, model anthropic.Model, u anthropic.Usage) {
	log.Info("claude usage",
		"call", call,
		"model", string(model),
		"input_tokens", u.InputTokens,
		"output_tokens", u.OutputTokens,
		"cache_creation_input_tokens", u.CacheCreationInputTokens,
		"cache_read_input_tokens", u.CacheReadInputTokens,
	)
}
