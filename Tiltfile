# Loco local development environment
# Prerequisites: docker (OrbStack or Docker Desktop) and mise. Every other tool is
# pinned in mise.toml, and every resource below runs a mise task.
# Run:  mise run tilt
# Stop: tilt down  (tears down helm releases and the compose services; the kind cluster and the database volume persist)
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
# Setup: controller image — built locally and loaded into kind
# ---------------------------------------------------------------------------

local_resource(
    'loco-controller-image',
    cmd='mise run controller:image',
    deps=[
        'controller/',
        'proto/',
        'k8sapi/',
    ],
    resource_deps=['kind-cluster'],
    labels=['setup'],
)

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
    'helm-core',
    cmd='mise run helm:sync:core',
    resource_deps=['helm-networking', 'loco-controller-image'],
    deps=[
        'charts/loco-core/',
        'charts/loco-controller/',
        'env/local/core-chart.yaml.gotmpl',
        'env/local/controller-chart.yaml.gotmpl',
    ],
    labels=['infra'],
)

# ---------------------------------------------------------------------------
# Phase 3: Observability (ClickHouse + OpenTelemetry + Grafana)
# ---------------------------------------------------------------------------

local_resource(
    'helm-obs',
    cmd='mise run helm:sync:obs',
    resource_deps=['helm-core'],
    deps=[
        'charts/loco-obs/',
        'env/local/obs-chart.yaml.gotmpl',
    ],
    labels=['infra'],
)

# ---------------------------------------------------------------------------
# Services — live-reloading processes
# ---------------------------------------------------------------------------

local_resource(
    'api',
    serve_cmd='mise run reload:api',
    resource_deps=['helm-core', 'db-migrate', 'valkey'],
    labels=['services'],
)

local_resource(
    'agent',
    serve_cmd='mise run reload:agent',
    resource_deps=['helm-core', 'db-migrate'],
    labels=['services'],
)

local_resource(
    'ui',
    serve_cmd='mise run ui',
    labels=['services'],
)

local_resource(
    'obs-proxy',
    serve_cmd='mise run reload:obs-proxy',
    resource_deps=['helm-obs'],
    labels=['services'],
)
