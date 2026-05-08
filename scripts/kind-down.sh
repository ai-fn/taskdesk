#!/usr/bin/env bash
# Tear down the local kind cluster.
set -euo pipefail

CLUSTER="todo"

if kind get clusters | grep -qx "${CLUSTER}"; then
  echo "→ deleting kind cluster '${CLUSTER}'"
  kind delete cluster --name "${CLUSTER}"
else
  echo "✓ no cluster '${CLUSTER}' found, nothing to do"
fi
