# Loco local development environment
# Prerequisites: docker (OrbStack or Docker Desktop) and mise. Every other tool is
# pinned in mise.toml, and every resource below runs a mise task or builds a Dockerfile.
# Run:  mise run tilt
# Stop: tilt down  (stops the compose services; the helm releases, the kind cluster and the database volume persist; mise run helm:destroy removes the releases)
#
# First-time setup:
#   1. mise run setup
#   2. Copy .env.example to .env and fill in secrets (or ensure .env is populated)

# ---------------------------------------------------------------------------
# Docker socket — auto-detect OrbStack, fall back to Docker Desktop default
# ---------------------------------------------------------------------------

home = os.environ.get('HOME', '')
orbstack_sock = home + '/.orbstack/run/docker.sock'
if os.environ.get('DOCKER_HOST', '') == '' and os.path.exists(orbstack_sock):
    os.environ['DOCKER_HOST'] = 'unix://' + orbstack_sock

allow_k8s_contexts('kind-loco-cluster-local')
update_settings(k8s_upsert_timeout_secs=600)

# ---------------------------------------------------------------------------
# Setup: Docker and the pinned tools
# ---------------------------------------------------------------------------

local_resource(
    'doctor',
    cmd='mise run doctor',
    labels=['setup'],
)

# ---------------------------------------------------------------------------
# Setup: kind cluster
# ---------------------------------------------------------------------------

local_resource(
    'kind-cluster',
    cmd='mise run cluster:up',
    resource_deps=['doctor'],
    labels=['setup'],
)

# ---------------------------------------------------------------------------
# Setup: helm repos + chart dependencies
# ---------------------------------------------------------------------------

local_resource(
    'helm-deps',
    cmd='mise run helm:deps',
    resource_deps=['kind-cluster'],
    labels=['setup'],
)

# ---------------------------------------------------------------------------
# Images: built from this checkout with the Dockerfiles CI ships, loaded into kind
# ---------------------------------------------------------------------------

docker_build(
    'loco-agent',
    '.',
    dockerfile='agent/Dockerfile',
    only=['agent', 'gen/go', 'k8sapi', 'go.mod', 'go.sum'],
)

docker_build(
    'loco-obs-proxy',
    '.',
    dockerfile='observability-proxy/Dockerfile',
    only=['observability-proxy', 'gen/go', 'go.mod', 'go.sum'],
)

docker_build(
    'loco-controller',
    '.',
    dockerfile='controller/Dockerfile',
    only=['controller', 'k8sapi'],
)

control_plane_url = 'http://host.docker.internal:${APP_PORT##*:}'


def helm_release(name, namespace, images, values, deps, resource_deps):
    sets = []
    for i, image in enumerate(images):
        ref = 'TILT_IMAGE_' + str(i)
        sets += [
            image['repository'] + '=${' + ref + '%:*}',
            image['tag'] + '=${' + ref + '##*:}',
            image['pull_policy'] + '=IfNotPresent',
        ]
    sets += values
    k8s_custom_deploy(
        name,
        apply_cmd=' '.join(['mise', 'run', 'tilt:deploy', name, namespace] + ['"' + v + '"' for v in sets]),
        delete_cmd='true',
        deps=deps,
        image_deps=[image['name'] for image in images],
    )
    k8s_resource(name, resource_deps=resource_deps, labels=['infra'])


# ---------------------------------------------------------------------------
# Infrastructure: Postgres and Valkey from compose.yaml
# ---------------------------------------------------------------------------

docker_compose('compose.yaml', project_name='loco-dev')

dc_resource('postgres', resource_deps=['doctor'], labels=['infrastructure'])
dc_resource('valkey', resource_deps=['doctor'], labels=['infrastructure'])

# ---------------------------------------------------------------------------
# Infrastructure: DB migrations + seed data, applied from a container
# ---------------------------------------------------------------------------

local_resource(
    'db-migrate',
    cmd='mise run db:migrate',
    resource_deps=['postgres'],
    deps=['api/migrations/', 'api/seed/'],
    labels=['infrastructure'],
)

# ---------------------------------------------------------------------------
# Phase 1: Networking (Cilium)
# ---------------------------------------------------------------------------

local_resource(
    'helm-networking',
    cmd='mise run helm:sync:networking',
    resource_deps=['helm-deps'],
    deps=[
        'charts/loco-networking/',
        'env/local/networking-chart.yaml.gotmpl',
    ],
    labels=['infra'],
)

# ---------------------------------------------------------------------------
# Phase 2: Core (cert-manager + Envoy Gateway + loco-core + loco-controller)
# ---------------------------------------------------------------------------

local_resource(
    'helm-namespaces',
    cmd='mise run helm:sync:namespaces',
    resource_deps=['kind-cluster'],
    deps=['manifests/namespaces/'],
    labels=['infra'],
)

local_resource(
    'helm-cert-manager',
    cmd='mise run helm:sync:cert-manager',
    resource_deps=['helm-networking'],
    labels=['infra'],
)

helm_release(
    'loco-core',
    'loco-system',
    images=[{
        'name': 'loco-agent',
        'repository': 'agent.image.repository',
        'tag': 'agent.image.tag',
        'pull_policy': 'agent.imagePullPolicy',
    }],
    values=[
        'global.replicas.ui=0',
        'env.AGENT_TOKEN=$AGENT_TOKEN',
        'env.CONTROL_PLANE_URL=' + control_plane_url,
    ],
    deps=['charts/loco-core/', 'env/local/core-chart.yaml.gotmpl'],
    resource_deps=['helm-namespaces', 'helm-cert-manager'],
)

helm_release(
    'loco-controller',
    'loco-system',
    images=[{
        'name': 'loco-controller',
        'repository': 'manager.image.repository',
        'tag': 'manager.image.tag',
        'pull_policy': 'manager.image.pullPolicy',
    }],
    values=[],
    deps=['charts/loco-controller/', 'env/local/controller-chart.yaml.gotmpl'],
    resource_deps=['loco-core'],
)

# ---------------------------------------------------------------------------
# Phase 3: Observability (ClickHouse + OpenTelemetry + Grafana)
# ---------------------------------------------------------------------------

helm_release(
    'loco-obs',
    'observability',
    images=[{
        'name': 'loco-obs-proxy',
        'repository': 'obsProxy.image.repository',
        'tag': 'obsProxy.image.tag',
        'pull_policy': 'obsProxy.imagePullPolicy',
    }],
    values=['obsProxy.controlPlane.url=' + control_plane_url],
    deps=['charts/loco-obs/', 'env/local/obs-chart.yaml.gotmpl'],
    resource_deps=['loco-core'],
)

# ---------------------------------------------------------------------------
# Services — processes on the host, rebuilt and restarted when their sources change
# ---------------------------------------------------------------------------

local_resource(
    'api',
    cmd='mise run build:api',
    serve_cmd='api/bin/loco-api',
    deps=['api/', 'gen/go/', 'k8sapi/', 'go.mod', 'go.sum'],
    resource_deps=['db-migrate', 'valkey'],
    labels=['services'],
)

local_resource(
    'ui',
    serve_cmd='mise run ui',
    labels=['services'],
)

local_resource(
    'cli',
    cmd='mise run build',
    deps=['main.go', 'cmd/', 'internal/', 'gen/go/', 'go.mod', 'go.sum'],
    labels=['services'],
)
