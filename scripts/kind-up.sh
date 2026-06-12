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
  # Core infrastructure
  caddy:2.8-alpine
  pgvector/pgvector:pg16
  redis:7-alpine
  axllent/mailpit:v1.20
  alpine:3.20                                  # base image for Tilt dev builds
  busybox:1.36                                 # Elasticsearch initContainer (vm.max_map_count)
  # Observability
  prom/prometheus:v2.55.1
  grafana/grafana:11.4.0
  docker.elastic.co/elasticsearch/elasticsearch:8.17.0
  docker.elastic.co/kibana/kibana:8.17.0
  docker.elastic.co/beats/filebeat:8.17.0
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
for ns in todo data monitoring; do
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
bold "→ deploying Caddy + gateway + auth + todo (todo ns)"
kubectl apply -k "${ROOT}/deploy/k8s/base/caddy"
kubectl apply -k "${ROOT}/deploy/k8s/base/gateway"
kubectl apply -k "${ROOT}/deploy/k8s/base/auth"
kubectl apply -k "${ROOT}/deploy/k8s/base/todo"

bold "→ deploying Postgres + Redis + Mailpit (data ns)"
kubectl apply -k "${ROOT}/deploy/k8s/base/postgres"
kubectl apply -k "${ROOT}/deploy/k8s/base/redis"
kubectl apply -k "${ROOT}/deploy/k8s/base/mailpit"

bold "→ waiting for data pods to be ready (up to 2m)"
kubectl -n data wait --for=condition=ready pod -l app=postgres --timeout=2m || true
kubectl -n data wait --for=condition=ready pod -l app=redis    --timeout=1m || true
kubectl -n data wait --for=condition=ready pod -l app=mailpit  --timeout=1m || true

bold "→ deploying observability stack (monitoring ns)"
kubectl apply -f "${ROOT}/infra/prometheus/namespace.yaml" 2>/dev/null || true
kubectl apply -f "${ROOT}/infra/prometheus/rbac.yaml"
kubectl apply -f "${ROOT}/infra/prometheus/configmap.yaml"
kubectl apply -f "${ROOT}/infra/prometheus/deployment.yaml"
kubectl apply -f "${ROOT}/infra/prometheus/service.yaml"
kubectl apply -f "${ROOT}/infra/grafana/configmap.yaml"
kubectl apply -f "${ROOT}/infra/grafana/dashboard-provider-cm.yaml"
kubectl apply -f "${ROOT}/infra/grafana/dashboard-grpc-cm.yaml"
kubectl apply -f "${ROOT}/infra/grafana/dashboard-http-cm.yaml"
kubectl apply -f "${ROOT}/infra/grafana/deployment.yaml"
kubectl apply -f "${ROOT}/infra/grafana/service.yaml"
kubectl apply -f "${ROOT}/infra/elk/elasticsearch.yaml"
kubectl apply -f "${ROOT}/infra/elk/kibana.yaml"
kubectl apply -f "${ROOT}/infra/elk/filebeat-rbac.yaml"
kubectl apply -f "${ROOT}/infra/elk/filebeat-config-cm.yaml"
kubectl apply -f "${ROOT}/infra/elk/filebeat-daemonset.yaml"

bold "→ patching CoreDNS to use 8.8.8.8 (VPN-friendly external DNS)"
# kind nodes forward external DNS via /etc/resolv.conf which is unreachable
# behind a corporate VPN. Switch to Google public DNS so pods can reach
# external APIs (Stripe, Claude, etc.) without changing the host resolver.
kubectl get configmap coredns -n kube-system -o json \
  | python3 -c "
import json, sys
cm = json.load(sys.stdin)
cm['data']['Corefile'] = cm['data']['Corefile'].replace(
    'forward . /etc/resolv.conf {',
    'forward . 8.8.8.8 8.8.4.4 {'
)
print(json.dumps(cm))
" | kubectl apply -f -
kubectl rollout restart deployment/coredns -n kube-system
kubectl rollout status  deployment/coredns -n kube-system --timeout=60s

bold "→ generating JWT key (idempotent)"
"${ROOT}/scripts/gen-jwt-key.sh"

bold "✓ cluster ready"
echo
echo "Phase 1 verify:"
echo "  source .envrc                 # set GODEBUG=netdns=cgo for Go DNS"
echo "  /usr/local/bin/tilt up        # build + deploy all services"
echo "  curl http://localhost:8080/healthz"
echo "  curl -s -X POST http://localhost:8080/api/v1/auth/signup \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"email\":\"test@example.com\",\"password\":\"secret123\",\"name\":\"Test\"}'"
echo
echo "When you start Phase 2 (Kafka):"
echo "  ./scripts/install-kafka.sh"
echo
echo "Status:"
echo "  kubectl get pods -A"
