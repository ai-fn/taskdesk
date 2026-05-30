#!/usr/bin/env bash
# Recover from Kafka CrashLoopBackOff after cluster resume.
# Deletes the Kafka CR + KafkaNodePool (wiping ephemeral data) and recreates fresh.
# Safe to run any time — topics are recreated by the entity operator.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bold() { printf '\033[1m%s\033[0m\n' "$*"; }

bold "→ deleting Kafka CR + KafkaNodePool (wipes ephemeral data)"
kubectl delete kafka todo -n data --ignore-not-found
kubectl delete kafkanodepool dual-role -n data --ignore-not-found

bold "→ waiting for pod to terminate"
kubectl -n data wait --for=delete pod/todo-dual-role-0 --timeout=60s 2>/dev/null || true

bold "→ recreating Kafka + topic"
kubectl apply -f "${ROOT}/deploy/k8s/base/kafka/kafka.yaml"
kubectl apply -f "${ROOT}/deploy/k8s/base/kafka/topics.yaml"

# Seed kafkaMetadataState=KRaft before the operator can default it to ZooKeeper.
# Strimzi 0.45 state machine uses this status field to choose the reconciliation path;
# without it a fresh KRaft CR falls into the migration path and fails validation.
bold "→ seeding kafkaMetadataState=KRaft in status"
for i in $(seq 1 10); do
  sleep 2
  kubectl patch kafka todo -n data --subresource=status --type=merge \
    -p '{"status":{"kafkaMetadataState":"KRaft"}}' 2>/dev/null && break || true
done

bold "→ waiting for Kafka to be ready (up to 8m)"
kubectl -n data wait kafka/todo --for=condition=Ready --timeout=8m || {
  echo "Kafka not ready. Check: kubectl -n data get pods"
  exit 1
}

bold "✓ Kafka healthy"
