#!/usr/bin/env bash
# Bring up the local kind cluster + install in-cluster dependencies.
#
# Idempotent: safe to re-run. Uses pre-pulled host images + `kind load`
# instead of pulling from inside the cluster, which sidesteps DNS issues
# in restricted-network environments (the host VPN is the source of truth
# for connectivity).
#
# Strimzi/Kafka is intentionally NOT installed here — it's heavy and
# pulls many images dynamically. Run scripts/install-kafka.sh before
# starting Phase 2.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER="todo"

bold() { printf '\033[1m%s\033[0m\n' "$*"; }

# Images we need in the cluster. Pinned tags only — never `:latest` so the
# cluster is reproducible and we know exactly what's loaded.
IMAGES=(
  caddy:2.8-alpine
  postgres:16-alpine
  redis:7-alpine
  axllent/mailpit:v1.20
  alpine:3.20                                  # base image for Tilt dev builds
)

# 1. Cluster -----------------------------------------------------------------
if kind get clusters | grep -qx "${CLUSTER}"; then
  bold "✓ kind cluster '${CLUSTER}' already exists"
else
  bold "→ creating kind cluster '${CLUSTER}'"
  kind create cluster --config "${ROOT}/infra/kind-config.yaml"
fi

kubectl config use-context "kind-${CLUSTER}" >/dev/null

# 2. Namespaces --------------------------------------------------------------
for ns in todo data observability; do
  kubectl create namespace "${ns}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done

# 3. Pre-pull on host + load into kind ---------------------------------------
# Workaround for kind nodes that can't resolve registry-1.docker.io directly
# (corporate / VPN / restricted networks). We pull on the host where DNS / VPN
# works, then push into kind's local image store so kubelet can use the images
# with imagePullPolicy=IfNotPresent.
#
# We bypass `kind load docker-image` and call `ctr import` directly: kind's
# loader passes --all-platforms which is broken on Docker 25+ with the
# containerd image store (manifest list digests reference platform layers
# that aren't actually in the local store).
NODE="${CLUSTER}-control-plane"
load_image() {
  local img="$1"
  if docker image inspect "${img}" >/dev/null 2>&1; then
    echo "  ✓ ${img} (already on host)"
  else
    echo "  ↓ pulling ${img}"
    docker pull --platform=linux/amd64 "${img}"
  fi
  echo "  ↪ importing into kind node"
  docker save "${img}" | docker exec -i "${NODE}" ctr -n k8s.io images import -
}

bold "→ pre-pulling images on host + loading into kind"
for img in "${IMAGES[@]}"; do
  load_image "${img}"
done

# 4. Apply manifests ---------------------------------------------------------
bold "→ deploying Caddy + gateway (todo ns)"
kubectl apply -k "${ROOT}/deploy/k8s/base/caddy"
kubectl apply -k "${ROOT}/deploy/k8s/base/gateway"

bold "→ deploying Postgres + Redis + Mailpit (data ns)"
kubectl apply -k "${ROOT}/deploy/k8s/base/postgres"
kubectl apply -k "${ROOT}/deploy/k8s/base/redis"
kubectl apply -k "${ROOT}/deploy/k8s/base/mailpit"

bold "→ waiting for data pods to be ready (up to 2m)"
kubectl -n data wait --for=condition=ready pod -l app=postgres --timeout=2m || true
kubectl -n data wait --for=condition=ready pod -l app=redis    --timeout=1m || true
kubectl -n data wait --for=condition=ready pod -l app=mailpit  --timeout=1m || true

bold "✓ cluster ready"
echo
echo "Phase 0 verify:"
echo "  tilt up                       # build + deploy gateway with live reload"
echo "  curl http://localhost:8080/healthz"
echo
echo "When you start Phase 2 (Kafka):"
echo "  ./scripts/install-kafka.sh"
echo
echo "Status:"
echo "  kubectl get pods -A"
