import argparse
import base64
import hashlib
import gzip
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
CHARTS = ('loco-worker', 'loco-control-plane')
CRD_SOURCES = {
    'applications': ('controller/config/crd/bases/infra.loco.io_applications.yaml', '.'),
    'builds': ('controller/config/crd/bases/infra.loco.io_builds.yaml', '.'),
    'gateway': ('charts/loco-worker/Chart.yaml', '.dependencies[] | select(.name == "gateway-helm")'),
}
CRD_VERSIONS = 'tools/platform-release/crds.yaml'
CERT_MANAGER = 'charts/cert-manager.yaml'
CONTENT_ANNOTATION = 'io.loco.platform.content'
IMAGES = ('agent', 'controller', 'builder', 'obs-proxy', 'api', 'ui')
DOCKERFILES = {'agent': 'agent/Dockerfile', 'controller': 'controller/Dockerfile', 'builder': 'builder/Dockerfile', 'obs-proxy': 'observability-proxy/Dockerfile', 'api': 'api/Dockerfile', 'ui': 'web/Dockerfile'}
MAIN_COMMITS = 50
MANIFEST_TYPE = 'application/vnd.oci.image.manifest.v1+json'
LAYER_TYPE = 'application/vnd.oci.image.layer.v1.tar+gzip'
HELM_LAYER_TYPE = 'application/vnd.cncf.helm.chart.content.v1.tar+gzip'
TIMEOUT_SECONDS = 120
VERSION = re.compile(r'(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-rc\.(0|[1-9]\d*))?\Z')
SHA = re.compile(r'[a-f0-9]{40}\Z')
DIGEST = re.compile(r'sha256:[a-f0-9]{64}\Z')


def run(*args, input=None):
    return subprocess.check_output(args, cwd=ROOT, text=True, input=input).strip()


def parse(text, expression='.'):
    return json.loads(run('yq', '-o=json', expression, '-', input=text))


def order(version):
    match = VERSION.fullmatch(version)
    if not match:
        raise ValueError(f'version must be X.Y.Z or X.Y.Z-rc.N without a v prefix: {version}')
    major, minor, patch, _, rc = match.groups()
    return (int(major), int(minor), int(patch), rc is None, int(rc or 0))


def encoded(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def digest(data):
    return 'sha256:' + hashlib.sha256(data).hexdigest()


def archive(files):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode='w') as tar:
        for name, data in sorted(files.items()):
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mode = 0o644
            tar.addfile(info, io.BytesIO(data))
    return gzip.compress(output.getvalue(), mtime=0)


def content(package):
    with tarfile.open(fileobj=io.BytesIO(package)) as tar:
        files = {member.name: tar.extractfile(member).read() for member in tar.getmembers() if member.isfile()}
    return digest(archive(files))


class Registry:
    def __init__(self, repository, username, password, host='ghcr.io'):
        if not re.fullmatch(r'[a-z0-9-]+/[a-z0-9_.-]+', repository):
            raise ValueError('repository must be a lowercase owner/repository')
        self.repository = repository
        self.username = username
        self.password = password
        self.host = host
        # Only a registry on this machine is served over plain HTTP.
        self.base = ('http://' if host.startswith('localhost:') else 'https://') + host
        self.tokens = {}

    def request(self, method, path, data=None, headers=None, missing=False):
        url = self.base + path
        request_headers = dict(headers or {})
        actions = 'pull' if method in ('GET', 'HEAD') else 'pull,push'
        scope = 'repository:' + path.split('/v2/')[1].split('/manifests/')[0].split('/blobs/')[0] + ':' + actions
        authorization = self.tokens.get(scope)
        if authorization:
            request_headers['Authorization'] = authorization
        for attempt in range(2):
            req = urllib.request.Request(url, data=data, headers=request_headers, method=method)
            try:
                with urllib.request.urlopen(req, timeout=TIMEOUT_SECONDS) as response:
                    return response.read(), {key.lower(): value for key, value in response.headers.items()}
            except urllib.error.HTTPError as error:
                error_body = error.read()
                error.close()
                if error.code == 404 and missing:
                    try:
                        errors = json.loads(error_body).get('errors', [])
                    except (ValueError, AttributeError) as invalid:
                        raise RuntimeError('registry returned an invalid absence response') from invalid
                    if errors and all(item.get('code') in ('MANIFEST_UNKNOWN', 'NAME_UNKNOWN', 'BLOB_UNKNOWN') for item in errors):
                        return None, {}
                    raise RuntimeError('registry absence is not confirmed') from error
                if error.code != 401 or attempt:
                    raise RuntimeError(f'registry {method} failed with HTTP {error.code}') from error
                challenge = error.headers.get('WWW-Authenticate', '')
                auth = base64.b64encode((self.username + ':' + self.password).encode()).decode()
                if challenge.startswith('Basic ') and self.password:
                    self.tokens[scope] = 'Basic ' + auth
                    request_headers['Authorization'] = self.tokens[scope]
                    continue
                fields = dict(re.findall(r'(\w+)="([^"]+)"', challenge))
                if fields.get('realm') != self.base + '/token' or fields.get('service') != self.host:
                    raise RuntimeError('unexpected registry authentication challenge') from error
                query = urllib.parse.urlencode({'service': self.host, 'scope': scope})
                auth_headers = {'Authorization': 'Basic ' + auth} if self.password else {}
                auth_req = urllib.request.Request(fields['realm'] + '?' + query, headers=auth_headers)
                with urllib.request.urlopen(auth_req, timeout=TIMEOUT_SECONDS) as response:
                    token = json.load(response)['token']
                self.tokens[scope] = 'Bearer ' + token
                request_headers['Authorization'] = self.tokens[scope]
        raise RuntimeError('registry authentication failed')

    def manifest(self, repository, reference, missing=False):
        data, headers = self.request('GET', f'/v2/{repository}/manifests/{reference}', headers={'Accept': ','.join((MANIFEST_TYPE, 'application/vnd.oci.image.index.v1+json', 'application/vnd.docker.distribution.manifest.list.v2+json', 'application/vnd.docker.distribution.manifest.v2+json'))}, missing=missing)
        if data is None:
            return None
        result = digest(data)
        if headers.get('docker-content-digest', result) != result:
            raise RuntimeError('registry manifest digest mismatch')
        return result

    def require_image_platforms(self, repository, reference):
        data, _ = self.request('GET', f'/v2/{repository}/manifests/{reference}', headers={'Accept': 'application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json'})
        if digest(data) != reference:
            raise RuntimeError('registry image index digest mismatch')
        platforms = [item.get('platform', {}) for item in json.loads(data).get('manifests', [])]
        for architecture in ('amd64', 'arm64'):
            matches = [item for item in platforms if item.get('os') == 'linux' and item.get('architecture') == architecture]
            if len(matches) != 1:
                raise RuntimeError(f'platform image must contain exactly one linux/{architecture} manifest')

    def annotations(self, repository, reference):
        data, _ = self.request('GET', f'/v2/{repository}/manifests/{reference}', headers={'Accept': MANIFEST_TYPE}, missing=True)
        return None if data is None else json.loads(data).get('annotations', {})

    def ensure(self, repository, version, data, config, layer_type=LAYER_TYPE):
        existing = self.annotations(repository, version)
        if existing is None:
            return self.publish(repository, version, data, config, layer_type)
        if existing.get(CONTENT_ANNOTATION) != content(data):
            raise RuntimeError(f'{repository}:{version} is already published with different content; bump its version')
        return self.manifest(repository, version)

    def assert_absent(self, repository, version):
        if self.manifest(repository, version, missing=True) is not None:
            raise RuntimeError(f'immutable release already exists: {repository}:{version}')

    def blob(self, repository, data):
        value = digest(data)
        _, headers = self.request('POST', f'/v2/{repository}/blobs/uploads/', data=b'')
        location = headers.get('location', '')
        parsed = urllib.parse.urlparse(urllib.parse.urljoin(self.base, location))
        if parsed.scheme + '://' + parsed.netloc != self.base:
            raise RuntimeError('unexpected registry upload location')
        query = urllib.parse.parse_qsl(parsed.query)
        query.append(('digest', value))
        path = parsed.path + '?' + urllib.parse.urlencode(query)
        self.request('PUT', path, data=data, headers={'Content-Type': 'application/octet-stream'})
        return value

    def publish(self, repository, version, data, config, layer_type=LAYER_TYPE, replace=False):
        if not replace:
            self.assert_absent(repository, version)
        config_data = encoded(config)
        manifest = {'schemaVersion': 2, 'mediaType': MANIFEST_TYPE, 'config': {'mediaType': 'application/vnd.cncf.helm.config.v1+json' if layer_type == HELM_LAYER_TYPE else 'application/vnd.oci.image.config.v1+json', 'digest': self.blob(repository, config_data), 'size': len(config_data)}, 'layers': [{'mediaType': layer_type, 'digest': self.blob(repository, data), 'size': len(data)}], 'annotations': {'org.opencontainers.image.source': 'https://github.com/' + self.repository, 'org.opencontainers.image.version': version, CONTENT_ANNOTATION: content(data)}}
        body = encoded(manifest)
        if not replace:
            self.assert_absent(repository, version)
        self.request('PUT', f'/v2/{repository}/manifests/{version}', data=body, headers={'Content-Type': MANIFEST_TYPE})
        result = digest(body)
        if self.manifest(repository, version) != result:
            raise RuntimeError('published artifact digest mismatch')
        return result


def resource(kind, name, spec, namespace='flux-system', api=None):
    versions = {'OCIRepository': 'source.toolkit.fluxcd.io/v1', 'Kustomization': 'kustomize.toolkit.fluxcd.io/v1', 'HelmRelease': 'helm.toolkit.fluxcd.io/v2'}
    return {'apiVersion': api or versions[kind], 'kind': kind, 'metadata': {'name': name, 'namespace': namespace}, 'spec': spec}


def source(name, reference, media_type=LAYER_TYPE):
    repository, pinned = reference.split('@')
    if not DIGEST.fullmatch(pinned):
        raise ValueError('artifact reference must contain a sha256 digest')
    return resource('OCIRepository', name, {'interval': '5m', 'url': 'oci://' + repository, 'ref': {'digest': pinned}, 'layerSelector': {'mediaType': media_type, 'operation': 'copy'}})


def dependency(name):
    return {'name': name, 'readyExpr': "dep.metadata.annotations['platform.loco.io/revision'] == self.metadata.annotations['platform.loco.io/revision'] && dep.metadata.generation == dep.status.observedGeneration && dep.status.conditions.exists(c, c.type == 'Ready' && c.status == 'True')"}


def bundle(version, commit, refs, images):
    files = {}
    revision = hashlib.sha256(encoded({'version': version, 'artifacts': refs, 'images': images})).hexdigest()
    labels = {'platform.loco.io/release': version}
    annotations = {'platform.loco.io/revision': revision}

    def write_layer(name, objects):
        paths = []
        for item in objects:
            path = item['metadata']['name'] + '-' + item['kind'].lower() + '.yaml'
            files[f'layers/{name}/{path}'] = encoded(item)
            paths.append(path)
        files[f'layers/{name}/kustomization.yaml'] = encoded({'apiVersion': 'kustomize.config.k8s.io/v1beta1', 'kind': 'Kustomization', 'resources': paths})

    namespace = lambda name: {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': name}}
    loco_namespace = value('manifests/namespaces/loco-system.yaml', '.')
    account = {'apiVersion': 'v1', 'kind': 'ServiceAccount', 'metadata': {'name': 'platform-reconciler', 'namespace': 'flux-system'}}
    binding = {'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'ClusterRoleBinding', 'metadata': {'name': 'loco-platform-reconciler'}, 'roleRef': {'apiGroup': 'rbac.authorization.k8s.io', 'kind': 'ClusterRole', 'name': 'cluster-admin'}, 'subjects': [{'kind': 'ServiceAccount', 'name': 'platform-reconciler', 'namespace': 'flux-system'}]}
    write_layer('namespaces', [loco_namespace, namespace('cert-manager'), account, binding])

    crds = []
    for name in ('applications', 'builds', 'gateway'):
        source_name = 'loco-' + name + '-crd'
        crds.append(source(source_name, refs[source_name]))
        crds.append(resource('Kustomization', source_name, {'interval': '5m', 'sourceRef': {'kind': 'OCIRepository', 'name': source_name}, 'path': './', 'prune': False, 'wait': True, 'timeout': '5m', 'serviceAccountName': 'platform-reconciler', 'postBuild': {'substitute': {'PLATFORM_RELEASE': revision}}}))
        crds[-1]['metadata']['annotations'] = {'platform.loco.io/artifact-digest': refs[source_name].split('@')[1]}
    write_layer('crds', crds)

    def helm_release(name, chart, namespace, values, dependencies=()):
        result = source(name + '-chart', refs[chart], HELM_LAYER_TYPE)
        hr = resource('HelmRelease', name, {'interval': '5m', 'serviceAccountName': 'platform-reconciler', 'releaseName': name, 'targetNamespace': namespace, 'chartRef': {'kind': 'OCIRepository', 'name': name + '-chart'}, 'dependsOn': [dependency(dep) for dep in dependencies], 'valuesFrom': [{'kind': kind, 'name': name + suffix, 'valuesKey': 'values.yaml', 'optional': True} for kind, suffix in (('ConfigMap', '-values'), ('Secret', '-secrets'))], 'values': values, 'install': {'createNamespace': False, 'crds': 'Skip', 'remediation': {'retries': 2}}, 'upgrade': {'crds': 'Skip', 'remediation': {'retries': 2, 'strategy': 'rollback'}}, 'driftDetection': {'mode': 'enabled'}})
        hr['metadata'].update({'labels': labels, 'annotations': annotations})
        return [result, hr]

    cert = helm_release('cert-manager', 'cert-manager-chart', 'cert-manager', {'crds': {'enabled': True, 'keep': True}})
    # cert-manager installs and retains its own CRDs.
    cert[1]['spec']['install']['crds'] = 'Create'
    cert[1]['spec']['upgrade']['crds'] = 'CreateReplace'
    gateway = helm_release('envoy-gateway', 'gateway-chart', 'loco-system', {'crds': {'enabled': False}, 'config': {'envoyGateway': {'provider': {'type': 'Kubernetes', 'kubernetes': {'deploy': {'type': 'GatewayNamespace'}}}}}})
    write_layer('infra', cert + gateway)
    pinned = {name: {'repository': reference.split('@')[0], 'tag': f'sha-{commit}@{reference.split("@")[1]}'} for name, reference in images.items()}
    control_plane = helm_release('loco-control-plane', 'loco-control-plane', 'loco-system', {'api': {'image': pinned['api']}, 'ui': {'image': pinned['ui']}, 'obsProxy': {'image': pinned['obs-proxy']}}, ('cert-manager', 'envoy-gateway'))
    # Embedded operator CRDs precede their custom resources and remain installed.
    control_plane[1]['spec']['install']['crds'] = 'Create'
    control_plane[1]['spec']['upgrade']['crds'] = 'CreateReplace'
    write_layer('control-plane', control_plane)
    write_layer('worker', helm_release('loco-worker', 'loco-worker', 'loco-system', {'agent': {'image': pinned['agent']}, 'controller': {'image': pinned['controller']}, 'builds': {'builderImage': pinned['builder']}, 'gateway-helm': {'enabled': False}}, ('cert-manager', 'envoy-gateway')))

    layers = {}
    for name, dependencies in (('namespaces', ()), ('crds', ('namespaces',)), ('infra', ('crds',)), ('control-plane', ('infra',)), ('worker', ('infra',))):
        spec = {'interval': '5m', 'sourceRef': {'kind': 'OCIRepository', 'name': 'platform-bundle'}, 'path': './layers/' + name, 'prune': name != 'crds', 'wait': True, 'timeout': '10m', 'postBuild': {'substitute': {'PLATFORM_RELEASE': revision}}, 'dependsOn': [dependency('loco-' + dep) for dep in dependencies]}
        if name != 'namespaces':
            spec['serviceAccountName'] = 'platform-reconciler'
        if name in ('control-plane', 'worker'):
            spec['dependsOn'].append({'name': 'loco-cluster-config'})
        if name == 'crds':
            health = "metadata.generation == status.observedGeneration && status.conditions.exists(c, c.type == 'Ready' && c.status == 'True') && status.lastAppliedRevision == metadata.annotations['platform.loco.io/artifact-digest']"
            spec['healthCheckExprs'] = [{'apiVersion': 'kustomize.toolkit.fluxcd.io/v1', 'kind': 'Kustomization', 'current': health}]
        layer = resource('Kustomization', 'loco-' + name, spec)
        layer['metadata'].update({'labels': labels, 'annotations': annotations})
        layers[name] = layer
    for entry, selected in (('control-plane', ('control-plane',)), ('worker', ('worker',)), ('all', ('control-plane', 'worker'))):
        resources = []
        for name in ('namespaces', 'crds', 'infra', *selected):
            path = name + '.yaml'
            files[entry + '/' + path] = encoded(layers[name])
            resources.append(path)
        files[entry + '/kustomization.yaml'] = encoded({'apiVersion': 'kustomize.config.k8s.io/v1beta1', 'kind': 'Kustomization', 'resources': resources})
    files['release.json'] = encoded(release_manifest(version, commit, refs, images))
    return files


def release_manifest(version, commit, refs, images):
    """Records the immutable software artifacts and images of a bundle."""
    return {'version': version, 'coreCommit': commit, 'artifacts': refs, 'images': images}


def protected_crd(path):
    item = json.loads(run('yq', '-o=json', '.', str(path)))
    item['metadata'].setdefault('annotations', {})['kustomize.toolkit.fluxcd.io/prune'] = 'disabled'
    return archive({'crd.yaml': encoded(item), 'kustomization.yaml': encoded({'apiVersion': 'kustomize.config.k8s.io/v1beta1', 'kind': 'Kustomization', 'resources': ['crd.yaml']})})


def gateway_crds(package):
    files = {}
    with tarfile.open(package) as tar:
        for member in tar.getmembers():
            if '/crds/crds/' in member.name and member.name.endswith(('.yaml', '.yml')):
                if member.isfile():
                    name = Path(member.name).name
                    files[name] = tar.extractfile(member).read()
    if not files:
        raise RuntimeError('gateway dependency contains no CRDs')
    files['kustomization.yaml'] = encoded({'apiVersion': 'kustomize.config.k8s.io/v1beta1', 'kind': 'Kustomization', 'resources': sorted(files), 'commonAnnotations': {'kustomize.toolkit.fluxcd.io/prune': 'disabled'}})
    return archive(files)


def validate_commit(commit):
    if not SHA.fullmatch(commit):
        raise ValueError('core commit must be a full lowercase Git SHA')
    if run('git', 'rev-parse', 'HEAD') != commit:
        raise ValueError('checked-out source differs from core commit')
    run('git', 'merge-base', '--is-ancestor', commit, 'origin/main')


def value(path, expression, ref=None):
    if ref is None:
        return parse((ROOT / path).read_text(), expression)
    if not run('git', 'ls-tree', '--name-only', ref, '--', path):
        return None
    return parse(run('git', 'show', f'{ref}:{path}'), expression)


def versions(ref=None):
    result = {name: value(f'charts/{name}/Chart.yaml', '.version', ref) for name in CHARTS}
    crds = value(CRD_VERSIONS, '.', ref)
    result.update({'loco-' + name + '-crd': crds[name] for name in CRD_SOURCES})
    for version in result.values():
        order(version)
    result['cert-manager-chart'] = value(CERT_MANAGER, '.version', ref)
    result['gateway-chart'] = value('charts/loco-worker/Chart.yaml', CRD_SOURCES['gateway'][1] + ' | .version', ref)
    return result


def repositories(registry):
    result = {name: registry.repository + '/charts/' + name for name in CHARTS}
    result.update({'loco-' + name + '-crd': registry.repository + '/crds/' + name for name in CRD_SOURCES})
    result.update({'cert-manager-chart': registry.repository + '/charts/cert-manager', 'gateway-chart': registry.repository + '/charts/gateway-helm', 'platform': registry.repository + '/platform'})
    return result


def unbumped(base):
    stale = []
    for name, (path, expression) in CRD_SOURCES.items():
        old = value(CRD_VERSIONS, '.' + name, base)
        if old is not None and value(path, expression, base) != value(path, expression) and order(value(CRD_VERSIONS, '.' + name)) <= order(old):
            stale.append('loco-' + name + '-crd')
    return stale


def packaged_artifacts(directory, commit):
    """Packages each chart and CRD bundle of the checkout as (name, version, data, config, layer type)."""
    pinned = versions()
    temp = Path(directory)
    run('mise', 'run', 'helm:deps')
    for name in CHARTS:
        run('helm', 'package', str(ROOT / 'charts' / name), '--destination', directory)
        config = value(f'charts/{name}/Chart.yaml', '.')
        yield name, pinned[name], (temp / f'{name}-{pinned[name]}.tgz').read_bytes(), config, HELM_LAYER_TYPE
    cert = value(CERT_MANAGER, '.')
    run('helm', 'pull', cert['chart'], '--repo', cert['repository'], '--version', cert['version'], '--destination', directory)
    data = next(temp.glob('cert-manager-*.tgz')).read_bytes()
    yield 'cert-manager-chart', cert['version'], data, {'name': cert['chart'], 'version': cert['version']}, HELM_LAYER_TYPE
    gateway_package = next((ROOT / 'charts/loco-worker/charts').glob('gateway-helm-*.tgz'))
    gateway_version = value('charts/loco-worker/Chart.yaml', CRD_SOURCES['gateway'][1] + ' | .version')
    yield 'gateway-chart', gateway_version, gateway_package.read_bytes(), {'name': 'gateway-helm', 'version': gateway_version}, HELM_LAYER_TYPE
    crds = {
        'loco-applications-crd': protected_crd(ROOT / CRD_SOURCES['applications'][0]),
        'loco-builds-crd': protected_crd(ROOT / CRD_SOURCES['builds'][0]),
        'loco-gateway-crd': gateway_crds(gateway_package),
    }
    for name, data in crds.items():
        yield name, pinned[name], data, {'version': pinned[name], 'coreCommit': commit}, LAYER_TYPE


def publish_artifacts(commit, registry):
    validate_commit(commit)
    names = repositories(registry)
    refs = {}
    pinned = {}
    with tempfile.TemporaryDirectory(prefix='loco-release-') as directory:
        for name, version, data, config, layer_type in packaged_artifacts(directory, commit):
            pinned[name] = version
            refs[name] = registry.ensure(names[name], version, data, config, layer_type)
    public = Registry(registry.repository, '', '')
    for name, result in refs.items():
        if public.manifest(names[name], result) != result:
            raise RuntimeError(f'artifact must permit anonymous pulls: {name}')
    print(json.dumps({name: f'ghcr.io/{names[name]}:{pinned[name]}@{result}' for name, result in refs.items()}, indent=2))


def published(commit, registry, ref=None):
    """Returns the published chart and CRD bundle versions the source at ref pins and the sha-<commit> images, by digest."""
    names = repositories(registry)
    public = Registry(registry.repository, '', '')
    refs = {}
    for name, pinned in versions(ref).items():
        result = public.manifest(names[name], pinned, missing=True)
        if result is None:
            raise RuntimeError(f'{name} {pinned} is not published')
        refs[name] = 'ghcr.io/' + names[name] + '@' + result
    images = {}
    for name in IMAGES:
        image_repository = registry.repository + '-' + name
        image_digest = registry.manifest(image_repository, 'sha-' + commit)
        registry.require_image_platforms(image_repository, image_digest)
        images[name] = 'ghcr.io/' + image_repository + '@' + image_digest
        run('gh', 'attestation', 'verify', 'oci://' + images[name], '--repo', registry.repository, '--signer-workflow', registry.repository + '/.github/workflows/build-push.yml', '--source-ref', 'refs/heads/main', '--source-digest', commit, '--deny-self-hosted-runners')
    for reference in images.values():
        repository, expected = reference.removeprefix('ghcr.io/').split('@')
        if public.manifest(repository, expected) != expected:
            raise RuntimeError('platform images must permit anonymous pulls')
    return refs, images


def publish_bundle(version, commit, registry):
    if not order(version)[3]:
        raise ValueError("platform bundles require a stable X.Y.Z release")
    validate_commit(commit)
    names = repositories(registry)
    registry.assert_absent(names['platform'], version)
    refs, images = published(commit, registry)
    files = bundle(version, commit, refs, images)
    public = Registry(registry.repository, '', '')
    result = registry.publish(names['platform'], version, archive(files), {'version': version, 'coreCommit': commit})
    if public.manifest(names['platform'], result) != result:
        raise RuntimeError('published platform bundle must permit anonymous pulls')
    print('published ghcr.io/' + names['platform'] + ':' + version + '@' + result, file=sys.stderr)
    print(files['release.json'].decode())


def newest_published_commit(registry):
    """Returns the newest of the last MAIN_COMMITS first-parent commits of origin/main whose images CI published."""
    for commit in run('git', 'rev-list', '--first-parent', '-n', str(MAIN_COMMITS), 'origin/main').split():
        if all(registry.manifest(registry.repository + '-' + name, 'sha-' + commit, missing=True) for name in IMAGES):
            return commit
    raise RuntimeError(f'none of the last {MAIN_COMMITS} commits of origin/main has published images')


def main_release(commit, registry):
    """Prints the release.json of a main commit from the artifacts and images CI published for it."""
    commit = commit or newest_published_commit(registry)
    if not SHA.fullmatch(commit):
        raise ValueError('core commit must be a full lowercase Git SHA')
    run('git', 'merge-base', '--is-ancestor', commit, 'origin/main')
    refs, images = published(commit, registry, commit)
    print(json.dumps(release_manifest('0.0.0-main.g' + commit[:12], commit, refs, images), indent=2))


def local_release(registry, address, images=None):
    """Builds the checkout's images, charts and CRD bundles into registry and prints a release.json that names them at address."""
    commit = run('git', 'rev-parse', 'HEAD')
    version = '0.0.0-local.g' + commit[:12]
    names = repositories(registry)
    refs = {}
    prebuilt = images
    images = {}
    with tempfile.TemporaryDirectory(prefix='loco-release-') as directory:
        for name, _, data, config, layer_type in packaged_artifacts(directory, commit):
            refs[name] = address + '/' + names[name] + '@' + registry.publish(names[name], version, data, config, layer_type, replace=True)
        auth = base64.b64encode((registry.username + ':' + registry.password).encode()).decode()
        config = Path(directory) / 'config.json'
        config.write_text(json.dumps({'auths': {registry.host: {'auth': auth}}}))
        config.chmod(0o600)
        # The push reads its credential from a separate client configuration, which has no contexts, and names the current context's daemon.
        daemon = run('docker', 'context', 'inspect', '--format', '{{.Endpoints.docker.Host}}')
        for name in IMAGES:
            repository = registry.repository + '-' + name
            tag = registry.host + '/' + repository + ':' + version
            if prebuilt:
                run('docker', 'tag', prebuilt.replace('*', name), tag)
            else:
                run('docker', 'build', '--build-arg', 'VERSION=' + version, '-f', DOCKERFILES[name], '-t', tag, '.')
            run('docker', '--config', directory, '--host', daemon, 'push', tag)
            images[name] = address + '/' + repository + '@' + registry.manifest(repository, version)
    files = bundle(version, commit, refs, images)
    result = registry.publish(names['platform'], version, archive(files), {'version': version, 'coreCommit': commit}, replace=True)
    print('published ' + address + '/' + names['platform'] + ':' + version + '@' + result, file=sys.stderr)
    print(json.dumps(release_manifest(version, commit, refs, images), indent=2))


def main():
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest='command', required=True)
    artifacts = commands.add_parser('artifacts')
    artifacts.add_argument('--commit', required=True)
    artifacts.add_argument('--repository', required=True)
    platform = commands.add_parser('bundle')
    platform.add_argument('--version', required=True)
    platform.add_argument('--commit', required=True)
    platform.add_argument('--repository', required=True)
    main_commit = commands.add_parser('main')
    main_commit.add_argument('--commit')
    main_commit.add_argument('--repository', required=True)
    local = commands.add_parser('local')
    local.add_argument('--repository', required=True)
    local.add_argument('--push', required=True)
    local.add_argument('--address', required=True)
    local.add_argument('--username', required=True)
    local.add_argument('--password', required=True)
    local.add_argument('--images', help='Local prebuilt image template, with * standing for the component name')
    check = commands.add_parser('check-versions')
    check.add_argument('--base', required=True)
    args = parser.parse_args()
    if args.command == 'check-versions':
        stale = unbumped(args.base)
        if stale:
            raise SystemExit('changed since ' + args.base + ' without a version bump: ' + ', '.join(stale))
        return
    if args.command == 'main':
        main_release(args.commit, Registry(args.repository, '', ''))
        return
    if args.command == 'local':
        local_release(Registry(args.repository, args.username, args.password, args.push), args.address, args.images)
        return
    registry = Registry(args.repository, os.environ['GITHUB_ACTOR'], os.environ['GH_TOKEN'])
    if args.command == 'artifacts':
        publish_artifacts(args.commit, registry)
        return
    publish_bundle(args.version, args.commit, registry)


if __name__ == '__main__':
    main()
