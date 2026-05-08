# todo

Production-shaped Go microservices todo app. Learning project.

See [`CLAUDE.md`](./CLAUDE.md) for the architecture summary and `~/.claude/plans/need-to-plan-great-zesty-moth.md` for the full phased plan.

## Prerequisites

You'll need these installed on your host:

- **Go** ≥ 1.23
- **Docker** (or Podman with docker shim)
- **kind** ≥ 0.24
- **kubectl** ≥ 1.30
- **helm** ≥ 3.14
- **Tilt** ≥ 0.33 (optional but strongly recommended for inner-loop speed)
- **Node** ≥ 20 (only needed once you start `web/` in Phase 1)

Quick install (Fedora):

```sh
sudo dnf install -y go kubectl
# kind
go install sigs.k8s.io/kind@latest
# helm
curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
# tilt
curl -fsSL https://raw.githubusercontent.com/tilt-dev/tilt/master/scripts/install.sh | bash
```

## Quickstart

```sh
# 1. Bring up the kind cluster + install deps (Caddy, Postgres, Redis, Mailpit, gateway)
./scripts/kind-up.sh

# 2. Deploy services with live-reload via Tilt
tilt up

# 3. Verify Phase 0
curl http://localhost:8080/healthz
# → ok

# Phase 2 prep (when needed): install Strimzi + Kafka
./scripts/install-kafka.sh

# Tear down when done
./scripts/kind-down.sh
```

Tilt UI: http://localhost:10350

## Restricted-network note

If your environment routes Docker Hub through a VPN (or DNS inside the kind
node misbehaves with errors like `lookup registry-1.docker.io … server
misbehaving`), our scripts already work around it: they pull every image on
the **host** (where your VPN is connected) and `kind load docker-image` it
into the cluster. No image is pulled from inside the kind container.

If a `docker pull` step fails, your VPN is the thing to check. Once images
are on the host, `kind load` doesn't touch the network.

## Layout

See [`CLAUDE.md`](./CLAUDE.md).

## Status

- **Phase 0** ✅ bootstrap: kind + Caddy + gateway `/healthz`
- Phase 1 — auth-service + todo-service + SPA
- Phase 2 — Kafka + notification-service
- Phase 3 — ai-service (Claude daily plan)
- Phase 4 — ELK + Prometheus
- Phase 5 — CI + Helm + load testing
