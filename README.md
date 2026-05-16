# todo

Production-shaped Go microservices todo app built for learning Go and the surrounding stack. The todo app is the vehicle — the real subject is gRPC, Kafka, Kubernetes, observability, and AI integration.

See [`CLAUDE.md`](./CLAUDE.md) for architecture decisions and `~/.claude/plans/need-to-plan-great-zesty-moth.md` for the full phased plan.

## Status

| Phase | Description | State |
|---|---|---|
| 0 | Bootstrap: kind cluster, Caddy edge, gateway `/healthz` | ✅ done |
| 1 | Auth service, todo service, React SPA | ✅ done |
| 2 | Kafka backbone, notification service, WebSocket, email | ✅ done |
| 3 | AI daily prioritization via Claude (scheduler CronJob) | ✅ done |
| 4 | Observability: trace IDs, Prometheus + Grafana, ELK stack | ✅ done |
| 5 | CI/CD (GitHub Actions), Helm chart, k6 load tests | planned |

## Prerequisites

- **Go** ≥ 1.23
- **Docker**
- **kind** ≥ 0.24
- **kubectl** ≥ 1.30
- **helm** ≥ 3.14
- **Tilt** ≥ 0.33
- **Node** ≥ 20 (for `web/` frontend dev)

Quick install (Fedora):

```sh
sudo dnf install -y go kubectl nodejs
go install sigs.k8s.io/kind@latest
curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
curl -fsSL https://raw.githubusercontent.com/tilt-dev/tilt/master/scripts/install.sh | bash
```

## Quickstart

```sh
# 1. Create the kind cluster and install all infrastructure dependencies
./scripts/kind-up.sh

# 2. Install Kafka (Strimzi operator + single-broker cluster)
./scripts/install-kafka.sh

# 3. Start the dev loop (compiles Go, loads images into kind, hot-reloads on save)
tilt up

# Tear down
./scripts/kind-down.sh
```

> If you need direct access to Postgres or Redis from the host, run `./scripts/port-forwards.sh`.

## Endpoints

All URLs are accessible from the host after `tilt up`.

### Application

| Tool | URL | Notes |
|---|---|---|
| **SPA + API** | http://localhost:8080 | Caddy edge — serves the React app and proxies `/api/v1/*` to gateway |
| **Tilt UI** | http://localhost:10350 | Live build status, logs, and pod restarts for every service |

The REST API lives under `http://localhost:8080/api/v1/`. Key routes:

```
POST /api/v1/auth/signup       register a new user
POST /api/v1/auth/login        obtain access token + HttpOnly refresh cookie
POST /api/v1/auth/refresh      rotate tokens (uses cookie)
POST /api/v1/auth/logout

GET    /api/v1/tasks           list tasks (paginated)
POST   /api/v1/tasks           create task
GET    /api/v1/tasks/{id}      get task
PATCH  /api/v1/tasks/{id}      update task
DELETE /api/v1/tasks/{id}      delete task
POST   /api/v1/tasks/{id}/complete

GET  /api/v1/plan              latest AI-generated daily plan for the authenticated user
```

### WebSocket (real-time notifications)

The SPA opens a WebSocket on page load. You can connect directly for debugging:

```
ws://localhost:8080/api/v1/ws?token=<access_token>
```

The gateway accepts the access token via `?token=` query param (browsers cannot set `Authorization` headers on WebSocket connections). Events are JSON objects:

```json
{ "event_type": "task.completed", "task_title": "...", "occurred_at": "..." }
{ "type": "daily_plan_ready", "plan_id": "...", "summary": "..." }
```

### Observability

| Tool | URL | Credentials |
|---|---|---|
| **Grafana** | http://localhost:30030 | `admin` / `admin` |
| **Prometheus** | http://localhost:30090 | — |
| **Kibana** | http://localhost:30056 | — |

**Grafana** ships with two pre-built dashboards:
- *gRPC Services* — rate, error rate, p50/p99 latency per method, per service
- *Gateway HTTP* — request rate, status code distribution, top paths

**Prometheus** scrapes all five Go services every 15 s via pod annotations (`prometheus.io/scrape: "true"`). Each service exposes `/metrics` on port 9090.

**Kibana** has a `todo-logs-*` data view pre-created. All structured JSON logs from every service land in Elasticsearch (`todo-logs-YYYY.MM.DD` daily index). Useful fields: `level`, `msg`, `service`, `trace_id`, `kubernetes.container.name`.

Useful Kibana query to filter errors from a specific service:

```
level: "ERROR" AND kubernetes.labels.app: "gateway"
```

### Email (Mailpit)

Mailpit catches all outbound email from the notification service in dev.

| Tool | URL | Notes |
|---|---|---|
| **Mailpit UI** | http://localhost:8025 | All emails sent by notification-service appear here |
| SMTP | cluster-internal only | `mailpit.data.svc.cluster.local:1025` |

### AI daily plan (Phase 3)

The scheduler CronJob fires at 06:00 and calls `ai-service`, which calls Claude with structured tool-use to produce a prioritized daily plan. Trigger it manually:

```sh
kubectl create job --from=cronjob/daily-plan-scheduler test-plan-$(date +%s) -n todo
kubectl logs -n todo -l job-name=test-plan-... -f
```

The plan appears in the SPA under "Today's Plan" and is also emailed to the user.

## Restricted-network / VPN note

Images are pre-pulled on the host (where your VPN is active) and loaded into kind via `docker save | ctr import` — nothing is pulled from inside the cluster. If a pull step fails, check your VPN. See `scripts/kind-up.sh` for the image list.

CoreDNS inside the cluster is configured to use `100.64.0.1` (Tailscale/VPN DNS) so `api.anthropic.com` resolves correctly for the AI service.

## Development workflow

```sh
# Frontend (fast HMR — use this for daily UI work)
cd web && npm run dev          # Vite on :5173, proxies /api/v1/* to localhost:8080

# Backend (Tilt handles this — edit any .go file and Tilt rebuilds in ~2s)
tilt up

# Trigger AI plan generation manually
kubectl create job --from=cronjob/daily-plan-scheduler manual-$(date +%s) -n todo

# Watch Kafka messages
kubectl -n data exec -it todo-kafka-0 -- bin/kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 --topic task.events.v1 --from-beginning

# Tail gateway logs
kubectl -n todo logs -f deploy/gateway
```

## Layout

```
proto/          .proto contracts (source of truth for all gRPC interfaces)
pkg/            shared Go libraries: logger, config, kafka, pgx, tokens, metrics, tracing
services/       one Go module per microservice (gateway, auth, todo, ai, notification, scheduler)
web/            React + Vite + TypeScript SPA
deploy/k8s/     Kustomize base + overlays; Caddyfile lives in deploy/k8s/base/caddy/
infra/          Prometheus, Grafana, and ELK manifests
scripts/        kind-up/down, install-kafka, port-forwards, proto-gen
migrations/     golang-migrate SQL, one directory per service schema
```
