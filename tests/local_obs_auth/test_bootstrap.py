import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class ProxyBootstrap(unittest.TestCase):
    def test_tilt_seeds_the_proxy_before_installing_observability(self):
        resources = {}
        namespace = {
            'read_yaml': lambda path: {'name': 'fixture'},
            'read_file': lambda path: (ROOT / path).read_text(),
            'k8s_context': lambda: 'kind-fixture',
            'local_resource': lambda name, **kwargs: resources.update({name: kwargs}),
            'k8s_custom_deploy': lambda name, **kwargs: None,
            'k8s_resource': lambda name, **kwargs: resources.update({name: kwargs}),
        }
        for name in ('allow_k8s_contexts', 'update_settings', 'docker_build',
                     'docker_compose', 'dc_resource'):
            namespace[name] = lambda *args, **kwargs: None
        exec(compile((ROOT / 'Tiltfile').read_text(), 'Tiltfile', 'exec'), namespace)
        self.assertIn('obs-proxy-auth', resources['loco-obs']['resource_deps'])
        self.assertEqual(set(resources['obs-proxy-auth']['resource_deps']),
                         {'db-migrate', 'helm-namespaces'})

    def bootstrap(self, secret):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name, source in {
                'kubectl': """#!/bin/sh
printf '%s\n' "$*" >>"$CALLS"
case "$*" in
  *'get secret'*) printf '%s' "$SECRET" ;;
  *'create secret'*) cat >"$TOKEN_FILE"; printf '{}' ;;
  *'apply'*) cat >/dev/null ;;
esac
""",
                'docker': """#!/bin/sh
cat >"$SQL_FILE"
""",
                'helmfile': """#!/bin/sh
printf '{"releases":[{"name":"loco-obs","values":[{"obsProxy":{"auth":{"existingSecret":"%s"}}}]}]}' "$OBS_PROXY_AUTH_SECRET"
""",
            }.items():
                path = root / name
                path.write_text(source)
                path.chmod(0o755)
            result = subprocess.run(['bash', str(ROOT / 'mise-tasks/obs/bootstrap-auth')],
                                    cwd=ROOT, text=True, capture_output=True,
                                    env={**os.environ, 'PATH': f"{root}:{os.environ['PATH']}",
                                         'SECRET': secret, 'CALLS': str(root / 'calls'),
                                         'TOKEN_FILE': str(root / 'token'), 'SQL_FILE': str(root / 'sql'),
                                         'OBS_PROXY_AUTH_SECRET': 'fixture-auth',
                                         'KIND_CLUSTER': 'fixture', 'COMPOSE_PROJECT_NAME': 'fixture'})
            self.assertEqual(result.returncode, 0, result.stderr)
            return (root / 'token').read_text(), (root / 'sql').read_text(), (root / 'calls').read_text(), result

    def test_a_fresh_cluster_gets_a_random_credential_without_printing_it(self):
        token, sql, calls, result = self.bootstrap('')
        self.assertTrue(token.startswith('loco_k_'))
        self.assertNotIn(token, result.stdout + result.stderr)
        self.assertNotIn(token, sql)
        self.assertIn(hashlib.sha256(token.encode()).hexdigest(), sql)
        self.assertIn('--context kind-fixture', calls)
        self.assertIn('create secret generic fixture-auth', calls)
        other_token, _, _, _ = self.bootstrap('')
        self.assertNotEqual(token, other_token)

    def test_restart_keeps_the_credential_and_restores_its_database_record(self):
        credential = 'loco_k_fixture-credential'
        token, sql, _, _ = self.bootstrap(credential)
        self.assertEqual(token, credential)
        self.assertNotIn(credential, sql)
        self.assertIn(hashlib.sha256(credential.encode()).hexdigest(), sql)
        self.assertIn('ON CONFLICT (name, entity_type, entity_id)', sql)
