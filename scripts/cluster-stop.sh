#!/usr/bin/env bash
# Pause the kind cluster without losing data.
# Scales Kafka down gracefully before stopping the Docker container
# KRaft mode: no ZooKeeper, just flush the broker WAL.
# Resumes with: ./scripts/cluster-start.sh
set -euo pipefail

bold() { printf '\033[1m%s\033[0m\n' "$*"; }

# Gracefully stop Kafka before freezing the node so it can flush its WAL.
if kubectl -n data get kafka todo >/dev/null 2>&1; then
  bold "→ scaling down Kafka (graceful shutdown)"
  kubectl -n data scale statefulset todo-dual-role --replicas=0 2>/dev/null || true
  kubectl -n data wait --for=delete pod/todo-dual-role-0 --timeout=60s 2>/dev/null || true
fi

bold "→ stopping kind cluster container"
docker stop todo-control-plane
echo "✓ cluster paused (data preserved). Resume with: ./scripts/cluster-start.sh"
