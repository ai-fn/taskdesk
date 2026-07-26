# Contributing

Thanks for considering a contribution! This is a production-shaped Go microservices todo app built primarily as a learning vehicle for gRPC, Kafka, Kubernetes, observability, and AI integration (see [README](./README.md) for the full picture and phase status). Bug reports, feature ideas, docs fixes, and code are all welcome.

For architecture, service boundaries, and hard project conventions (outbox pattern, i18n rules, migration steps, etc.), read [`CLAUDE.md`](./CLAUDE.md) first — it's the source of truth and most PRs will need to follow it.

## Ways to contribute

- **Bug reports** — found something broken? Open an issue with repro steps.
- **Feature requests** — open an issue to discuss before submitting a large PR; this project follows a phased plan (see the Status table in the README), so it helps to check where a feature fits.
- **Code** — bug fixes, new features, performance improvements.
- **Docs** — typo fixes, clarifications, missing setup steps.

## Getting started

```bash
git clone https://github.com/ai-fn/taskdesk.git
cd taskdesk

# 1. Create the kind cluster and install infra dependencies
./scripts/kind-up.sh

# 2. Install Kafka (Strimzi operator + single-broker cluster)
./scripts/install-kafka.sh

# 3. Start the dev loop (compiles Go, loads images into kind, hot-reloads on save)
tilt up
```

Prerequisites (see README for install commands): Go ≥ 1.23, Docker, kind ≥ 0.24, kubectl ≥ 1.30, helm ≥ 3.14, Tilt ≥ 0.33, Node ≥ 20 (for `web/`).

Useful once the cluster is up:

- `./scripts/port-forwards.sh` — forwards gateway:8080, Grafana:3000, Kibana:5601, Mailpit:8025 (and direct Postgres/Redis access).
- `./scripts/seed.sh` — seed a test user with sample data.
- `./scripts/proto-gen.sh` — regenerate `proto/gen/` after editing any `.proto` file. **Run this after any proto change** — generated code must stay in sync, and `proto/gen/go.mod` needs to be checked/tidied afterward since the script wipes the directory.

## Branching & workflow

- Branch directly off `main` (or fork if you don't have write access).
- Branch naming follows `<type>/<short-description>`, e.g. `feat/attachments-multipart`, `fix/todo-service-startup`, `docs/contributing-guide`.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/): `feat(scope): ...`, `fix(scope): ...`, `docs(scope): ...` — scope is usually the service or area touched (`web`, `todo`, `infra`, `notification`, etc.).
- Keep PRs focused — one logical change per PR.
- Rebase onto `main` before opening a PR if your branch has fallen behind.

## Code style

There's no linter/formatter wired into CI yet, so please self-check before pushing:

- **Go**: run `gofmt -l .` and `go vet ./...` from the module root; keep code idiomatic per `go-reviewer` conventions (explicit error handling, no `context.Background()` inside handlers, no swallowed errors — see the "Conventions" and "Don't" sections in `CLAUDE.md`).
- **Frontend (`web/`)**: run `npx tsc -b` (via `npm run build`) to catch type errors; follow existing component/hook patterns. Every new user-facing string needs an entry in both `web/src/locales/en/translation.json` and `web/src/locales/zh/translation.json` — see the i18n section in `CLAUDE.md`.
- **Proto changes**: always run `./scripts/proto-gen.sh` after editing a `.proto` file, and never hand-edit generated code under `proto/gen/`.

## Testing

This project uses stdlib `testing` only (no testify), table-driven where applicable, across three layers — see the "Testing" section in `CLAUDE.md` for full details.

```bash
# Layer 1 + 3 — pure unit + gateway httptest (no Docker required)
go test ./pkg/tokens/ ./services/gateway/...

# Full suite — needs Docker + postgres:16-alpine pre-pulled
go test ./pkg/... && go test ./services/auth/... -timeout 120s && go test ./services/gateway/...
```

Add or update tests for any new functionality or bug fix. There's no CI pipeline yet (planned — see README Status table), so running the relevant test layers locally before opening a PR is currently the only gate.

## Submitting a pull request

1. Open a PR against `main`.
2. Describe **what** changed and **why**; link any related issue with `Closes #123` or `Relates to #123`.
3. Note which test layers you ran locally (no CI to fall back on yet).
4. Expect review within a few days — changes requested are normal, not a rejection.

## Reporting issues

When filing a bug report, please include:

- **Steps to reproduce**
- **Expected behavior**
- **Actual behavior**
- **Environment** — OS, Go/Node version, kind/Tilt version, and whether it reproduces via `tilt up` or in a specific service's unit tests

Feature requests should describe the problem you're trying to solve, not just the solution you have in mind — makes discussion easier, especially against the existing phased roadmap.

## Code of conduct

Be respectful and constructive in issues, PRs, and reviews — assume good faith, keep feedback focused on the code.

## Questions

Not sure where to start, or have a question that isn't a bug or feature request? Open an issue.
