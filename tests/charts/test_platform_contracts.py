import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


def render(chart, template, overrides):
    documents = render_documents(chart, template, overrides)
    return documents[0] if documents else None


def render_documents(chart, template, overrides):
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
        rendered = subprocess.check_output(['helm', 'template', chart, str(root), '-n', 'platform-test', '-f', str(root / 'overrides.json')], text=True)
        documents = subprocess.check_output(['yq', '-o=json', '-I=0', '.', '-'], input=rendered, text=True)
        return [json.loads(line) for line in documents.splitlines() if line.strip() not in ('', 'null')]


class PlatformContracts(unittest.TestCase):
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
            'obsProxy': {'image': {'tag': 'test'}, 'auth': {'existingSecret': 'proxy-auth'},
                         'clickhouse': {'existingSecret': {'name': 'proxy-database', 'key': 'connection'}}},
        })
        env = {item['name']: item for item in deployment['spec']['template']['spec']['containers'][0]['env']}
        self.assertEqual(env['CLICKHOUSE_URL']['valueFrom']['secretKeyRef'], {'name': 'proxy-database', 'key': 'connection'})
        self.assertNotIn('value', env['CLICKHOUSE_URL'])
        self.assertEqual(env['PROXY_AUTH_TOKEN']['valueFrom']['secretKeyRef'], {'name': 'proxy-auth', 'key': 'token', 'optional': False})

    def test_local_defaults_remain_available(self):
        deployment = render('loco-obs', 'obs-proxy-deployment.yaml', {'obsProxy': {'image': {'tag': 'test'}}})
        env = {item['name']: item for item in deployment['spec']['template']['spec']['containers'][0]['env']}
        self.assertEqual(env['CLICKHOUSE_URL']['value'], 'clickhouse://clickhouse-loco-obs-clickhouse.platform-test.svc.cluster.local:9000')
        self.assertTrue(env['PROXY_AUTH_TOKEN']['valueFrom']['secretKeyRef']['optional'])

    def test_missing_database_secret_key_is_rejected(self):
        with self.assertRaises(subprocess.CalledProcessError):
            render('loco-obs', 'obs-proxy-deployment.yaml', {'obsProxy': {'image': {'tag': 'test'}, 'clickhouse': {'existingSecret': {'name': 'db', 'key': ''}}}})


class ObservabilitySchema(unittest.TestCase):
    def test_user_secrets_follow_the_clickhouse_users(self):
        passwords = {'loco_migrator': 'migrator-pw', 'loco_ingest': 'ingest-pw', 'loco_reader': 'reader-pw'}
        secrets = render_documents('loco-obs', 'clickhouse-users.yaml', {'clickhouseUserPasswords': passwords})
        found = {(item['metadata']['namespace'], item['metadata']['name']): item['stringData']['password'] for item in secrets}
        self.assertEqual(found, {
            ('platform-test', 'loco-obs-clickhouse-migrator'): 'migrator-pw',
            ('platform-test', 'loco-obs-clickhouse-ingest'): 'ingest-pw',
            ('platform-test', 'loco-obs-clickhouse-reader'): 'reader-pw',
        })

    def test_user_secrets_are_left_to_the_cluster_without_passwords(self):
        self.assertEqual(render_documents('loco-obs', 'clickhouse-users.yaml', {}), [])

    def test_passwords_that_break_a_dsn_are_rejected(self):
        with self.assertRaises(subprocess.CalledProcessError):
            render_documents('loco-obs', 'clickhouse-users.yaml', {'clickhouseUserPasswords': {'loco_reader': 'a b@c'}})


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
