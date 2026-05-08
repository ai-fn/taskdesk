# Project context for Claude

This is a greenfield Go microservices todo app. **The point isn't the todo app — it's learning Go and the production stack around it.** Optimize suggestions for clarity and pedagogy over shortest-path delivery.

## Architecture (locked)

- **Microservices** in Go talking via gRPC; REST only at the edge (api-gateway).
- **Edge**: Caddy.
- **Data**: Postgres + Redis.
- **Async bus**: Kafka (`twmb/franz-go`).
- **Auth**: custom JWT auth-service (access in-memory, refresh in HttpOnly cookie / Redis).
- **AI feature**: daily smart prioritization in `ai-service`, calls Claude with tool-use + prompt caching, model `claude-sonnet-4-6`.
- **Frontend**: React + Vite + TS SPA.
- **Local dev**: kind cluster from day one, Tilt for live reload.
- **Observability**: ELK for logs + Prometheus/Grafana for metrics.

## Services

| Service | Public protocol | Internal protocol | Owns |
|---|---|---|---|
| `gateway` | HTTP/REST + WebSocket | gRPC clients | edge, JWT verification, WS fan-out |
| `auth` | — | gRPC | users, JWTs |
| `todo` | — | gRPC + Kafka producer | tasks/lists/tags |
| `ai` | — | gRPC + Anthropic API | daily plan generation |
| `notification` | — | Kafka consumer + SMTP + Redis pub/sub | email + in-app pushes |
| `scheduler` | — | gRPC client | K8s CronJob driver |

## Repo layout

```
proto/        # .proto contracts (source of truth)
pkg/          # shared Go libs (logger, config, kafka, pgx, tokens, otel)
services/     # one Go module per microservice
web/          # React SPA
deploy/k8s/   # Kustomize base + overlays (Caddyfile lives in deploy/k8s/base/caddy/)
infra/        # Strimzi/ELK/Prom helm values
scripts/      # kind-up, kind-down, install-deps, proto-gen
migrations/   # golang-migrate, one dir per service-schema
```

## Conventions

- Go workspace (`go.work`) — every service is its own module under `services/<name>/`, sharing `pkg/`.
- DB-per-service-schema — each service owns its own Postgres schema; no cross-schema reads.
- Kafka: transactional outbox pattern (events written in same Postgres tx as state change).
- Logs: stdlib `log/slog` JSON to stdout; trace ID propagated via context.
- Errors: return errors up; log only at the boundary (gRPC server / HTTP handler).
- No ORM — `sqlc` codegen on top of `pgx/v5`.

## Phases

Currently in Phase 0 (bootstrap). See `~/.claude/plans/need-to-plan-great-zesty-moth.md` for the full phased plan.

## Don't

- Don't propose ORMs (gorm, ent) — `sqlc` is chosen deliberately for learning value.
- Don't expose gRPC directly to the browser. Always go through gateway.
- Don't publish to Kafka outside a Postgres transaction. Use the outbox pattern.
- Don't store JWT access tokens in localStorage.
