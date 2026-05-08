#!/usr/bin/env bash
# Generate Go code from all .proto files.
#
# Installs protoc + plugins into ~/.local/bin on first run (no sudo needed).
# Re-running is safe — it skips installs if versions already match.
#
# Output lands in proto/gen/ which is .gitignored; check it in when you want
# reproducible builds without requiring contributors to install protoc.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$HOME/.local/bin"
mkdir -p "$BIN"

PROTOC_VERSION="27.2"
GO_PROTO_VERSION="v1.34.2"
GO_GRPC_VERSION="v1.4.0"

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
step() { printf '  → %s\n' "$*"; }

# ── 1. protoc ────────────────────────────────────────────────────────────────
install_protoc() {
  local arch
  arch="$(uname -m)"
  case "$arch" in
    x86_64)  arch="x86_64"  ;;
    aarch64) arch="aarch_64" ;;
    arm64)   arch="aarch_64" ;;
    *) echo "Unsupported arch: $arch"; exit 1 ;;
  esac

  local url="https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-${arch}.zip"
  local tmp; tmp="$(mktemp -d)"
  step "downloading protoc ${PROTOC_VERSION}"
  curl -sSL "$url" -o "$tmp/protoc.zip"
  unzip -q "$tmp/protoc.zip" -d "$tmp/protoc"
  cp "$tmp/protoc/bin/protoc" "$BIN/protoc"
  chmod +x "$BIN/protoc"
  # Well-known types land in ~/.local/include
  mkdir -p "$HOME/.local/include"
  cp -r "$tmp/protoc/include/." "$HOME/.local/include/"
  rm -rf "$tmp"
}

if ! command -v protoc &>/dev/null || ! protoc --version 2>/dev/null | grep -q "$PROTOC_VERSION"; then
  bold "→ installing protoc ${PROTOC_VERSION}"
  install_protoc
else
  bold "✓ protoc $(protoc --version)"
fi

# Ensure ~/.local/bin is on PATH for the rest of this script
export PATH="$BIN:$PATH"

# ── 2. protoc-gen-go + protoc-gen-go-grpc ────────────────────────────────────
install_go_plugin() {
  local pkg="$1" ver="$2" bin="$3"
  if ! command -v "$bin" &>/dev/null; then
    step "installing $bin ${ver}"
    # GODEBUG=netdns=cgo forces the system (CGO) DNS resolver instead of Go's
    # pure-Go resolver, which is blocked on VPN setups that filter UDP port 53.
    GODEBUG=netdns=cgo GOPROXY=https://proxy.golang.org GOBIN="$BIN" go install "${pkg}@${ver}"
  else
    bold "✓ $bin already installed"
  fi
}

install_go_plugin "google.golang.org/protobuf/cmd/protoc-gen-go"      "$GO_PROTO_VERSION" "protoc-gen-go"
install_go_plugin "google.golang.org/grpc/cmd/protoc-gen-go-grpc"     "$GO_GRPC_VERSION"  "protoc-gen-go-grpc"

# ── 3. Generate ───────────────────────────────────────────────────────────────
bold "→ generating Go code"

OUT="${ROOT}/proto/gen"
rm -rf "$OUT"
mkdir -p "$OUT"

# protoc needs the well-known types include dir
INCLUDE="$HOME/.local/include"

find "${ROOT}/proto" -name "*.proto" ! -path "${OUT}/*" | while read -r proto_file; do
  step "$proto_file"
  protoc \
    -I "${ROOT}/proto" \
    -I "${INCLUDE}" \
    --go_out="${OUT}"      --go_opt=paths=source_relative \
    --go-grpc_out="${OUT}" --go-grpc_opt=paths=source_relative \
    "${proto_file#${ROOT}/proto/}"
done

bold "✓ done — generated files in proto/gen/"
echo
echo "Add proto/gen/ to go.work and import as:"
echo "  github.com/fsociety/todo/proto/gen/auth/v1"
echo "  github.com/fsociety/todo/proto/gen/todo/v1"
