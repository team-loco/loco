import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


def environment_values(environment, variables, chart='loco-obs'):
    with tempfile.TemporaryDirectory() as directory:
        state = Path(directory) / 'helmfile.yaml'
        config = json.dumps({'releases': [{
            'name': chart, 'chart': str(ROOT / 'charts' / chart),
            'values': [str(ROOT / 'env' / environment / (chart.removeprefix('loco-') + '-chart.yaml.gotmpl'))],
        }]})
        state.write_text(subprocess.check_output(['yq', '-P', '.', '-'], input=config, text=True))
        built = subprocess.check_output(['helmfile', '-f', str(state), 'build', '--embed-values'],
                                        env={**os.environ, **variables}, text=True)
        document = subprocess.check_output(['yq', '-o=json', '.', '-'], input=built, text=True)
        return json.loads(document)['releases'][0]['values'][0]


def render(chart, template, overrides):
    source = ROOT / 'charts' / chart
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        (root / 'templates').mkdir()
        metadata = subprocess.check_output(['yq', '-o=json', '.', str(source / 'Chart.yaml')], text=True)
        chart_values = json.loads(metadata)
        chart_values.pop('dependencies', None)
        (root / 'Chart.yaml').write_text(json.dumps(chart_values))
        shutil.copyfile(source / 'values.yaml', root / 'values.yaml')
        target = root / 'templates' / template
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source / 'templates' / template, target)
        for helper in (source / 'templates').glob('*.tpl'):
            shutil.copyfile(helper, root / 'templates' / helper.name)
        (root / 'overrides.json').write_text(json.dumps(overrides))
        rendered = subprocess.check_output(['helm', 'template', chart, str(root), '-n', 'platform-test', '-f', str(root / 'overrides.json')], text=True, stderr=subprocess.STDOUT)
        document = subprocess.check_output(['yq', '-o=json', '.', '-'], input=rendered, text=True)
        return json.loads(document)


class PlatformContracts(unittest.TestCase):
    def test_production_agent_and_proxy_use_the_same_api(self):
        variables = {
            'GH_SHA': 'test', 'CERT_MANAGER_EMAIL': 'test@example.test',
            'CLOUDFLARE_TOKEN': 'test', 'LOG_LEVEL': 'info', 'AGENT_TOKEN': 'test',
            'CLICKHOUSE_PASSWORD': 'test', 'CONTROL_PLANE_URL': 'https://api.example.test',
        }
        core = environment_values('prod', variables, 'loco-core')
        obs = environment_values('prod', variables)
        self.assertEqual(core['env']['CONTROL_PLANE_URL'], variables['CONTROL_PLANE_URL'])
        self.assertEqual(obs['obsProxy']['controlPlane']['url'], variables['CONTROL_PLANE_URL'])

    def test_environment_permission_checks_use_the_api(self):
        cases = [
            ('local', {'APP_PORT': ':8123'}, 'http://host.docker.internal:8123'),
            ('local', {'APP_PORT': '0.0.0.0:8123'}, 'http://host.docker.internal:8123'),
            ('prod', {'GH_SHA': 'test', 'CLICKHOUSE_PASSWORD': 'test',
                      'CONTROL_PLANE_URL': 'https://api.example.test'}, 'https://api.example.test'),
        ]
        for environment, variables, expected in cases:
            with self.subTest(environment=environment):
                values = environment_values(environment, variables)
                values['obsProxy']['image'] = {'tag': 'test'}
                deployment = render('loco-obs', 'obs-proxy-deployment.yaml', values)
                env = {item['name']: item for item in deployment['spec']['template']['spec']['containers'][0]['env']}
                self.assertEqual(env['CONTROL_PLANE_URL']['value'], expected)

    def test_regional_certificate_names(self):
        certificate = render('loco-core', 'cm-cert.yaml', {
            'global': {'domain': {'apps': ['apps.example.test'], 'platform': 'platform.example.test'}},
            'certManager': {'additionalDNSNames': ['*.staging-east.apps.example.test', '*.staging-west.apps.example.test']},
        })
        self.assertEqual(certificate['spec']['dnsNames'], [
            '*.apps.example.test', '*.platform.example.test',
            '*.staging-east.apps.example.test', '*.staging-west.apps.example.test',
        ])

    def test_database_and_auth_secret_references(self):
        deployment = render('loco-obs', 'obs-proxy-deployment.yaml', {
            'obsProxy': {'image': {'tag': 'test'}, 'controlPlane': {'url': 'https://api.example.test'}, 'auth': {'existingSecret': 'proxy-auth'},
                         'clickhouse': {'existingSecret': {'name': 'proxy-database', 'key': 'connection'}}},
        })
        env = {item['name']: item for item in deployment['spec']['template']['spec']['containers'][0]['env']}
        self.assertEqual(env['CLICKHOUSE_URL']['valueFrom']['secretKeyRef'], {'name': 'proxy-database', 'key': 'connection'})
        self.assertNotIn('value', env['CLICKHOUSE_URL'])
        self.assertEqual(env['PROXY_AUTH_TOKEN']['valueFrom']['secretKeyRef'], {'name': 'proxy-auth', 'key': 'token', 'optional': False})

    def test_local_defaults_remain_available(self):
        deployment = render('loco-obs', 'obs-proxy-deployment.yaml', {'obsProxy': {'image': {'tag': 'test'}, 'controlPlane': {'url': 'https://api.example.test'}}})
        env = {item['name']: item for item in deployment['spec']['template']['spec']['containers'][0]['env']}
        self.assertEqual(env['CLICKHOUSE_URL']['value'], 'clickhouse://clickhouse-loco-obs-clickhouse.platform-test.svc.cluster.local:9000')
        self.assertTrue(env['PROXY_AUTH_TOKEN']['valueFrom']['secretKeyRef']['optional'])

    def test_missing_database_secret_key_is_rejected(self):
        with self.assertRaises(subprocess.CalledProcessError):
            render('loco-obs', 'obs-proxy-deployment.yaml', {'obsProxy': {'image': {'tag': 'test'}, 'controlPlane': {'url': 'https://api.example.test'}, 'clickhouse': {'existingSecret': {'name': 'db', 'key': ''}}}})

    def test_missing_permission_endpoint_is_rejected(self):
        with self.assertRaises(subprocess.CalledProcessError) as caught:
            render('loco-obs', 'obs-proxy-deployment.yaml', {'obsProxy': {'image': {'tag': 'test'}}})
        self.assertIn('obsProxy.controlPlane.url is required', caught.exception.output)


class BuildNamespaceOwnership(unittest.TestCase):
    def test_chart_creates_build_namespace_by_default(self):
        namespace = render('loco-operator', 'builds/namespace.yaml', {})
        self.assertEqual(namespace['kind'], 'Namespace')
        self.assertEqual(namespace['metadata']['name'], 'loco-builds')
        self.assertEqual(namespace['metadata']['labels']['pod-security.kubernetes.io/enforce'], 'privileged')

    def test_gitops_can_own_build_namespace(self):
        namespace = render('loco-operator', 'builds/namespace.yaml', {'builds': {'createNamespace': False}})
        self.assertIsNone(namespace)


if __name__ == '__main__':
    unittest.main()
