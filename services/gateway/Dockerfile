FROM golang:1.25-alpine AS builder
WORKDIR /src
ENV GODEBUG=netdns=cgo CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=readonly
# Workspace build against the committed go.work.sum: nothing is resolved or re-tidied at build time.
COPY go.work go.work.sum ./
COPY pkg/ ./pkg/
COPY proto/gen/ ./proto/gen/
COPY services/gateway/ ./services/gateway/
RUN go work edit -dropuse=./services/ai -dropuse=./services/auth -dropuse=./services/notification -dropuse=./services/scheduler -dropuse=./services/todo
WORKDIR /src/services/gateway
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/gateway /gateway
USER 65532
EXPOSE 8080
ENTRYPOINT ["/gateway"]
