# Tilt config — local dev loop on kind.
#
# What Tilt does for us:
#   1. Compiles the Go binary on the host (fast, uses host Go cache).
#   2. Packs it into a tiny distroless image and loads it into kind.
#   3. On subsequent saves: syncs only the binary + restarts the process
#      (no full image rebuild — typically ~2s loop).
#   4. Re-applies Kustomize manifests on YAML changes.
#   5. Surfaces logs from every pod in the Tilt UI (http://localhost:10350).
#
# Usage:
#   ./scripts/kind-up.sh    # one-time cluster setup
#   /usr/local/bin/tilt up  # start dev loop (alias tilt=/usr/local/bin/tilt)

# restart_process replaces the deprecated restart_container() live_update step.
# It injects a tiny wrapper binary into the image that watches for a signal
# file written by the sync, then re-execs the entrypoint.
load('ext://restart_process', 'docker_build_with_restart')

# Restrict Tilt to the kind cluster we created.
allow_k8s_contexts('kind-todo')

# ----------------------------------------------------------------------------
# Gateway
# ----------------------------------------------------------------------------
# Compile the gateway binary on the host, then pack it into a tiny image and
# live-sync it on subsequent rebuilds. This is dramatically faster than
# rebuilding inside the container on every save.

local_resource(
    'gateway-compile',
    cmd='CGO_ENABLED=0 GOOS=linux go build -o build/gateway ./cmd/gateway',
    dir='services/gateway',
    deps=[
        'services/gateway/cmd',
        'services/gateway/internal',
        'services/gateway/go.mod',
        'pkg',
    ],
    labels=['gateway'],
)

docker_build_with_restart(
    'todo/gateway',
    context='.',
    # Dev base: alpine (has `touch`, `sh`, `chmod` that restart_process needs).
    # Production builds use services/gateway/Dockerfile (distroless, smaller).
    dockerfile_contents='''
FROM alpine:3.20
COPY services/gateway/build/gateway /gateway
USER 65534
ENTRYPOINT ["/gateway"]
''',
    entrypoint='/gateway',
    only=['services/gateway/build/gateway'],
    live_update=[
        sync('services/gateway/build/gateway', '/gateway'),
    ],
)

# ----------------------------------------------------------------------------
# Manifests
# ----------------------------------------------------------------------------
k8s_yaml(kustomize('deploy/k8s/base/caddy'))
k8s_yaml(kustomize('deploy/k8s/base/gateway'))

k8s_resource('caddy',   labels=['edge'],    port_forwards=[])
k8s_resource('gateway', labels=['gateway'], resource_deps=['gateway-compile'])

# Phase 1 hint:
# load('ext://restart_process', 'docker_build_with_restart')  # already loaded
# k8s_yaml(kustomize('deploy/k8s/base/auth'))
# k8s_yaml(kustomize('deploy/k8s/base/todo'))
