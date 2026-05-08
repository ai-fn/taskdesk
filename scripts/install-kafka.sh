#!/usr/bin/env bash
# Install Strimzi (Kafka operator) + a single-broker Kafka cluster.
#
# Run this once before starting Phase 2. Strimzi pulls many images dynamically
# (operator + Kafka + Zookeeper + cruise-control + ...), so this script may
# take several minutes on first run and is sensitive to registry connectivity.
#
# If you hit DNS / pull failures, check that your VPN is up before re-running.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER="todo"
STRIMZI_VERSION="0.43.0"

bold() { printf '\033[1m%s\033[0m\n' "$*"; }

# Pre-pull the operator image + the Kafka/Zookeeper images Strimzi spawns.
# Strimzi defers actual pull decisions to the operator, but kind-loading
# these saves a lot of time on first reconciliation.
IMAGES=(
  quay.io/strimzi/operator:${STRIMZI_VERSION}
  quay.io/strimzi/kafka:${STRIMZI_VERSION}-kafka-3.7.0
)

NODE="${CLUSTER}-control-plane"
load_image() {
  local img="$1"
  if ! docker image inspect "${img}" >/dev/null 2>&1; then
    echo "  ↓ pulling ${img}"
    docker pull --platform=linux/amd64 "${img}"
  fi
  echo "  ↪ importing ${img} into kind node"
  docker save "${img}" | docker exec -i "${NODE}" ctr -n k8s.io images import -
}

bold "→ pre-pulling Strimzi images on host + loading into kind"
for img in "${IMAGES[@]}"; do
  load_image "${img}"
done

bold "→ installing Strimzi operator (data ns)"
kubectl create -f \
  "https://strimzi.io/install/${STRIMZI_VERSION}/?namespace=data" \
  --dry-run=client -o yaml | kubectl apply -n data -f -

bold "→ waiting for operator to be ready"
kubectl -n data wait --for=condition=available deployment/strimzi-cluster-operator --timeout=3m

bold "→ creating Kafka cluster"
kubectl apply -n data -f "${ROOT}/deploy/k8s/base/kafka/kafka.yaml"

bold "→ waiting for Kafka to be ready (up to 5m, first time can be slow)"
kubectl -n data wait kafka/todo --for=condition=Ready --timeout=5m || {
  echo
  echo "Kafka didn't come up in 5m. Check:"
  echo "  kubectl -n data get kafka,pods"
  echo "  kubectl -n data describe kafka todo"
  exit 1
}

bold "✓ Kafka cluster ready"
echo
echo "Bootstrap address (inside cluster): todo-kafka-bootstrap.data.svc.cluster.local:9092"
