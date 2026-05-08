#!/usr/bin/env bash
# Generate the RSA private key used by auth-service to sign JWTs,
# then store it as a Kubernetes Secret in the cluster.
#
# Safe to re-run: if the secret already exists the key is NOT rotated
# (rotation would invalidate all active sessions). Pass --force to rotate.
set -euo pipefail

CLUSTER_CTX="kind-todo"
NAMESPACE="todo"
SECRET_NAME="auth-jwt-key"
KEY_FILE="$(mktemp /tmp/jwt_private_XXXXXX.pem)"

bold() { printf '\033[1m%s\033[0m\n' "$*"; }

FORCE=false
for arg in "$@"; do [[ "$arg" == "--force" ]] && FORCE=true; done

kubectl config use-context "${CLUSTER_CTX}" >/dev/null

if kubectl -n "${NAMESPACE}" get secret "${SECRET_NAME}" &>/dev/null; then
  if [[ "$FORCE" == false ]]; then
    bold "✓ ${SECRET_NAME} already exists in namespace ${NAMESPACE} — skipping"
    echo "  Pass --force to rotate (WARNING: invalidates all active sessions)"
    exit 0
  fi
  bold "→ --force: deleting existing ${SECRET_NAME}"
  kubectl -n "${NAMESPACE}" delete secret "${SECRET_NAME}"
fi

bold "→ generating 2048-bit RSA key"
openssl genrsa -out "${KEY_FILE}" 2048 2>/dev/null

bold "→ creating Kubernetes Secret ${SECRET_NAME}"
kubectl -n "${NAMESPACE}" create secret generic "${SECRET_NAME}" \
  --from-file="jwt_private.pem=${KEY_FILE}"

rm -f "${KEY_FILE}"

bold "✓ done — auth-service will load the key from /secrets/jwt_private.pem"
