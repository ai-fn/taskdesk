#!/usr/bin/env bash
# Resume the kind cluster after cluster-stop.sh.
# Restores Kafka replica count then hands off to Tilt.
set -euo pipefail

bold() { printf '\033[1m%s\033[0m\n' "$*"; }

bold "→ starting kind cluster container"
docker start todo-control-plane
kubectl config use-context kind-todo

# Restore Kafka if it was scaled down by cluster-stop.sh.
if kubectl -n data get kafka todo >/dev/null 2>&1; then
  bold "→ restoring Kafka"
  kubectl -n data scale statefulset todo-kafka --replicas=1 2>/dev/null || true
  kubectl -n data wait kafka/todo --for=condition=Ready --timeout=5m 2>/dev/null \
    && echo "  ✓ Kafka ready" \
    || echo "  ⚠ Kafka still starting — check: kubectl -n data get pods"
fi

bold "✓ cluster resumed. Run: tilt up"
