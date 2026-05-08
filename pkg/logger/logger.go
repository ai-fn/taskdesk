// Package logger wraps log/slog with project-wide defaults.
//
// Every service constructs its logger via New(serviceName) so that every log
// line carries `service=<name>` and is JSON-encoded for ingestion into ELK
// (Phase 4). Per-request fields (request_id, user_id, …) should be added with
// (*slog.Logger).With at the handler boundary.
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey struct{}

// New returns a JSON slog.Logger tagged with the given service name.
// The level is read from LOG_LEVEL (debug|info|warn|error), defaulting to info.
func New(service string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(os.Getenv("LOG_LEVEL")),
	})
	return slog.New(h).With("service", service)
}

// FromContext returns the logger stored in ctx, or slog.Default() if none.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// Into returns a derived context carrying the given logger.
func Into(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
