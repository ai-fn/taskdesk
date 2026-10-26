#!/usr/bin/env bash
# Install Strimzi (Kafka operator) + a single-broker Kafka cluster.
#
# Run this once before starting Phase 2. Pre-pulls images into kind to avoid
# pull failures during reconciliation.
#
# NOTE: strimzi.io DNS may be blocked by VPN. The operator YAML is stored in
# deploy/strimzi/ and applied from disk — no live DNS lookup needed.
#
# If you hit pull failures, check that your VPN is up before re-running.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER="todo"
STRIMZI_VERSION="0.45.2"
KAFKA_VERSION="3.9.0"

bold() { printf '\033[1m%s\033[0m\n' "$*"; }

IMAGES=(
  quay.io/strimzi/operator:${STRIMZI_VERSION}
  quay.io/strimzi/kafka:${STRIMZI_VERSION}-kafka-${KAFKA_VERSION}
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

bold "→ installing Strimzi operator (data ns, from local file)"
# Apply from local file — avoids DNS lookup for strimzi.io (blocked on some VPNs).
# File was downloaded with: curl -sL https://github.com/strimzi/strimzi-kafka-operator/releases/download/${STRIMZI_VERSION}/strimzi-cluster-operator-${STRIMZI_VERSION}.yaml
sed 's/namespace: myproject/namespace: data/g' \
  "${ROOT}/deploy/strimzi/strimzi-cluster-operator-${STRIMZI_VERSION}.yaml" \
  | kubectl apply -n data -f -

bold "→ waiting for operator to be ready"
kubectl -n data wait --for=condition=available deployment/strimzi-cluster-operator --timeout=3m

bold "→ creating Kafka cluster + topic"
kubectl apply -f "${ROOT}/deploy/k8s/base/kafka/kafka.yaml"
kubectl apply -f "${ROOT}/deploy/k8s/base/kafka/topics.yaml"

bold "→ waiting for Kafka to be ready (up to 8m, first time can be slow)"
kubectl -n data wait kafka/todo --for=condition=Ready --timeout=8m || {
  echo
  echo "Kafka didn't come up in 8m. Check:"
  echo "  kubectl -n data get kafka,pods"
  echo "  kubectl -n data describe kafka todo"
  exit 1
}

kubectl -n data wait kafkatopic --all --for=condition=Ready --timeout=60s

bold "✓ Kafka cluster + topic ready"
echo
echo "Bootstrap address (inside cluster): todo-kafka-bootstrap.data.svc.cluster.local:9092"
