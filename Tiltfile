# Loco local development environment
# Prerequisites: docker (OrbStack or Docker Desktop), mise, psql. Every other tool is
# pinned in mise.toml, and every resource below runs a mise task.
# Run:  mise run tilt
# Stop: tilt down  (tears down helm releases; kind cluster + Docker containers persist)
#
# First-time setup:
#   1. mise run setup
#   2. Copy .env.example to .env and fill in secrets (or ensure .env is populated)
#   3. AGENT_TOKEN in .env must match the token seeded into the database via seed.sql

# ---------------------------------------------------------------------------
# Docker socket — auto-detect OrbStack, fall back to Docker Desktop default
# ---------------------------------------------------------------------------

home = os.environ.get('HOME', '')
orbstack_sock = home + '/.orbstack/run/docker.sock'
if os.environ.get('DOCKER_HOST', '') == '' and os.path.exists(orbstack_sock):
    os.environ['DOCKER_HOST'] = 'unix://' + orbstack_sock

allow_k8s_contexts('kind-loco-cluster-local')

# ---------------------------------------------------------------------------
# Setup: kind cluster
# ---------------------------------------------------------------------------

local_resource(
    'kind-cluster',
    cmd='mise run cluster:up',
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
# Infrastructure: Postgres
# ---------------------------------------------------------------------------

local_resource(
    'postgres',
    cmd="""
        if docker ps --format '{{.Names}}' | grep -q '^loco-dev-postgres$'; then
            echo "postgres already running"
        elif docker ps -a --format '{{.Names}}' | grep -q '^loco-dev-postgres$'; then
            docker start loco-dev-postgres
        else
            docker run -d \
                --name loco-dev-postgres \
                -e POSTGRES_USER=loco_user \
                -e POSTGRES_PASSWORD=loco_password \
                -e POSTGRES_DB=loco \
                -p 5432:5432 \
                postgres:16-alpine
        fi
        until pg_isready -h localhost -p 5432 -U loco_user >/dev/null 2>&1; do sleep 1; done
    """,
    labels=['infrastructure'],
)

# ---------------------------------------------------------------------------
# Infrastructure: Valkey (Redis-compatible cache)
# ---------------------------------------------------------------------------

local_resource(
    'valkey',
    cmd="""
        if docker ps --format '{{.Names}}' | grep -q '^loco-dev-valkey$'; then
            echo "valkey already running"
        elif docker ps -a --format '{{.Names}}' | grep -q '^loco-dev-valkey$'; then
            docker start loco-dev-valkey
        else
            docker run -d \
                --name loco-dev-valkey \
                -p 6379:6379 \
                valkey/valkey:8-alpine
        fi
    """,
    labels=['infrastructure'],
)

# ---------------------------------------------------------------------------
# Infrastructure: DB migrations + seed data
# Runs all migration SQL files in order, then seeds dev data.
# Errors are suppressed so re-runs are idempotent (tables already exist, etc.)
# ---------------------------------------------------------------------------

local_resource(
    'db-migrate',
    cmd="""
        DB_URL="postgres://loco_user:loco_password@localhost:5432/loco?sslmode=disable"
        for f in $(ls api/migrations/*.sql | sort); do
            psql "$DB_URL" -f "$f" 2>/dev/null || true
        done
        psql "$DB_URL" -f seed.sql 2>/dev/null || true
        echo "migrations applied"
    """,
    resource_deps=['postgres'],
    deps=['api/migrations/', 'seed.sql'],
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
