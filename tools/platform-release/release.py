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
    def __init__(self, repository, username, password):
        if not re.fullmatch(r'[a-z0-9-]+/[a-z0-9_.-]+', repository):
            raise ValueError('repository must be a lowercase owner/repository')
        self.repository = repository
        self.username = username
        self.password = password
        self.tokens = {}

    def request(self, method, path, data=None, headers=None, missing=False):
        url = 'https://ghcr.io' + path
        request_headers = dict(headers or {})
        actions = 'pull' if method in ('GET', 'HEAD') else 'pull,push'
        scope = 'repository:' + path.split('/v2/')[1].split('/manifests/')[0].split('/blobs/')[0] + ':' + actions
        token = self.tokens.get(scope)
        if token:
            request_headers['Authorization'] = 'Bearer ' + token
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
                fields = dict(re.findall(r'(\w+)="([^"]+)"', challenge))
                if fields.get('realm') != 'https://ghcr.io/token' or fields.get('service') != 'ghcr.io':
                    raise RuntimeError('unexpected registry authentication challenge') from error
                query = urllib.parse.urlencode({'service': 'ghcr.io', 'scope': scope})
                auth = base64.b64encode((self.username + ':' + self.password).encode()).decode()
                auth_headers = {'Authorization': 'Basic ' + auth} if self.password else {}
                auth_req = urllib.request.Request(fields['realm'] + '?' + query, headers=auth_headers)
                with urllib.request.urlopen(auth_req, timeout=TIMEOUT_SECONDS) as response:
                    token = json.load(response)['token']
                self.tokens[scope] = token
                request_headers['Authorization'] = 'Bearer ' + token
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
        parsed = urllib.parse.urlparse(urllib.parse.urljoin('https://ghcr.io', location))
        if parsed.scheme != 'https' or parsed.netloc != 'ghcr.io':
            raise RuntimeError('unexpected registry upload location')
        query = urllib.parse.parse_qsl(parsed.query)
        query.append(('digest', value))
        path = parsed.path + '?' + urllib.parse.urlencode(query)
        self.request('PUT', path, data=data, headers={'Content-Type': 'application/octet-stream'})
        return value

    def publish(self, repository, version, data, config, layer_type=LAYER_TYPE):
        self.assert_absent(repository, version)
        config_data = encoded(config)
        manifest = {'schemaVersion': 2, 'mediaType': MANIFEST_TYPE, 'config': {'mediaType': 'application/vnd.cncf.helm.config.v1+json' if layer_type == HELM_LAYER_TYPE else 'application/vnd.oci.image.config.v1+json', 'digest': self.blob(repository, config_data), 'size': len(config_data)}, 'layers': [{'mediaType': layer_type, 'digest': self.blob(repository, data), 'size': len(data)}], 'annotations': {'org.opencontainers.image.source': 'https://github.com/' + self.repository, 'org.opencontainers.image.version': version, CONTENT_ANNOTATION: content(data)}}
        body = encoded(manifest)
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
    return {'name': name, 'readyExpr': "dep.metadata.labels['platform.loco.io/release'] == self.metadata.labels['platform.loco.io/release'] && dep.metadata.generation == dep.status.observedGeneration && dep.status.conditions.exists(c, c.type == 'Ready' && c.status == 'True')"}


def bundle(version, commit, refs, images):
    files = {}
    crds = []
    releases = []
    for name in ('applications', 'builds', 'gateway'):
        source_name = 'loco-' + name + '-crd'
        crds.append(source(source_name, refs[source_name]))
        crds.append(resource('Kustomization', source_name, {'interval': '5m', 'sourceRef': {'kind': 'OCIRepository', 'name': source_name}, 'path': './', 'prune': False, 'wait': True, 'timeout': '5m', 'serviceAccountName': 'platform-reconciler', 'postBuild': {'substitute': {'PLATFORM_RELEASE': version}}}))
    cert_source = 'cert-manager-chart'
    releases.append(source(cert_source, refs[cert_source], HELM_LAYER_TYPE))
    cert = resource('HelmRelease', 'cert-manager', {'interval': '5m', 'serviceAccountName': 'platform-reconciler', 'releaseName': 'cert-manager', 'targetNamespace': 'cert-manager', 'chartRef': {'kind': 'OCIRepository', 'name': cert_source}, 'commonMetadata': {'labels': {'platform.loco.io/release': version}}, 'install': {'createNamespace': False, 'remediation': {'retries': 2}}, 'upgrade': {'remediation': {'retries': 2, 'strategy': 'rollback'}}, 'values': {'crds': {'enabled': True, 'keep': True}}})
    cert['metadata']['labels'] = {'platform.loco.io/release': version}
    releases.append(cert)
    pinned = {name: {'repository': images[name].split('@')[0], 'tag': f'sha-{commit}@{images[name].split("@")[1]}'} for name in ('agent', 'controller', 'builder')}
    worker = 'loco-worker'
    worker_chart = worker + '-chart'
    releases.append(source(worker_chart, refs[worker], HELM_LAYER_TYPE))
    values = {'agent': {'image': pinned['agent']}, 'controller': {'image': pinned['controller']}, 'builds': {'builderImage': pinned['builder']}}
    hr = resource('HelmRelease', worker, {'interval': '5m', 'serviceAccountName': 'platform-reconciler', 'releaseName': worker, 'targetNamespace': 'loco-system', 'chartRef': {'kind': 'OCIRepository', 'name': worker_chart}, 'dependsOn': [dependency('cert-manager')], 'valuesFrom': [{'kind': 'ConfigMap', 'name': worker + '-values', 'valuesKey': 'values.yaml'}, {'kind': 'Secret', 'name': worker + '-secrets', 'valuesKey': 'values.yaml'}], 'values': values, 'install': {'createNamespace': False, 'crds': 'Skip', 'remediation': {'retries': 2}}, 'upgrade': {'crds': 'Skip', 'remediation': {'retries': 2, 'strategy': 'rollback'}}, 'driftDetection': {'mode': 'enabled'}})
    hr['metadata']['labels'] = {'platform.loco.io/release': version}
    releases.append(hr)
    for path, documents in (('crds', crds), ('releases', releases)):
        filenames = []
        for item in documents:
            filename = item['metadata']['name'] + '-' + item['kind'].lower() + '.yaml'
            files[path + '/' + filename] = encoded(item)
            filenames.append(filename)
        files[path + '/kustomization.yaml'] = encoded({'apiVersion': 'kustomize.config.k8s.io/v1beta1', 'kind': 'Kustomization', 'resources': filenames})
    revision_checks = []
    for name in ('applications', 'builds', 'gateway'):
        source_name = 'loco-' + name + '-crd'
        expected = refs[source_name].split('@')[1]
        revision_checks.append(f"(metadata.name == '{source_name}' && status.lastAppliedRevision == '{expected}')")
    crd_health = "metadata.generation == status.observedGeneration && status.conditions.exists(c, c.type == 'Ready' && c.status == 'True') && (" + ' || '.join(revision_checks) + ')'
    parent_spec = {'interval': '5m', 'sourceRef': {'kind': 'OCIRepository', 'name': 'platform-bundle'}, 'serviceAccountName': 'platform-reconciler', 'wait': True, 'timeout': '10m', 'postBuild': {'substitute': {'PLATFORM_RELEASE': version}}}
    crd_layer = resource('Kustomization', 'crds', {**parent_spec, 'path': './crds', 'prune': False, 'healthCheckExprs': [{'apiVersion': 'kustomize.toolkit.fluxcd.io/v1', 'kind': 'Kustomization', 'current': crd_health}]})
    release_layer = resource('Kustomization', 'platform-releases', {**parent_spec, 'path': './releases', 'prune': True, 'dependsOn': [{'name': 'crds'}]})
    files['layers.yaml'] = b'\n---\n'.join(encoded(layer) for layer in (crd_layer, release_layer))
    files['kustomization.yaml'] = encoded({'apiVersion': 'kustomize.config.k8s.io/v1beta1', 'kind': 'Kustomization', 'resources': ['layers.yaml']})
    files['release.json'] = encoded({'version': version, 'coreCommit': commit, 'artifacts': refs, 'images': images})
    return files


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


def versions():
    result = {name: value(f'charts/{name}/Chart.yaml', '.version') for name in CHARTS}
    crds = value(CRD_VERSIONS, '.')
    result.update({'loco-' + name + '-crd': crds[name] for name in CRD_SOURCES})
    for version in result.values():
        order(version)
    result['cert-manager-chart'] = value(CERT_MANAGER, '.version')
    return result


def repositories(registry):
    result = {name: registry.repository + '/charts/' + name for name in CHARTS}
    result.update({'loco-' + name + '-crd': registry.repository + '/crds/' + name for name in CRD_SOURCES})
    result.update({'cert-manager-chart': registry.repository + '/charts/cert-manager', 'platform': registry.repository + '/platform'})
    return result


def unbumped(base):
    stale = []
    for name, (path, expression) in CRD_SOURCES.items():
        old = value(CRD_VERSIONS, '.' + name, base)
        if old is not None and value(path, expression, base) != value(path, expression) and order(value(CRD_VERSIONS, '.' + name)) <= order(old):
            stale.append('loco-' + name + '-crd')
    return stale


def publish_artifacts(commit, registry):
    validate_commit(commit)
    pinned = versions()
    names = repositories(registry)
    refs = {}
    with tempfile.TemporaryDirectory(prefix='loco-release-') as directory:
        temp = Path(directory)
        run('mise', 'run', 'helm:deps')
        for name in CHARTS:
            run('helm', 'package', str(ROOT / 'charts' / name), '--destination', directory)
            config = value(f'charts/{name}/Chart.yaml', '.')
            data = (temp / f'{name}-{pinned[name]}.tgz').read_bytes()
            refs[name] = registry.ensure(names[name], pinned[name], data, config, HELM_LAYER_TYPE)
        cert = value(CERT_MANAGER, '.')
        run('helm', 'pull', cert['chart'], '--repo', cert['repository'], '--version', cert['version'], '--destination', directory)
        data = next(temp.glob('cert-manager-*.tgz')).read_bytes()
        refs['cert-manager-chart'] = registry.ensure(names['cert-manager-chart'], cert['version'], data, {'name': cert['chart'], 'version': cert['version']}, HELM_LAYER_TYPE)
        gateway_package = next((ROOT / 'charts/loco-worker/charts').glob('gateway-helm-*.tgz'))
        crds = {
            'loco-applications-crd': protected_crd(ROOT / CRD_SOURCES['applications'][0]),
            'loco-builds-crd': protected_crd(ROOT / CRD_SOURCES['builds'][0]),
            'loco-gateway-crd': gateway_crds(gateway_package),
        }
        for name, data in crds.items():
            refs[name] = registry.ensure(names[name], pinned[name], data, {'version': pinned[name], 'coreCommit': commit})
    public = Registry(registry.repository, '', '')
    for name, result in refs.items():
        if public.manifest(names[name], result) != result:
            raise RuntimeError(f'artifact must permit anonymous pulls: {name}')
    print(json.dumps({name: f'ghcr.io/{names[name]}:{pinned[name]}@{result}' for name, result in refs.items()}, indent=2))


def publish_bundle(version, commit, registry):
    order(version)
    validate_commit(commit)
    names = repositories(registry)
    registry.assert_absent(names['platform'], version)
    public = Registry(registry.repository, '', '')
    refs = {}
    for name, pinned in versions().items():
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
    data = archive(bundle(version, commit, refs, images))
    result = registry.publish(names['platform'], version, data, {'version': version, 'coreCommit': commit})
    if public.manifest(names['platform'], result) != result:
        raise RuntimeError('published platform bundle must permit anonymous pulls')
    print(json.dumps({'version': version, 'coreCommit': commit, 'bundle': 'ghcr.io/' + names['platform'] + '@' + result}, indent=2))


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
    check = commands.add_parser('check-versions')
    check.add_argument('--base', required=True)
    args = parser.parse_args()
    if args.command == 'check-versions':
        stale = unbumped(args.base)
        if stale:
            raise SystemExit('changed since ' + args.base + ' without a version bump: ' + ', '.join(stale))
        return
    registry = Registry(args.repository, os.environ['GITHUB_ACTOR'], os.environ['GH_TOKEN'])
    if args.command == 'artifacts':
        publish_artifacts(args.commit, registry)
        return
    publish_bundle(args.version, args.commit, registry)


if __name__ == '__main__':
    main()
