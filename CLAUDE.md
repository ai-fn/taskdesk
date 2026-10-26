# Project context for Claude

**Production Go microservices product.** Optimize every suggestion for correctness, performance, and production safety — not for brevity or pedagogy. Prefer explicit error handling, connection pool hygiene, and graceful shutdown over clean-looking code. When in doubt, choose the option that fails loudly and recovers cleanly.

---

## Stack at a glance

Full-stack task management SaaS. kind cluster + Tilt in dev; production target is multi-replica k8s. Services: auth, todo, ai, notification, scheduler + React SPA. ELK + Prometheus/Grafana observability. Full i18n (EN + ZH). Argon2id passwords, RS256 JWTs, transactional Kafka outbox, SSE AI plan streaming.

---

## Architecture

```
Browser
  ↕ HTTPS (Caddy)
gateway (HTTP/REST + WebSocket, chi router)
  ↕ gRPC
auth-service  todo-service  ai-service
                 ↕ Kafka (franz-go)
              notification-service  scheduler (CronJob)
```

- **No gRPC to browser** — gateway is the only HTTP-speaking service.
- **DB-per-schema** — `auth.*`, `todo.*` schemas in a single Postgres instance; no cross-schema queries.
- **Outbox pattern** — todo-service writes Kafka events to `todo.outbox` in the same Postgres transaction as the state change; a background publisher polls and drains to Kafka every 500 ms.
- **JWT** — access token kept in React memory only; refresh token in HttpOnly cookie. 401 interceptor with subscriber queue (prevents token double-invalidation on concurrent requests).
- **Health probes** — `pkg/lifecycle`. Liveness is shallow (gRPC `""` service, gateway `GET /healthz`, notification `:9090/healthz`) so a dependency outage never restarts pods. Readiness checks dependencies: gRPC services expose health service `readiness` (auth/todo/ai: Postgres, 2 consecutive failures to flip), gateway `GET /readyz` (auth + todo only — an ai/redis outage degrades features, it must not unroute the edge), notification `:9090/readyz` (Redis + Kafka). New services must implement both before wiring into k8s.
- **Graceful shutdown** — all services catch `SIGTERM`, stop accepting new work, drain in-flight requests (5 s timeout; gRPC servers use `lifecycle.GracefulStop`, which force-stops after it; gateway allows 10 s, ai 25 s for in-flight streams), then close DB pools and Kafka connections. Never `os.Exit` outside `main`. Context cancellation must propagate to all goroutines.
- **Connection pools** — pgxpool `MaxConns` is set per-service based on Postgres `max_connections` budget: auth=10, todo=20, ai=5. Kafka producers use a single shared client per service; never create per-request producers.

---

## Services

### `services/auth`
Auth, users, JWTs. Owns `auth.*` schema.

**RPCs:** `SignUp`, `Login`, `RefreshToken`, `Logout`, `GetJWKS`, `ValidateToken`, `ListUserIDs`, `GetUserByID`

**Password policy:** `services/auth/internal/password` — `Validate(pw)` called in `SignUp` before bcrypt. Scoring rules intentionally mirror `web/src/lib/passwordStrength.ts` so the strength bar the user sees matches server enforcement.

**DB tables:**
- `auth.users` — id, email, name, password_hash (argon2id), language, time_zone (IANA, default `UTC`; drives "today" and plan timing), created_at, updated_at
- `auth.refresh_tokens` — token_hash, user_id, expires_at, created_at

---

### `services/todo`
Tasks, lists, groups. Owns `todo.*` schema. Kafka producer via outbox.

**RPCs:**
- Tasks: `CreateTask`, `GetTask`, `ListTasks`, `UpdateTask`, `DeleteTask`, `CompleteTask`, `RestoreTask` (un-soft-delete), `PurgeTask` (hard delete)
- Lists: `CreateList`, `ListLists`, `UpdateList`, `DeleteList`, `ReorderLists`
- Groups: `CreateGroup`, `ListGroups`, `UpdateGroup`, `DeleteGroup`, `ReorderGroups`

**DB tables (all in `todo` schema):**

`tasks`:
| column | type | notes |
|--------|------|-------|
| id | TEXT PK | |
| user_id | TEXT | |
| title | TEXT | |
| description | TEXT | default '' |
| priority | SMALLINT | 0=unspec 1=low 2=med 3=high 4=urgent |
| status | SMALLINT | 1=todo 2=in_progress 3=done |
| tags | TEXT[] | |
| due_at | TIMESTAMPTZ | nullable |
| list_id | TEXT FK→lists | nullable, ON DELETE SET NULL |
| flagged | BOOLEAN | default false |
| deleted_at | TIMESTAMPTZ | soft-delete; null = not deleted |
| created_at / updated_at | TIMESTAMPTZ | |

`lists`:
| column | type | notes |
|--------|------|-------|
| id | TEXT PK | |
| user_id | TEXT | |
| name, color, icon | TEXT | color is hex e.g. `#007AFF`, icon is emoji |
| position | INTEGER | for drag-to-reorder |
| is_hidden | BOOLEAN | hides from home grid |
| group_id | TEXT FK→list_groups | nullable |
| task_count | — | computed by JOIN, not stored |

`list_groups`:
| column | type |
|--------|------|
| id, user_id, name | TEXT |
| position | INTEGER |

`outbox`: pending Kafka events (written in same tx as state change). Columns: id, topic, key, payload, published_at.

**Key files:**
- `services/todo/internal/store/store.go` — all SQL (pgx/v5, no ORM)
- `services/todo/internal/server/server.go` — gRPC handler
- `services/todo/internal/outbox/publisher.go` — polls outbox → Kafka (500 ms interval)

---

### `services/ai`
Calls Claude API. Owns its own DB table for storing plans.

**RPCs:**
- `GeneratePlan(GeneratePlanRequest) → GeneratePlanResponse` — blocking, used by scheduler
- `GeneratePlanStream(GeneratePlanRequest) → stream PlanStreamChunk` — server-streaming; sends `{text_delta}` chunks then final `{plan}` chunk
- `GetLatestPlan(GetLatestPlanRequest) → GetLatestPlanResponse`

**AI implementation details:**
- Model: `claude-sonnet-4-6`
- Prompt caching: system prompt cached with `CacheControlEphemeral` (large, stable across users)
- Tool use: `submit_daily_plan` tool with strict schema forces structured output; Claude writes narrative text first (streamed), then calls the tool
- `GenerateStream` accumulates streamed narrative via `strings.Builder`; overwrites tool-call summary before returning (so DB stores the streamed text, not the dry tool summary)
- Guard: returns `codes.FailedPrecondition` with message `"no tasks to prioritize"` if user has no tasks

**Key files:**
- `services/ai/internal/planner/planner.go` — Claude API calls, both blocking and streaming
- `services/ai/internal/server/server.go` — gRPC server, fetches tasks from todo-service, persists plan, publishes to Kafka

---

### `services/gateway`
HTTP edge. Only service the browser talks to. Uses `chi` router.

**All routes** (all protected routes require `Authorization: Bearer <token>` or valid session):

```
POST /api/v1/auth/signup
POST /api/v1/auth/login
POST /api/v1/auth/refresh
POST /api/v1/auth/logout

GET    /api/v1/tasks                    ?list_id, flagged_only, include_deleted, due_today, has_due_date, status, priority, page_size, page_token
POST   /api/v1/tasks
GET    /api/v1/tasks/{taskID}
PATCH  /api/v1/tasks/{taskID}
DELETE /api/v1/tasks/{taskID}           (soft delete)
POST   /api/v1/tasks/{taskID}/complete
POST   /api/v1/tasks/{taskID}/restore
DELETE /api/v1/tasks/{taskID}/purge     (hard delete)

GET    /api/v1/lists
POST   /api/v1/lists
POST   /api/v1/lists/reorder            body: {list_ids: []}
PATCH  /api/v1/lists/{listID}
DELETE /api/v1/lists/{listID}

GET    /api/v1/groups
POST   /api/v1/groups
POST   /api/v1/groups/reorder
PATCH  /api/v1/groups/{groupID}
DELETE /api/v1/groups/{groupID}

GET    /api/v1/plan                     latest plan for current user
POST   /api/v1/plan/generate            SSE stream: text deltas then done event

GET    /api/v1/ws                       WebSocket for real-time notifications
```

**Key files:**
- `services/gateway/internal/handler/` — one file per domain: `auth.go`, `todo.go`, `lists.go`, `groups.go`, `plan.go`, `ws.go`
- `services/gateway/internal/server/server.go` — chi router wiring
- `services/gateway/internal/middleware/` — JWT auth middleware

**SSE plan stream format:**
```
data: {"type":"text","delta":"word "}
data: {"type":"done","plan":{...DailyPlan...}}
data: {"type":"error","error":"message"}
```

---

### `services/notification`
Kafka consumer. Sends emails (Mailpit in dev) + Redis pub/sub for WebSocket fan-out.

**SMTP auth:** `mailer.New(addr, from, user, pass)` — empty user+pass → unauthenticated (Mailpit). Both set → `smtp.PlainAuth`. k8s reads credentials from `deploy/k8s/base/notification/.env` (gitignored; copy from `.env.example`).

**Topics consumed:**
- `task.events.v1` — task created/updated/completed/deleted; sends email on `task.completed`
- `daily-plan.generated.v1` — daily plan ready; sends email + pushes to Redis pub/sub channel `user:{id}`

**Key files:**
- `services/notification/internal/consumer/` — handler + runner
- `services/notification/internal/mailer/` — SMTP via `net/smtp`

---

### `services/scheduler`
K8s CronJob. Iterates all users via `auth.ListUserIDs`, calls `ai.GeneratePlan` for each.

---

## Kafka topics

| Topic | Producer | Consumer | When |
|-------|----------|----------|------|
| `task.events.v1` | todo-service (via outbox) | notification-service | any task mutation |
| `daily-plan.generated.v1` | ai-service | notification-service | after plan generated |

Event type field in `task.events.v1`: `task.created`, `task.updated`, `task.completed`, `task.deleted`

---

## Performance & capacity

### Query discipline
- **All list queries must be paginated.** Never fetch unbounded rows. Use `page_size` + cursor (`page_token`). Max `page_size` enforced at gateway: 200 for tasks, 500 for lists.
- **No N+1 queries.** Fetch related data in a single JOIN or a bounded `WHERE id = ANY($1)` batch. The store layer must never call SQL in a loop.
- **Indexes** — any new filter column added to a list query needs an index. Add it in the migration; document it in the store file above the query.

### Kafka
- Consumer group IDs: `notification-service`, `scheduler` — never hardcode strings outside config. Changing a group ID resets offset to latest by default; coordinate with ops.
- Outbox publisher polls every 500 ms; acceptable lag for non-critical events. For latency-sensitive flows, reduce interval via env var `OUTBOX_POLL_INTERVAL`.
- Kafka producers: `acks=all`, `retries=5`, idempotent enabled. Do not lower these for "performance" — at-least-once delivery is a hard requirement.

### Redis
- Pub/sub for WebSocket fan-out only (`user:{id}` channel). Not used as primary data store. TTL on all keys that aren't pub/sub channels.
- WebSocket connections per gateway pod: target <10 k. Above that, add gateway replicas and a sticky-session load balancer.

### Rate limiting
- Gateway enforces per-IP rate limits via middleware: 100 req/s sustained, burst 200. Auth endpoints (signup, login) are stricter: 10 req/s per IP.
- AI plan generation is limited to 1 concurrent SSE stream per user (enforced in-process via a per-user semaphore in the gateway handler).

### Pod resources (production targets)
| Service | CPU request | CPU limit | Mem request | Mem limit |
|---------|-------------|-----------|-------------|-----------|
| gateway | 100m | 500m | 64Mi | 256Mi |
| auth | 100m | 300m | 64Mi | 128Mi |
| todo | 100m | 500m | 64Mi | 256Mi |
| ai | 200m | 1000m | 128Mi | 512Mi |
| notification | 50m | 200m | 32Mi | 128Mi |

---

## Ops / runbook

### Schema migrations
Migrations must be **backward-compatible** — old binary runs against new schema, new binary runs against old schema. Steps for any DDL change:
1. Write `.up.sql` / `.down.sql` in `migrations/{service}/` (the only place to edit).
2. Run `scripts/sync-migrations.sh`: it copies them to `deploy/k8s/base/{service}/` and rewrites the `configMapGenerator` list (Tilt runs it on load; CI runs `--check`).
3. Column additions: nullable or with a default only — never `NOT NULL` without a default on an existing table.
4. Column removals: two-step — first deploy removes all code references, second deploy drops the column.
5. Never run migrations manually in prod; the k8s init container runs them on pod start.

### Incident grep patterns
```bash
# 5xx spike on gateway
kubectl logs -l app=gateway | grep '"status":5' | jq '{ts:.time,path:.path,err:.error}'

# Outbox backlog growing
kubectl exec -it <todo-pod> -- psql $DB_URL -c "SELECT count(*) FROM todo.outbox WHERE published_at IS NULL"

# Kafka consumer lag
kubectl exec -it <kafka-pod> -- kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --group notification-service
```

### Rollback
- Gateway + services: `kubectl rollout undo deployment/<name>` — safe if migration was backward-compatible.
- If migration was not backward-compatible: run `.down.sql` first, then roll back the binary.

---

## Frontend (`web/`)

React 18 + Vite + TypeScript. Tailwind CSS with `darkMode: 'class'`.

**Dev:** `npm run dev` in `web/` — Vite HMR, proxies `/api/v1` → `http://localhost:8080`, `/api/v1/ws` → `ws://localhost:8080`
**Prod:** nginx serves `dist/`, SPA fallback via `try_files`

### Pages
| Route | Page | Notes |
|-------|------|-------|
| `/` | `ListsHomePage` | Grid of system + custom lists, AI daily plan panel, search |
| `/tasks/:listId` | `TasksPage` | Task list for a specific list or virtual list |
| `/login` | `LoginPage` | |
| `/signup` | `SignupPage` | |

### System (virtual) lists — computed, no DB rows
`all`, `today` (due_today=true), `scheduled` (has_due_date=true), `flagged` (flagged_only=true), `completed` (status=3), `deleted` (include_deleted=true)

### Key hooks
| Hook | File | Purpose |
|------|------|---------|
| `useAuth` | `hooks/useAuth.ts` | auth state, login/logout/signup |
| `useTasks` | `hooks/useTasks.ts` | `useQuery` for task list with filter |
| `useTaskMutations` | `hooks/useTaskMutations.ts` | create, update, delete, complete, flag, restore, purge |
| `useLists` / `useGroups` | `hooks/useLists.ts`, `hooks/useGroups.ts` | list/group CRUD with optimistic updates |
| `usePlan` | `hooks/usePlan.ts` | fetch latest plan |
| `useGeneratePlan` | `hooks/usePlan.ts` | SSE streaming plan generation; returns `{ mutate, cancel, isPending, deltas, errorMessage }` |
| `useNotifications` | `hooks/useNotifications.ts` | WebSocket subscription for real-time toasts |

### Auth flow
1. `AuthProvider` mounts → `POST /api/v1/auth/refresh` (sends HttpOnly cookie) → on 200: stores token in memory
2. `isInitializing=true` blocks route rendering (prevents `/login` flash)
3. Access token stored in `let _token` module variable (not React state) so Axios interceptor can read it synchronously
4. 401 interceptor: subscriber queue prevents double-refresh on concurrent 401s

### React Query cache keys
```ts
taskKeys.list(filterString)   // tasks list
taskKeys.detail(id)
listKeys.list()
groupKeys.list()
planKeys.latest()
```

### Theme system
- `ThemeContext` (`context/ThemeContext.tsx`) — persists to `localStorage`, applies `dark` class to `<html>`
- Modes: `light | dark | system`; cycles on each click of `ThemeToggle`
- `index.html` has inline `<script>` to apply theme before React mounts (prevents flash)
- Fallback: polls `prefers-color-scheme` every 2 s for Linux/GNOME setups where mq change event doesn't fire

### Password strength
- `web/src/lib/passwordStrength.ts` exports `scorePassword(pw): number` and `MIN_PASSWORD_SCORE`.
- `web/src/components/ui/PasswordStrengthBar.tsx` — include under any new-password `<Input>`. Add `validate: (v) => scorePassword(v) >= MIN_PASSWORD_SCORE || t('auth.password.tooWeak')` to the `register()` call.

---

## Shared packages (`pkg/`)

| Package | Purpose |
|---------|---------|
| `pkg/logger` | `slog` wrapper, JSON output, trace ID in context |
| `pkg/config` | `envconfig` loader |
| `pkg/pgx` | pgxpool factory + health check |
| `pkg/kafka` | `franz-go` producer/consumer wrappers |
| `pkg/tokens` | JWT sign/verify (RS256) |
| `pkg/metrics` | Prometheus HTTP middleware + gRPC interceptors |
| `pkg/tracing` | Trace ID generation + context propagation |
| `pkg/events` | Kafka event type constants (`task.events.v1`) |

---

## Key scripts

```bash
scripts/cluster-start.sh     # start kind cluster + Tilt
scripts/cluster-stop.sh      # stop
scripts/port-forwards.sh     # forward gateway:8080, grafana:3000, kibana:5601, mailpit:8025
scripts/proto-gen.sh         # regenerate proto/gen/ from .proto files
scripts/seed.sh              # seed test data (default user)
scripts/seed-olivia.sh       # seed yan.olivia@gmail.com (business consultant, 4 groups, 37 tasks)
```

---

## Proto codegen

All `.proto` files live in `proto/{auth,todo,ai}/v1/`. Generated Go code goes to `proto/gen/`.
After editing a proto: `./scripts/proto-gen.sh`
The `proto/gen/go.mod` must be kept consistent with the versions in go.work.

---

## Testing

Three layers, all stdlib `testing` (no testify), table-driven where applicable.

### Layer 1 — Pure unit (no Docker, fast)

```bash
go test ./pkg/tokens/                                     # JWT mint/verify, KID, pubkey
go test ./services/auth/internal/server/ -run "TestHash|TestDevice"  # hashToken, deviceLabel
go test ./services/gateway/internal/handler/              # clientInfoFromRequest, handleGRPCError
go test ./services/gateway/internal/middleware/           # Authenticate middleware
```

### Layer 2 — Integration (testcontainers + real Postgres)

Requires Docker. Uses `postgres:16-alpine` (must be pre-pulled). Ryuk reaper disabled via
`TESTCONTAINERS_RYUK_DISABLED=true` (set automatically by the harness). One container per
test package via `TestMain`; `testutil.Truncate` isolates each test.

```bash
go test ./services/auth/internal/store/   -timeout 60s   # SQL: sessions, users, OAuth
go test ./services/auth/internal/server/  -timeout 60s   # gRPC handlers end-to-end
```

**Harness:** `services/auth/internal/testutil/postgres.go` — `StartPostgres()` starts the
container, runs migrations 001–003, returns `(*pgxpool.Pool, teardown)`.

### Layer 3 — Gateway httptest + hand-written fakes

```bash
go test ./services/gateway/internal/handler/    # sessions routes via real chi router
go test ./services/gateway/internal/middleware/ # Authenticate: 401/503, context keys; AuthenticateWS: ?token= (ws route only)
```

`fakeAuthClient` in `handler/fake_test.go` implements `authv1.AuthServiceClient` with per-test
configurable `func` fields; unused methods panic to catch accidental calls.

### Run everything

```bash
# Layer 1 + 3 (no Docker):
go test ./pkg/tokens/ ./services/gateway/...

# Full suite (needs Docker + postgres:16-alpine):
go test ./pkg/... && go test ./services/auth/... -timeout 120s && go test ./services/gateway/...
```

### Notes

- The `sessions.id` column is `UUID` type — pass valid UUIDs in any test that inserts sessions.
- **Test passwords must meet strength policy.** `SignUp` calls `password.Validate()` — use passwords scoring ≥ 2 (e.g. `"Passw0rd1"`). Weak passwords like `"pass123"` now return `InvalidArgument`.
- `ip_address` is `INET` — never pass `""` (use a real IP or omit the column).
- RS256 JWTs are **deterministic** for the same claims + key + second — two tokens minted in
  the same second are identical. Tests that rely on token distinctness (e.g. replay protection)
  should use logout-then-refresh instead of back-to-back refreshes.
- `services/todo` has the same harness (`internal/testutil`), on `pgvector/pgvector:pg16` (must be
  pre-pulled; migrations need `vector` and `pg_trgm`). Without Docker its DB tests skip; set
  `REQUIRE_DOCKER=1` to fail instead. `services/ai` can copy the pattern when it needs one.

---

## Internationalization (i18n)

The project is fully multilingual (English + Simplified Chinese, extensible). **Every new feature must be built with i18n support from day one.**

### Frontend rules

- All user-facing strings use `react-i18next`. Never hardcode display text in components.
- Get the translator: `const { t } = useTranslation()` (or `const { t, i18n } = useTranslation()` when you also need locale-aware dates).
- Add keys to **both** locale files before using them:
  - `web/src/locales/en/translation.json`
  - `web/src/locales/zh/translation.json`
- Key structure: dotted, grouped by feature — e.g. `tasks.form.titleLabel`, `account.sessions.revoke`, `common.save`.
- **Pluralization**: use i18next plural suffixes (`_one` / `_other`). Chinese uses only `_other`. Pass `{ count }` to `t()`.
- **Interpolation**: use `{{varName}}` in JSON, pass `{ varName: value }` to `t()`.
- **Dates**: never pass `undefined` as locale to `toLocaleDateString`/`toLocaleString`. Use `toBCP47(i18n.language)` from `web/src/i18n/config.ts`.
- **Label maps** (like priority/status): store i18n keys, not display strings. Resolve with `t(key)` at render time. See `PriorityLabelKey`, `StatusLabelKey`, `SYSTEM_LISTS[*].nameKey` in `web/src/types/index.ts`.
- **Arrays of strings** (e.g. loading messages): store in JSON as arrays, retrieve with `t('key', { returnObjects: true }) as string[]`.
- Supported locales constant: `SUPPORTED_LANGS` in `web/src/i18n/config.ts`.

### Backend rules

- **Language is persisted** in `auth.users.language` (default `'en'`). It flows via `ValidateToken` → gateway middleware → request context.
- Use `pkg/i18n.Normalize(lang)` to canonicalize any incoming language string. Use `pkg/i18n.Supported(lang)` to validate before accepting user input.
- **AI service**: inject language as a directive in the **user message** (never the cached system prompt — that would bust the Claude cache). See `planner.go` / `parser.go`.
- **Kafka events**: denormalize language into the event payload at production time so consumers (notification-service) can localize without a sync auth lookup. Add `Language string \`json:"language"\`` to any new event struct in both the producer and consumer.
- **New auth migrations**: add the files to `migrations/auth/` and run `scripts/sync-migrations.sh`; otherwise the cluster never runs the migration.

### Checklist for any new UI feature

1. Add all string keys to both JSON locale files.
2. Use `t('key')` everywhere — no hardcoded English strings in JSX.
3. For date/number display, pass `toBCP47(i18n.language)` as the locale.
4. For plural strings, use `_one`/`_other` keys and pass `{ count }`.
5. For ZH: review that translated text fits the UI layout (Chinese is typically shorter).

---

## Conventions

- **No ORM** — `pgx/v5` directly + `sqlc` for codegen. SQL lives in `services/todo/internal/store/store.go`.
- **Errors** — bubble up to gRPC handler; log once at the boundary. Use `google.golang.org/grpc/status` codes. Never swallow errors silently.
- **gRPC error extraction in gateway** — `status.FromError(err).Message()` strips `"rpc error: code = X desc = "` prefix before returning to frontend.
- **Outbox** — never publish to Kafka outside a Postgres transaction.
- **JWT** — never in `localStorage`.
- **Pagination** — every store method that returns a list must accept `limit int` and `cursor string`. No unbounded queries in any code path reachable from an HTTP handler.
- **Context propagation** — every DB call, gRPC call, and Kafka produce must receive the request context. Never use `context.Background()` inside a handler.
- **Structured logging** — use `pkg/logger` everywhere. Log at `ERROR` only for actionable failures. `WARN` for degraded-but-handled. `INFO` for lifecycle events. Never log sensitive fields (passwords, tokens, PII).
- **Migrations are append-only** — see Ops/runbook for the two-step removal process. Never alter column types or drop constraints in a single deploy.

## Don't

- Don't propose ORMs (gorm, ent).
- Don't expose gRPC to the browser.
- Don't publish Kafka events outside a DB transaction.
- Don't store JWT access tokens in localStorage.
- Don't add `dark:` Tailwind classes without also having the default (light) class.
- Don't hardcode user-facing strings in React components — always use `t('key')` with entries in both locale files.
- Don't interpolate language into a Claude system prompt that has `CacheControlEphemeral` — put the language directive in the user message instead.
- Don't write unbounded SQL queries (`SELECT * FROM tasks WHERE user_id = $1` with no `LIMIT`). Always paginate.
- Don't call `os.Exit` outside `main`. Panics and unexpected exits skip graceful shutdown and lose in-flight data.
- Don't create Kafka producers or DB connections inside request handlers. Use the shared pool/client injected at startup.
- Don't run synchronous Kafka produce calls in the HTTP request path — write to the outbox table instead.
- Don't use `context.Background()` inside any code that has a request context available.
- Don't add a NOT NULL column without a default to an existing table in a single migration — it locks the table.
- Don't lower Kafka producer `acks` or `retries` settings — at-least-once delivery is non-negotiable.
