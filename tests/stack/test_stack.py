import ipaddress
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
STACK = 'env/local/stack.sh'
LB_RANGE = 'mise-tasks/cluster/lb-range'
STACK_NAMES = (
    'LOCO_STACK_SLOT', 'LOCO_STACK_CLAIM', 'KIND_CLUSTER', 'COMPOSE_PROJECT_NAME', 'REGISTRY_ALIAS', 'S3_ALIAS',
    'POSTGRES_PORT', 'VALKEY_PORT', 'REGISTRY_PORT', 'S3_PORT', 'DEX_PORT', 'TILT_PORT', 'WEB_PORT',
    'APP_PORT', 'PUBLIC_API_URL', 'WEB_URL', 'DATABASE_URL', 'CACHE_ADDR', 'CORS_ALLOWED_ORIGINS',
    'AUTH_ISSUERS', 'LOCO_REGISTRY_HOST', 'LOCO_REGISTRY_URL', 'LOCO_SOURCE_BUCKET_ENDPOINT',
)
KIND_SUBNET = '192.168.97.0/24'
MAX_SLOT = 9


def git(cwd, *args):
    subprocess.run(['git', *args], cwd=cwd, check=True, capture_output=True)


class LocalStack(unittest.TestCase):
    def setUp(self):
        self.directory = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.directory)
        self.main = self.directory / 'main'
        (self.main / 'env' / 'local').mkdir(parents=True)
        shutil.copyfile(ROOT / STACK, self.main / STACK)
        shutil.copyfile(ROOT / '.env.example', self.main / '.env.example')
        git(self.main, 'init', '--quiet', '--initial-branch=main')
        git(self.main, 'add', '.')
        git(self.main, '-c', 'user.name=test', '-c', 'user.email=test@example.test', 'commit', '--quiet', '-m', 'init')
        shutil.copyfile(self.main / '.env.example', self.main / '.env')

    def worktree(self, name):
        path = self.directory / name
        git(self.main, 'worktree', 'add', '--quiet', '--detach', str(path))
        (path / '.env').symlink_to(self.main / '.env')
        return path

    def stack(self, checkout, **env):
        script = f'source {STACK} && for name in {" ".join(STACK_NAMES)}; do printf "%s=%s\\n" "$name" "${{!name}}"; done'
        clean = {key: value for key, value in os.environ.items() if key not in STACK_NAMES}
        result = subprocess.run(['bash', '-c', script], cwd=checkout, env={**clean, **env},
                                capture_output=True, text=True)
        if result.returncode != 0:
            raise AssertionError(result.stderr)
        return dict(line.split('=', 1) for line in result.stdout.splitlines())

    def dotenv(self):
        values = {}
        for line in (self.main / '.env').read_text().splitlines():
            if '=' in line and not line.startswith('#'):
                key, value = line.split('=', 1)
                values[key] = value.strip("'")
        return values

    def test_main_checkout_keeps_the_env_file_values(self):
        stack = self.stack(self.main)
        dotenv = self.dotenv()
        self.assertEqual(stack['LOCO_STACK_SLOT'], '0')
        self.assertEqual(stack['LOCO_STACK_CLAIM'], '')
        self.assertEqual(stack['KIND_CLUSTER'], 'loco-cluster-local')
        self.assertEqual(stack['COMPOSE_PROJECT_NAME'], 'loco-dev')
        self.assertEqual(stack['TILT_PORT'], '10350')
        self.assertEqual(stack['WEB_PORT'], '5173')
        for name in ('APP_PORT', 'WEB_URL', 'DATABASE_URL', 'CACHE_ADDR', 'CORS_ALLOWED_ORIGINS', 'AUTH_ISSUERS',
                     'LOCO_REGISTRY_HOST', 'LOCO_REGISTRY_URL', 'LOCO_SOURCE_BUCKET_ENDPOINT'):
            self.assertEqual(stack[name], dotenv[name], name)

    def test_worktrees_get_their_own_names_and_ports(self):
        first = self.stack(self.worktree('first'))
        second = self.stack(self.worktree('second'))
        main = self.stack(self.main)
        self.assertEqual(first['LOCO_STACK_SLOT'], '1')
        self.assertEqual(second['LOCO_STACK_SLOT'], '2')
        self.assertEqual(second['KIND_CLUSTER'], 'loco-cluster-local-2')
        self.assertEqual(second['COMPOSE_PROJECT_NAME'], 'loco-dev-2')
        self.assertEqual(second['APP_PORT'], ':8200')
        self.assertEqual(second['PUBLIC_API_URL'], 'http://localhost:8200')
        self.assertEqual(second['WEB_URL'], 'http://localhost:5373')
        self.assertEqual(second['CORS_ALLOWED_ORIGINS'], 'http://localhost:5373')
        self.assertEqual(second['DATABASE_URL'], 'postgres://loco_user:loco_password@localhost:5632/loco?sslmode=disable')
        self.assertEqual(second['CACHE_ADDR'], 'valkey://127.0.0.1:6579')
        self.assertIn('"issuer":"http://localhost:5756/dex"', second['AUTH_ISSUERS'])
        self.assertEqual(second['LOCO_REGISTRY_HOST'], 'loco-registry-2:5000')
        self.assertEqual(second['LOCO_REGISTRY_URL'], 'http://localhost:5201')
        self.assertEqual(second['LOCO_SOURCE_BUCKET_ENDPOINT'], 'http://loco-s3-2.localhost:9200')
        ports = ('POSTGRES_PORT', 'VALKEY_PORT', 'REGISTRY_PORT', 'S3_PORT', 'DEX_PORT', 'TILT_PORT', 'WEB_PORT')
        used = [stack[name] for stack in (main, first, second) for name in ports]
        used += [stack['APP_PORT'] for stack in (main, first, second)]
        self.assertEqual(len(used), len({port.lstrip(':') for port in used}))
        for name in ('KIND_CLUSTER', 'COMPOSE_PROJECT_NAME', 'REGISTRY_ALIAS', 'S3_ALIAS'):
            self.assertEqual(len({main[name], first[name], second[name]}), 3, name)

    def test_a_worktree_keeps_its_slot(self):
        checkout = self.worktree('kept')
        self.worktree('other')
        self.assertEqual(self.stack(checkout)['LOCO_STACK_SLOT'], '1')
        self.assertEqual(self.stack(checkout)['LOCO_STACK_SLOT'], '1')

    def test_a_removed_worktree_frees_its_slot(self):
        removed = self.worktree('removed')
        self.assertEqual(self.stack(removed)['LOCO_STACK_SLOT'], '1')
        git(self.main, 'worktree', 'remove', '--force', str(removed))
        self.assertEqual(self.stack(self.worktree('next'))['LOCO_STACK_SLOT'], '1')

    def test_a_worktree_fails_when_every_slot_is_claimed(self):
        for slot in range(MAX_SLOT):
            self.stack(self.worktree(f'claimed-{slot}'))
        with self.assertRaisesRegex(AssertionError, 'every local stack slot'):
            self.stack(self.worktree('unclaimed'))

    def test_a_slot_can_be_chosen(self):
        self.assertEqual(self.stack(self.main, LOCO_STACK_SLOT='4')['KIND_CLUSTER'], 'loco-cluster-local-4')
        with self.assertRaisesRegex(AssertionError, 'LOCO_STACK_SLOT must be a slot'):
            self.stack(self.main, LOCO_STACK_SLOT='10')

    def test_a_checkout_without_an_env_file_fails(self):
        (self.main / '.env').unlink()
        with self.assertRaisesRegex(AssertionError, r'\.env is missing'):
            self.stack(self.main)


class LoadBalancerRange(unittest.TestCase):
    def lb_range(self, slot):
        directory = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, directory)
        docker = directory / 'docker'
        docker.write_text(f'#!/usr/bin/env bash\necho {KIND_SUBNET}\n')
        docker.chmod(0o755)
        env = {**os.environ, 'PATH': f'{directory}:{os.environ["PATH"]}', 'LOCO_STACK_SLOT': str(slot)}
        output = subprocess.check_output([str(ROOT / LB_RANGE)], env=env, text=True)
        first, last = (ipaddress.ip_address(address) for address in output.split())
        return first, last

    def test_the_main_checkout_keeps_its_range(self):
        self.assertEqual(self.lb_range(0), (ipaddress.ip_address('192.168.97.200'), ipaddress.ip_address('192.168.97.249')))

    def test_slots_get_ranges_that_do_not_overlap(self):
        taken = set()
        for slot in range(MAX_SLOT + 1):
            first, last = self.lb_range(slot)
            addresses = set(range(int(first), int(last) + 1))
            self.assertFalse(addresses & taken, slot)
            taken |= addresses


if __name__ == '__main__':
    unittest.main()
