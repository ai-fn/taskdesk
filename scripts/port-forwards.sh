#!/usr/bin/env bash
# Start background port-forwards for dev tools not exposed via NodePort.
# Run this once after `tilt up`. Safe to re-run (kills stale forwards first).
set -euo pipefail

bold() { printf '\033[1m%s\033[0m\n' "$*"; }

# Kill any existing forwards for these ports.
for port in 8025 5432 6379 9000 9001; do
  lsof -ti :"${port}" | xargs -r kill -9 2>/dev/null || true
done

bold "→ forwarding Mailpit  → http://localhost:8025"
kubectl -n data port-forward svc/mailpit 8025:8025 &

bold "→ forwarding Postgres → localhost:5432"
kubectl -n data port-forward svc/postgres 5432:5432 &

bold "→ forwarding Redis    → localhost:6379"
kubectl -n data port-forward svc/redis 6379:6379 &

bold "→ forwarding MinIO API     → http://localhost:9000"
kubectl -n data port-forward svc/minio 9000:9000 &

bold "→ forwarding MinIO Console → http://localhost:9001"
kubectl -n data port-forward svc/minio 9001:9001 &

bold "✓ port-forwards running in background (kill with: pkill -f 'kubectl.*port-forward')"
echo
echo "  Mailpit:       http://localhost:8025"
echo "  Postgres:      psql -h localhost -U postgres -d todo"
echo "  Redis:         redis-cli -h localhost"
echo "  MinIO API:     http://localhost:9000  (minioadmin / minioadmin)"
echo "  MinIO Console: http://localhost:9001"
