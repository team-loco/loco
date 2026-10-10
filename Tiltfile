# Loco local development environment
# Prerequisites: docker (OrbStack or Docker Desktop) and mise. Every other tool is
# pinned in mise.toml, and every resource below runs a mise task or builds a Dockerfile.
# Run:  mise run tilt
# Stop: tilt down  (stops the compose services; the helm releases, the kind cluster and the database volume persist; mise run helm:destroy removes the releases)
#
# First-time setup:
#   1. mise run setup
#   2. Copy .env.example to .env and fill in secrets (or ensure .env is populated)

kind_context = 'kind-' + read_yaml('env/local/kind-cluster.yml')['name']
allow_k8s_contexts(kind_context)
if k8s_context() != kind_context:
    fail('Tilt is on Kubernetes context %s instead of %s; start it with mise run tilt' % (k8s_context(), kind_context))
update_settings(k8s_upsert_timeout_secs=600, max_parallel_updates=5)

# ---------------------------------------------------------------------------
# Setup: helm repos + chart dependencies
# ---------------------------------------------------------------------------

local_resource(
    'helm-deps',
    cmd='mise run helm:deps',
    deps=[
        'charts/loco-core/Chart.yaml',
        'charts/loco-core/Chart.lock',
        'charts/loco-networking/Chart.yaml',
        'charts/loco-networking/Chart.lock',
        'charts/loco-obs/Chart.yaml',
        'charts/loco-obs/Chart.lock',
    ],
    allow_parallel=True,
    labels=['setup'],
)

# ---------------------------------------------------------------------------
# Images: built from this checkout with the Dockerfiles CI ships, loaded into kind
# ---------------------------------------------------------------------------

docker_build(
    'loco-agent',
    '.',
    dockerfile='agent/Dockerfile',
    only=['agent', 'gen/go', 'k8sapi', 'internal/buildinfo', 'internal/loglevel', 'go.mod', 'go.sum'],
)

docker_build(
    'loco-obs-proxy',
    '.',
    dockerfile='observability-proxy/Dockerfile',
    only=['observability-proxy', 'gen/go', 'internal/buildinfo', 'go.mod', 'go.sum'],
)

docker_build(
    'loco-controller',
    '.',
    dockerfile='controller/Dockerfile',
    only=['controller', 'k8sapi'],
)

docker_build(
    'loco-builder',
    '.',
    dockerfile='builder/Dockerfile',
    only=['builder'],
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
# Infrastructure: Postgres, Valkey, the identity provider, the image registry and the source bucket from compose.yaml
# ---------------------------------------------------------------------------

docker_compose('compose.yaml', project_name='loco-dev')

dc_resource('postgres', labels=['infrastructure'])
dc_resource('valkey', labels=['infrastructure'])
dc_resource('registry', labels=['infrastructure'])
dc_resource('s3', labels=['infrastructure'])
dc_resource('dex', labels=['infrastructure'])

local_resource(
    'cluster-registry',
    cmd='mise run cluster:registry',
    resource_deps=['registry', 's3', 'helm-namespaces'],
    deps=['mise-tasks/cluster/registry'],
    allow_parallel=True,
    labels=['infrastructure'],
)

# ---------------------------------------------------------------------------
# Infrastructure: DB migrations + seed data, applied from a container
# ---------------------------------------------------------------------------

local_resource(
    'db-migrate',
    cmd='mise run db:migrate',
    resource_deps=['postgres'],
    deps=['api/migrations/', 'api/seed/'],
    allow_parallel=True,
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
    allow_parallel=True,
    labels=['infra'],
)

# ---------------------------------------------------------------------------
# Phase 2: Core (cert-manager + Envoy Gateway + loco-core + loco-operator)
# ---------------------------------------------------------------------------

local_resource(
    'helm-namespaces',
    cmd='mise run helm:sync:namespaces',
    deps=['manifests/namespaces/'],
    allow_parallel=True,
    labels=['infra'],
)

local_resource(
    'helm-cert-manager',
    cmd='mise run helm:sync:cert-manager',
    resource_deps=['helm-networking'],
    allow_parallel=True,
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
        'env.AGENT_TOKEN=$AGENT_TOKEN',
        'env.CONTROL_PLANE_URL=' + control_plane_url,
    ],
    deps=['charts/loco-core/', 'env/local/core-chart.yaml.gotmpl'],
    resource_deps=['helm-namespaces', 'helm-cert-manager', 'loco-operator'],
)

helm_release(
    'loco-operator',
    'loco-system',
    images=[{
        'name': 'loco-controller',
        'repository': 'controller.image.repository',
        'tag': 'controller.image.tag',
        'pull_policy': 'controller.image.pullPolicy',
    }, {
        'name': 'loco-builder',
        'repository': 'builds.builderImage.repository',
        'tag': 'builds.builderImage.tag',
        'pull_policy': 'builds.builderImage.pullPolicy',
    }],
    values=[],
    deps=['charts/loco-operator/', 'env/local/operator-chart.yaml.gotmpl', 'mise-tasks/cluster/build-egress'],
    resource_deps=['helm-networking', 'cluster-registry'],
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
    deps=['api/', 'gen/go/', 'k8sapi/', 'internal/buildinfo/', 'internal/loglevel/', 'go.mod', 'go.sum'],
    resource_deps=['db-migrate', 'valkey', 's3', 'dex'],
    allow_parallel=True,
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
    allow_parallel=True,
    labels=['services'],
)
