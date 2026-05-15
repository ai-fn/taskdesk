#!/usr/bin/env bash
# Recover from Kafka/ZooKeeper corruption (CrashLoopBackOff after cluster resume).
# Deletes the Kafka CR (wiping ephemeral ZK+Kafka data) and recreates it fresh.
# Safe to run any time — topics are recreated by the entity operator.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bold() { printf '\033[1m%s\033[0m\n' "$*"; }

bold "→ deleting Kafka CR (wipes ephemeral ZK/Kafka data)"
kubectl delete kafka todo -n data --ignore-not-found

bold "→ waiting for pods to terminate"
kubectl -n data wait --for=delete pod/todo-kafka-0     --timeout=60s 2>/dev/null || true
kubectl -n data wait --for=delete pod/todo-zookeeper-0 --timeout=60s 2>/dev/null || true

bold "→ recreating Kafka + topic"
kubectl apply -f "${ROOT}/deploy/k8s/base/kafka/kafka.yaml"
kubectl apply -f "${ROOT}/deploy/k8s/base/kafka/topics.yaml"

bold "→ waiting for Kafka to be ready (up to 8m)"
kubectl -n data wait kafka/todo --for=condition=Ready --timeout=8m || {
  echo "Kafka not ready. Check: kubectl -n data get pods"
  exit 1
}

bold "✓ Kafka healthy"
