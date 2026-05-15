// Package tracing provides trace ID generation and propagation across
// context and gRPC metadata boundaries.
package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"google.golang.org/grpc/metadata"
)

// MetadataKey is the gRPC metadata key used to carry the trace ID.
const MetadataKey = "x-trace-id"

type ctxKey struct{}

// New generates a random 16-character hex trace ID.
func New() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WithID stores id in ctx.
func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// IDFromContext returns the trace ID stored in ctx, or empty string.
func IDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKey{}).(string); ok {
		return id
	}
	return ""
}

// ExtractGRPC extracts the trace ID from incoming gRPC metadata.
// Returns ("", false) if not present.
func ExtractGRPC(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}
	vals := md.Get(MetadataKey)
	if len(vals) == 0 {
		return "", false
	}
	return vals[0], true
}

// InjectGRPC returns a context with the trace ID injected into outgoing
// gRPC metadata so downstream services can extract it.
func InjectGRPC(ctx context.Context, id string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, MetadataKey, id)
}
