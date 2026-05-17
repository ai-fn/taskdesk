#!/usr/bin/env bash
# seed.sh — fill the DB with realistic dummy data for one user.
# Usage:
#   ./scripts/seed.sh                          # uses the first user in auth.users
#   USER_ID=<uuid> ./scripts/seed.sh           # explicit user
#
# Idempotent: uses ON CONFLICT DO NOTHING, safe to re-run.

set -euo pipefail

PSQL_POD=$(kubectl -n data get pod -l app=postgres -o jsonpath='{.items[0].metadata.name}')
PSQL="kubectl -n data exec -i $PSQL_POD -- psql -U postgres -d todo"

# Resolve user
if [[ -z "${USER_ID:-}" ]]; then
  USER_ID=$($PSQL -t -c "SELECT id FROM auth.users LIMIT 1;" | tr -d '[:space:]')
fi

if [[ -z "$USER_ID" ]]; then
  echo "No users found. Sign up first, then run this script." >&2
  exit 1
fi

echo "Seeding for user: $USER_ID"

$PSQL <<SQL

-- ─── Groups ───────────────────────────────────────────────────────────────────
INSERT INTO todo.list_groups (id, user_id, name, position) VALUES
  ('seed-grp-work-0001', '$USER_ID', 'Work',     0),
  ('seed-grp-pers-0001', '$USER_ID', 'Personal', 1)
ON CONFLICT (id) DO NOTHING;

-- ─── Lists ────────────────────────────────────────────────────────────────────
INSERT INTO todo.lists (id, user_id, name, color, icon, position, group_id) VALUES
  ('seed-lst-proj-0001', '$USER_ID', 'Projects',      '#007AFF', '💼', 0, 'seed-grp-work-0001'),
  ('seed-lst-urgt-0001', '$USER_ID', 'Urgent',        '#FF3B30', '🔥', 1, 'seed-grp-work-0001'),
  ('seed-lst-meet-0001', '$USER_ID', 'Meetings',      '#8E8E93', '📝', 2, 'seed-grp-work-0001'),
  ('seed-lst-home-0001', '$USER_ID', 'Home',          '#34C759', '🏠', 0, 'seed-grp-pers-0001'),
  ('seed-lst-shop-0001', '$USER_ID', 'Shopping',      '#FF9500', '🛒', 1, 'seed-grp-pers-0001'),
  ('seed-lst-goal-0001', '$USER_ID', 'Goals',         '#5856D6', '🎯', 2, 'seed-grp-pers-0001'),
  ('seed-lst-read-0001', '$USER_ID', 'Reading List',  '#30B0C7', '📚', 3, 'seed-grp-pers-0001')
ON CONFLICT (id) DO NOTHING;

-- ─── Tasks ────────────────────────────────────────────────────────────────────
-- status: 1=TODO  2=IN_PROGRESS  3=DONE
-- priority: 0=UNSPECIFIED  1=LOW  2=MEDIUM  3=HIGH  4=URGENT

INSERT INTO todo.tasks (id, user_id, title, description, priority, status, tags, due_at, list_id, flagged) VALUES

-- Projects list
  ('seed-tsk-0001', '$USER_ID', 'Implement user auth flow',
   'JWT access tokens + refresh tokens stored in HttpOnly cookies.',
   3, 3, '{"backend","auth"}', now() - interval '2 days', 'seed-lst-proj-0001', false),

  ('seed-tsk-0002', '$USER_ID', 'Set up CI/CD pipeline',
   'GitHub Actions: lint, test, build, push images on PR; deploy on main.',
   2, 2, '{"devops","infra"}', now() + interval '3 days', 'seed-lst-proj-0001', false),

  ('seed-tsk-0003', '$USER_ID', 'Write API documentation',
   'Document all REST endpoints with request/response examples.',
   1, 1, '{"docs"}', now() + interval '7 days', 'seed-lst-proj-0001', false),

  ('seed-tsk-0004', '$USER_ID', 'Add integration tests for todo service',
   'Use testcontainers-go with a real Postgres instance.',
   2, 1, '{"testing","backend"}', now() + interval '5 days', 'seed-lst-proj-0001', false),

  ('seed-tsk-0005', '$USER_ID', 'Performance profiling — task list query',
   'Query takes >200ms on large datasets. Check indexes.',
   3, 1, '{"performance","backend"}', now() + interval '1 day', 'seed-lst-proj-0001', true),

-- Urgent list
  ('seed-tsk-0006', '$USER_ID', 'Fix production memory leak',
   'Notification service RSS grows unboundedly over ~12h.',
   4, 2, '{"bug","production"}', now(), 'seed-lst-urgt-0001', true),

  ('seed-tsk-0007', '$USER_ID', 'Renew SSL certificate',
   'Expires in 5 days. Caddy should auto-renew but verify.',
   4, 1, '{"infra","security"}', now() + interval '5 days', 'seed-lst-urgt-0001', true),

  ('seed-tsk-0008', '$USER_ID', 'Hotfix: duplicate Kafka events on retry',
   'Idempotency key missing from outbox publisher.',
   4, 1, '{"bug","kafka"}', now() + interval '1 day', 'seed-lst-urgt-0001', false),

-- Meetings list
  ('seed-tsk-0009', '$USER_ID', 'Sprint planning — Q3 roadmap',
   'Prepare backlog, estimates, and capacity sheet.',
   2, 1, '{"planning"}', now() + interval '2 days', 'seed-lst-meet-0001', false),

  ('seed-tsk-0010', '$USER_ID', 'Write retro notes from last sprint',
   'What went well, what to improve, action items.',
   1, 3, '{"retro"}', now() - interval '1 day', 'seed-lst-meet-0001', false),

  ('seed-tsk-0011', '$USER_ID', '1-on-1 with team lead — prepare talking points',
   'Career growth, blockers, feedback.',
   2, 1, '{"people"}', now() + interval '4 days', 'seed-lst-meet-0001', false),

-- Home list
  ('seed-tsk-0012', '$USER_ID', 'Replace kitchen faucet',
   'Parts already ordered. Need 2h on a weekend.',
   2, 1, '{"home-repair"}', now() + interval '10 days', 'seed-lst-home-0001', false),

  ('seed-tsk-0013', '$USER_ID', 'Schedule annual boiler service',
   'Last service was 14 months ago.',
   3, 1, '{"home","maintenance"}', now() + interval '3 days', 'seed-lst-home-0001', true),

  ('seed-tsk-0014', '$USER_ID', 'Deep clean garage',
   'Sort tools, donate old items, sweep floor.',
   1, 1, '{}', null, 'seed-lst-home-0001', false),

-- Shopping list
  ('seed-tsk-0015', '$USER_ID', 'Buy groceries for the week',
   'Milk, eggs, bread, chicken, vegetables.',
   2, 1, '{"food"}', now() + interval '1 day', 'seed-lst-shop-0001', false),

  ('seed-tsk-0016', '$USER_ID', 'New monitor stand',
   'Ergonomic, fits 27" display, cable management.',
   1, 2, '{"office"}', null, 'seed-lst-shop-0001', false),

  ('seed-tsk-0017', '$USER_ID', 'Birthday gift for sister',
   'She likes hiking gear and cooking books.',
   3, 1, '{"gifts"}', now() + interval '6 days', 'seed-lst-shop-0001', true),

-- Goals list
  ('seed-tsk-0018', '$USER_ID', 'Read 12 books this year',
   'Currently at 4/12. On track.',
   2, 2, '{"reading","self-improvement"}', now() + interval '180 days', 'seed-lst-goal-0001', false),

  ('seed-tsk-0019', '$USER_ID', 'Complete Go microservices project',
   'All phases: auth, tasks, AI, observability, CI/CD.',
   3, 2, '{"coding","learning"}', now() + interval '60 days', 'seed-lst-goal-0001', true),

  ('seed-tsk-0020', '$USER_ID', 'Run a 10k',
   'Currently training at 7k. Build up gradually.',
   2, 2, '{"fitness","health"}', now() + interval '45 days', 'seed-lst-goal-0001', false),

-- Reading List
  ('seed-tsk-0021', '$USER_ID', '"Designing Data-Intensive Applications" — Kleppmann',
   'Chapter 7: Transactions.',
   2, 2, '{"tech","distributed-systems"}', null, 'seed-lst-read-0001', false),

  ('seed-tsk-0022', '$USER_ID', '"The Go Programming Language" — Donovan & Kernighan',
   'Finish chapters 9–11 (concurrency).',
   2, 1, '{"go","learning"}', null, 'seed-lst-read-0001', false),

  ('seed-tsk-0023', '$USER_ID', 'Article: "How Kafka guarantees exactly-once delivery"',
   'Saved from the Confluent blog.',
   1, 1, '{"kafka","distributed-systems"}', null, 'seed-lst-read-0001', false),

-- No list (inbox-style)
  ('seed-tsk-0024', '$USER_ID', 'Reply to conference speaking proposal',
   'Deadline is end of month.',
   3, 1, '{}', now() + interval '8 days', null, true),

  ('seed-tsk-0025', '$USER_ID', 'Expense report for April',
   'Submit receipts to finance.',
   2, 3, '{"admin"}', now() - interval '3 days', null, false)

ON CONFLICT (id) DO NOTHING;

-- A couple of soft-deleted tasks to populate "Recently Deleted"
INSERT INTO todo.tasks (id, user_id, title, description, priority, status, list_id, deleted_at) VALUES
  ('seed-tsk-0026', '$USER_ID', 'Old task — cancelled feature',
   'Product decided not to build this.',
   1, 1, 'seed-lst-proj-0001', now() - interval '1 day'),
  ('seed-tsk-0027', '$USER_ID', 'Draft: weekly status email',
   'Superseded by Slack update.',
   1, 1, 'seed-lst-meet-0001', now() - interval '2 days')
ON CONFLICT (id) DO NOTHING;

SELECT
  (SELECT count(*) FROM todo.list_groups WHERE user_id = '$USER_ID') AS groups,
  (SELECT count(*) FROM todo.lists       WHERE user_id = '$USER_ID') AS lists,
  (SELECT count(*) FROM todo.tasks       WHERE user_id = '$USER_ID') AS tasks;
SQL

echo "Done."
