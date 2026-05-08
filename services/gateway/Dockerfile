# syntax=docker/dockerfile:1.7

# Build stage --------------------------------------------------------------
FROM golang:1.23-alpine AS builder

WORKDIR /src

# Copy the workspace + the modules this service needs. Build context is the
# repo root so that pkg/ and services/gateway/ are both visible (see Tiltfile
# and scripts/kind-up.sh — `docker build` is invoked from the repo root).
COPY go.work ./
COPY pkg/ ./pkg/
COPY services/gateway/ ./services/gateway/

WORKDIR /src/services/gateway

ENV CGO_ENABLED=0 GOOS=linux
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

# Runtime stage ------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/gateway /gateway

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/gateway"]
