import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
FIXTURE_ENV = {
    'GH_SHA': 'test', 'CERT_MANAGER_EMAIL': 'test@example.test',
    'CLOUDFLARE_TOKEN': 'test', 'LOG_LEVEL': 'info', 'AGENT_TOKEN': 'test',
    'CONTROL_PLANE_URL': 'https://api.example.test', 'CLICKHOUSE_PASSWORD': 'test',
    'CLICKHOUSE_MIGRATOR_PASSWORD': 'test', 'CLICKHOUSE_INGEST_PASSWORD': 'test',
    'CLICKHOUSE_READER_PASSWORD': 'test', 'OBS_PROXY_AUTH_SECRET': 'proxy-auth',
}


def release_state(environment):
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        shutil.copyfile(ROOT / 'helmfile.yaml.gotmpl', root / 'helmfile.yaml.gotmpl')
        (root / 'env' / environment).mkdir(parents=True)
        shutil.copyfile(ROOT / 'env' / 'global.yaml', root / 'env' / 'global.yaml')
        for source in (ROOT / 'env' / environment).glob('*-chart.yaml.gotmpl'):
            (root / 'env' / environment / source.name).write_text('{}')
        for source in (ROOT / 'charts').glob('*/values.yaml'):
            target = root / source.relative_to(ROOT)
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, target)
        built = subprocess.check_output(
            ['helmfile', '-f', str(root / 'helmfile.yaml.gotmpl'), '-e', environment,
             'build', '--embed-values'], env={**os.environ, **FIXTURE_ENV}, text=True)
    raw = subprocess.check_output(['yq', '-o=json', '.', '-'], input=built, text=True)
    state = json.loads(raw)
    releases = {release['name']: release for release in state['releases']}
    return state, releases


class HelmProviderReadiness(unittest.TestCase):
    def test_cert_manager_is_ready_before_consumers_in_both_environments(self):
        for environment in ('local', 'prod'):
            with self.subTest(environment=environment):
                state, releases = release_state(environment)
                provider = releases['cert-manager']
                self.assertTrue(provider.get('wait', state['helmDefaults']['wait']))
                self.assertIn('cert-manager/cert-manager', releases['loco-core']['needs'])
                self.assertIn('loco-system/loco-core', releases['loco-obs']['needs'])
                for name in ('loco-core', 'loco-obs'):
                    self.assertFalse(releases[name].get('wait', state['helmDefaults']['wait']))

    def test_networking_is_ready_before_local_providers(self):
        state, releases = release_state('local')
        self.assertTrue(releases['loco-networking'].get('wait', state['helmDefaults']['wait']))
        for name in ('cert-manager', 'loco-operator'):
            self.assertIn('kube-system/loco-networking', releases[name]['needs'])
