FROM golang:1.25-alpine AS builder
WORKDIR /src
ENV GOWORK=off GONOSUMDB=* GODEBUG=netdns=cgo CGO_ENABLED=0 GOOS=linux
COPY pkg/ ./pkg/
COPY proto/gen/ ./proto/gen/
COPY services/gateway/ ./services/gateway/
WORKDIR /src/services/gateway
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod tidy && go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/gateway /gateway
USER 65532
EXPOSE 8080
ENTRYPOINT ["/gateway"]
