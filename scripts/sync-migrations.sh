#!/usr/bin/env bash
# migrations/<svc>/ is the single source; this copies it into deploy/k8s/base/<svc>/ and
# regenerates the <svc>-migrations configMapGenerator list. `--check` only reports drift.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
check=0
case "${1:-}" in
  "") ;;
  --check) check=1 ;;
  *) echo "usage: $0 [--check]" >&2; exit 2 ;;
esac

drift=0
for svc in auth todo ai; do
  src="$root/migrations/$svc"
  dst="$root/deploy/k8s/base/$svc"
  kust="$dst/kustomization.yaml"
  ls "$src"/*.sql >/dev/null 2>&1 || { echo "no migrations in $src" >&2; exit 1; }
  # .up before .down, matching how the lists were written by hand.
  files=$(cd "$src" && ls -1 *.sql | sed 's/\.down\.sql$/.zdown.sql/' | LC_ALL=C sort | sed 's/\.zdown\.sql$/.down.sql/')

  stale=$(cd "$dst" && ls -1 *.sql 2>/dev/null | LC_ALL=C sort | comm -13 <(echo "$files" | LC_ALL=C sort) - || true)

  # Rewrite the "files:" list of the <svc>-migrations generator, leave the rest as is.
  tmp=$(mktemp)
  LIST="$files" awk -v svc="$svc" '
    $0 == "  - name: " svc "-migrations" { found = 1; print; print "    files:"; n = split(ENVIRON["LIST"], f, "\n"); for (i = 1; i <= n; i++) print "      - " f[i]; skip = 1; next }
    skip && (/^    files:/ || /^      - /) { next }
    { skip = 0; print }
    END { if (!found) exit 3 }
  ' "$kust" >"$tmp"

  if [ "$check" = 1 ]; then
    for f in $files; do
      cmp -s "$src/$f" "$dst/$f" || { echo "drift: deploy/k8s/base/$svc/$f"; drift=1; }
    done
    for f in $stale; do echo "stale: deploy/k8s/base/$svc/$f"; drift=1; done
    cmp -s "$tmp" "$kust" || { echo "drift: deploy/k8s/base/$svc/kustomization.yaml"; drift=1; }
    rm -f "$tmp"
  else
    for f in $stale; do rm -f "$dst/$f"; done
    # Touch only what changed so Tilt's file watcher doesn't loop on its own output.
    for f in $files; do cmp -s "$src/$f" "$dst/$f" || cp "$src/$f" "$dst/$f"; done
    cmp -s "$tmp" "$kust" || cat "$tmp" >"$kust"
    rm -f "$tmp"
  fi
done

if [ "$drift" = 1 ]; then
  echo "run scripts/sync-migrations.sh" >&2
  exit 1
fi
