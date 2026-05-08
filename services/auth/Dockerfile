FROM golang:1.25-alpine AS builder
WORKDIR /src
# GOWORK=off: use each service's own go.mod + replace directives.
# GONOSUMDB=*: skip checksum DB (sum.golang.org may be unreachable on VPN).
# GODEBUG=netdns=cgo: use OS resolver instead of Go's pure-Go UDP DNS.
ENV GOWORK=off GONOSUMDB=* GODEBUG=netdns=cgo CGO_ENABLED=0 GOOS=linux
COPY pkg/ ./pkg/
COPY proto/gen/ ./proto/gen/
COPY services/auth/ ./services/auth/
WORKDIR /src/services/auth
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod tidy && go build -trimpath -ldflags="-s -w" -o /out/auth ./cmd/auth

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/auth /auth
USER 65532
EXPOSE 50051
ENTRYPOINT ["/auth"]
