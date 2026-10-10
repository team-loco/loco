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
CHARTS = ('loco-core', 'loco-networking', 'loco-obs', 'loco-operator')
IMAGES = ('agent', 'controller', 'builder', 'obs-proxy', 'api', 'ui')
MANIFEST_TYPE = 'application/vnd.oci.image.manifest.v1+json'
LAYER_TYPE = 'application/vnd.oci.image.layer.v1.tar+gzip'
HELM_LAYER_TYPE = 'application/vnd.cncf.helm.chart.content.v1.tar+gzip'
TIMEOUT_SECONDS = 120
VERSION = re.compile(r'(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-rc\.(0|[1-9]\d*))?\Z')
SHA = re.compile(r'[a-f0-9]{40}\Z')
DIGEST = re.compile(r'sha256:[a-f0-9]{64}\Z')


def run(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


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
        manifest = {'schemaVersion': 2, 'mediaType': MANIFEST_TYPE, 'config': {'mediaType': 'application/vnd.cncf.helm.config.v1+json' if layer_type == HELM_LAYER_TYPE else 'application/vnd.oci.image.config.v1+json', 'digest': self.blob(repository, config_data), 'size': len(config_data)}, 'layers': [{'mediaType': layer_type, 'digest': self.blob(repository, data), 'size': len(data)}], 'annotations': {'org.opencontainers.image.source': 'https://github.com/' + self.repository, 'org.opencontainers.image.version': version}}
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
    platform_values = {
        'loco-core': {'agent': {'image': {'repository': images['agent'].split('@')[0], 'tag': f'sha-{commit}@{images["agent"].split("@")[1]}'}}},
        'loco-operator': {'crd': {'enable': False}, 'controller': {'image': {'repository': images['controller'].split('@')[0], 'tag': f'sha-{commit}@{images["controller"].split("@")[1]}'}}, 'builds': {'builderImage': {'repository': images['builder'].split('@')[0], 'tag': f'sha-{commit}@{images["builder"].split("@")[1]}'}}},
        'loco-obs': {'obsProxy': {'image': {'repository': images['obs-proxy'].split('@')[0], 'tag': f'sha-{commit}@{images["obs-proxy"].split("@")[1]}'}}},
    }
    for name, values in platform_values.items():
        chart_name = name + '-chart'
        releases.append(source(chart_name, refs[name], HELM_LAYER_TYPE))
        dependencies = ['cert-manager'] if name == 'loco-core' else ['loco-core']
        hr = resource('HelmRelease', name, {'interval': '5m', 'serviceAccountName': 'platform-reconciler', 'releaseName': name, 'targetNamespace': 'observability' if name == 'loco-obs' else 'loco-system', 'chartRef': {'kind': 'OCIRepository', 'name': chart_name}, 'dependsOn': [dependency(item) for item in dependencies], 'valuesFrom': [{'kind': 'ConfigMap', 'name': name + '-values', 'valuesKey': 'values.yaml'}] + ([{'kind': 'Secret', 'name': name + '-secrets', 'valuesKey': 'values.yaml'}] if name in ('loco-core', 'loco-obs') else []), 'values': values, 'install': {'createNamespace': False, 'crds': 'Skip', 'remediation': {'retries': 2}}, 'upgrade': {'crds': 'Skip', 'remediation': {'retries': 2, 'strategy': 'rollback'}}, 'driftDetection': {'mode': 'enabled'}})
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


def validate_identity(version, commit):
    if not VERSION.fullmatch(version):
        raise ValueError('version must be X.Y.Z or X.Y.Z-rc.N without a v prefix')
    if not SHA.fullmatch(commit):
        raise ValueError('core commit must be a full lowercase Git SHA')
    if run('git', 'rev-parse', 'HEAD') != commit:
        raise ValueError('checked-out source differs from core commit')
    run('git', 'merge-base', '--is-ancestor', commit, 'origin/main')


def publish(version, commit, registry):
    validate_identity(version, commit)
    repositories = {name: registry.repository + '/charts/' + name for name in CHARTS}
    repositories.update({'loco-applications-crd': registry.repository + '/crds/applications', 'loco-builds-crd': registry.repository + '/crds/builds', 'loco-gateway-crd': registry.repository + '/crds/gateway', 'cert-manager-chart': registry.repository + '/charts/cert-manager', 'platform': registry.repository + '/platform'})
    for name in repositories.values():
        registry.assert_absent(name, version)
    images = {}
    for name in IMAGES:
        image_repository = registry.repository + '-' + name
        image_digest = registry.manifest(image_repository, 'sha-' + commit)
        images[name] = 'ghcr.io/' + image_repository + '@' + image_digest
        run('gh', 'attestation', 'verify', 'oci://' + images[name], '--repo', registry.repository, '--signer-workflow', registry.repository + '/.github/workflows/build-push.yml', '--source-ref', 'refs/heads/main', '--source-digest', commit, '--deny-self-hosted-runners')
    public = Registry(registry.repository, '', '')
    for reference in images.values():
        repository, expected = reference.removeprefix('ghcr.io/').split('@')
        if public.manifest(repository, expected) != expected:
            raise RuntimeError('platform images must permit anonymous pulls')
    refs = {}
    with tempfile.TemporaryDirectory(prefix='loco-release-') as directory:
        temp = Path(directory)
        run('mise', 'run', 'helm:deps')
        packages = {}
        for name in CHARTS:
            run('helm', 'package', str(ROOT / 'charts' / name), '--version', version, '--app-version', version, '--destination', directory)
            packages[name] = temp / f'{name}-{version}.tgz'
        cert_version = re.search(r'chart: jetstack/cert-manager\s+version: (\S+)', (ROOT / 'helmfile.yaml.gotmpl').read_text()).group(1)
        run('helm', 'pull', 'cert-manager', '--repo', 'https://charts.jetstack.io', '--version', cert_version, '--destination', directory)
        cert_package = next(temp.glob('cert-manager-*.tgz'))
        packages['cert-manager-chart'] = cert_package
        gateway_package = next((ROOT / 'charts/loco-core/charts').glob('gateway-helm-*.tgz'))
        artifacts = {
            'loco-applications-crd': protected_crd(ROOT / 'controller/config/crd/bases/infra.loco.io_applications.yaml'),
            'loco-builds-crd': protected_crd(ROOT / 'controller/config/crd/bases/infra.loco.io_builds.yaml'),
            'loco-gateway-crd': gateway_crds(gateway_package),
        }
        for name, path in packages.items():
            config = json.loads(run('yq', '-o=json', '.', str(ROOT / 'charts' / name / 'Chart.yaml'))) if name in CHARTS else {'name': 'cert-manager', 'version': cert_version}
            config['version'] = version if name in CHARTS else config['version']
            if name in CHARTS:
                config['appVersion'] = version
            result = registry.publish(repositories[name], version, path.read_bytes(), config, HELM_LAYER_TYPE)
            refs[name] = 'ghcr.io/' + repositories[name] + '@' + result
        for name, data in artifacts.items():
            result = registry.publish(repositories[name], version, data, {'version': version, 'coreCommit': commit})
            refs[name] = 'ghcr.io/' + repositories[name] + '@' + result
        for name, reference in refs.items():
            repository, expected = reference.removeprefix('ghcr.io/').split('@')
            if public.manifest(repository, expected) != expected:
                raise RuntimeError(f'artifact must permit anonymous pulls: {name}')
        data = archive(bundle(version, commit, refs, images))
        result = registry.publish(repositories['platform'], version, data, {'version': version, 'coreCommit': commit})
        if public.manifest(repositories['platform'], result) != result:
            raise RuntimeError('published platform bundle must permit anonymous pulls')
    print(json.dumps({'version': version, 'coreCommit': commit, 'bundle': 'ghcr.io/' + repositories['platform'] + '@' + result}, indent=2))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--version', required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--repository', required=True)
    args = parser.parse_args()
    registry = Registry(args.repository, os.environ['GITHUB_ACTOR'], os.environ['GH_TOKEN'])
    publish(args.version, args.commit, registry)


if __name__ == '__main__':
    main()
