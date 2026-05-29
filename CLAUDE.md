# Project context for Claude

**This is a learning project.** The goal is not the todo app itself — it's hands-on exposure to the Go microservices production stack. Optimize suggestions for clarity and pedagogy, not shortest-path delivery.

---

## Current state (as of 2026-05-19)

Phases 0–4 complete. The full stack is running in a kind cluster with Tilt live-reload.

| Phase | Status | What was built |
|-------|--------|----------------|
| 0 — Bootstrap | ✅ | kind cluster, Tilt, Caddy, Postgres, Redis, Kafka, gateway skeleton |
| 1 — Auth + Todo MVP | ✅ | auth-service, todo-service, gateway, React SPA with full CRUD |
| 2 — Async backbone | ✅ | Kafka topics, transactional outbox, notification-service (email + WebSocket) |
| 3 — AI daily plan | ✅ | ai-service with Claude, streaming SSE plan panel, scheduler CronJob |
| 4 — Observability | ✅ | ELK stack, Prometheus/Grafana, slog JSON with trace IDs |
| Lists + Groups | ✅ | Custom task lists, list groups, system virtual lists, drag-to-reorder |
| UI polish | ✅ | Dark/light/system theme, SSE word-by-word streaming, user identity in header |
| Sessions | ✅ | Session management page (list devices/IPs, revoke, sign-out-all), `externalTrafficPolicy: Local` real-IP chain |
| Backend tests | ✅ | Three-layer test suite: pure unit, store/server integration (testcontainers), gateway httptest+fakes |

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

---

## Services

### `services/auth`
Auth, users, JWTs. Owns `auth.*` schema.

**RPCs:** `SignUp`, `Login`, `RefreshToken`, `Logout`, `GetJWKS`, `ValidateToken`, `ListUserIDs`, `GetUserByID`

**DB tables:**
- `auth.users` — id, email, name, password_hash (argon2id), created_at, updated_at
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
go test ./services/gateway/internal/middleware/ # Authenticate: 401/503, context keys, ?token=
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
- `ip_address` is `INET` — never pass `""` (use a real IP or omit the column).
- RS256 JWTs are **deterministic** for the same claims + key + second — two tokens minted in
  the same second are identical. Tests that rely on token distinctness (e.g. replay protection)
  should use logout-then-refresh instead of back-to-back refreshes.
- The same pattern (testutil harness + TestMain + Truncate) can be copied to `services/todo`
  and `services/ai` when those stores need integration coverage.

---

## Conventions

- **No ORM** — `pgx/v5` directly + `sqlc` for codegen. SQL lives in `services/todo/internal/store/store.go`.
- **Errors** — bubble up to gRPC handler; log once at the boundary. Use `google.golang.org/grpc/status` codes.
- **gRPC error extraction in gateway** — `status.FromError(err).Message()` strips `"rpc error: code = X desc = "` prefix before returning to frontend.
- **Outbox** — never publish to Kafka outside a Postgres transaction.
- **JWT** — never in `localStorage`.

## Don't

- Don't propose ORMs (gorm, ent).
- Don't expose gRPC to the browser.
- Don't publish Kafka events outside a DB transaction.
- Don't store JWT access tokens in localStorage.
- Don't add `dark:` Tailwind classes without also having the default (light) class.
