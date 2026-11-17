import json
from pathlib import Path
import subprocess
import tempfile

import release

FIXTURE_VERSION = '0.0.0'
FIXTURE_COMMIT = 'a' * 40
FIXTURE_DIGEST = 'sha256:' + 'b' * 64


def documents(data):
    process = subprocess.run(['yq', '-o=json', '-I=0', '.', '-'], input=data, text=True, capture_output=True, check=True)
    return [json.loads(line) for line in process.stdout.splitlines() if line and line != 'null']


def main():
    names = (*release.CHARTS, 'loco-applications-crd', 'loco-builds-crd', 'loco-gateway-crd', 'cert-manager-chart', 'gateway-chart')
    refs = {name: 'ghcr.io/example/platform/' + name + '@' + FIXTURE_DIGEST for name in names}
    images = {name: 'ghcr.io/example/platform-' + name + '@' + FIXTURE_DIGEST for name in release.IMAGES}
    files = release.bundle(FIXTURE_VERSION, FIXTURE_COMMIT, refs, images)
    with tempfile.TemporaryDirectory(prefix='loco-render-') as directory:
        temp = Path(directory)
        for name, data in files.items():
            path = temp / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        for layer in ('control-plane', 'worker', 'all', 'layers/namespaces', 'layers/crds', 'layers/infra', 'layers/control-plane', 'layers/worker'):
            if layer.startswith('layers/'):
                name = layer.split('/')[1]
                rendered = release.run('flux', 'build', 'kustomization', 'loco-' + name, '--dry-run', '--path', str(temp / layer), '--kustomization-file', str(temp / 'all' / (name + '.yaml')))
            else:
                rendered = release.run('kustomize', 'build', str(temp / layer))
            if not documents(rendered):
                raise RuntimeError('bundle layer rendered no resources')
        name = 'loco-worker'
        hr = json.loads(files['layers/worker/' + name + '-helmrelease.yaml'])
        values = hr['spec']['values']
        values['certManager'] = {'issuer': {'email': 'release-test@example.invalid'}}
        values['global'] = {'region': 'release-test-region', 'exporter': {'endpoint': 'https://ingest.release-test.example.invalid'}}
        path = temp / (name + '-values.json')
        path.write_text(json.dumps(values))
        rendered = release.run('helm', 'template', name, str(release.ROOT / 'charts' / name), '--namespace', hr['spec']['targetNamespace'], '--include-crds', '--values', str(path))
        resources = documents(rendered)
        if any(item.get('kind') == 'CustomResourceDefinition' for item in resources):
            raise RuntimeError(name + ' rendered CRDs despite independent CRD ownership')
        pinned_images = set()
        for item in resources:
            if item.get('kind') == 'Deployment':
                for container in item['spec']['template']['spec']['containers']:
                    image = container['image']
                    if image.startswith('ghcr.io/example/platform-'):
                        if not image.endswith('@' + FIXTURE_DIGEST):
                            raise RuntimeError('Loco deployment image is not digest pinned')
                        pinned_images.add(image)
        if not pinned_images:
            raise RuntimeError('chart rendered no digest-pinned Loco deployments')
        gateway_chart = next((release.ROOT / 'charts/loco-worker/charts').glob('gateway-helm-*.tgz'))
        gateway = json.loads(files['layers/infra/envoy-gateway-helmrelease.yaml'])
        gateway_values = temp / 'gateway-values.json'
        gateway_values.write_text(json.dumps(gateway['spec']['values']))
        gateway_resources = documents(release.run('helm', 'template', 'envoy-gateway', str(gateway_chart), '--namespace', 'loco-system', '--include-crds', '--values', str(gateway_values)))
        if any(item.get('kind') in ('CustomResourceDefinition', 'ValidatingAdmissionPolicy', 'ValidatingAdmissionPolicyBinding') for item in gateway_resources):
            raise RuntimeError('gateway chart rendered CRD management resources despite independent CRD ownership')
        cp_values = {
            'certManager': {'issuer': {'email': 'ops@example.test'}},
            'openbao': {'seal': {'static': {'existingSecret': {'name': 'openbao-seal'}}},
                        'snapshots': {'s3': {'endpoint': 'https://s3.example.test', 'bucket': 'backups', 'existingSecret': {'name': 'snapshot-s3'}}}},
            'api': {'existingSecret': 'regional-api-env',
                    'clickhouse': {'existingSecret': {'name': 'reader'}}},
            'obsProxy': {'ingest': {'clickhouse': {'existingSecret': {'name': 'ingest'}, 'migratorExistingSecret': {'name': 'migrator'}}}},
        }
        cp_pinned = json.loads(files['layers/control-plane/loco-control-plane-helmrelease.yaml'])['spec']['values']
        for component in ('api', 'ui', 'obsProxy'):
            cp_values.setdefault(component, {})['image'] = cp_pinned[component]['image']
        cp_path = temp / 'control-plane-values.json'
        cp_path.write_text(json.dumps(cp_values))
        cp_resources = documents(release.run('helm', 'template', 'regional', str(release.ROOT / 'charts/loco-control-plane'), '--namespace', 'loco-system', '--values', str(cp_path)))
        cp_images = {container['image'] for item in cp_resources if item.get('kind') == 'Deployment' for container in item['spec']['template']['spec']['containers']}
        expected_cp_images = {f"{images[name].split('@')[0]}:sha-{FIXTURE_COMMIT}@{FIXTURE_DIGEST}" for name in ('api', 'obs-proxy', 'ui')}
        if cp_images != expected_cp_images:
            raise RuntimeError('control-plane chart does not render the pinned API and observability proxy images')
        versions = release.versions()
        for name in release.CHARTS:
            release.run('helm', 'package', str(release.ROOT / 'charts' / name), '--destination', directory)
            package = temp / (name + '-' + versions[name] + '.tgz')
            metadata = documents(release.run('helm', 'show', 'chart', str(package)))[0]
            if metadata['version'] != versions[name]:
                raise RuntimeError('packaged chart version differs from its Chart.yaml')
            again = temp / 'again'
            release.run('helm', 'package', str(release.ROOT / 'charts' / name), '--destination', str(again))
            if release.content((again / package.name).read_bytes()) != release.content(package.read_bytes()):
                raise RuntimeError('packaged chart content is not reproducible')
        release.gateway_crds(gateway_chart)
        for name in ('applications', 'builds'):
            release.protected_crd(release.ROOT / f'controller/config/crd/bases/infra.loco.io_{name}.yaml')
    print('Platform bundle, chart packages, image digests and CRD ownership verified')


if __name__ == '__main__':
    main()
