import importlib.util
import json
import subprocess
from pathlib import Path
import struct
import unittest

SPEC = importlib.util.spec_from_file_location('verify', Path(__file__).resolve().parents[1] / 'verify.py')
verify = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(verify)


def elf(machine, interpreter=False):
    header = bytearray(128)
    header[:6] = b'\x7fELF\x02\x01'
    struct.pack_into('<H', header, 18, machine)
    struct.pack_into('<Q', header, 32, 64)
    struct.pack_into('<HH', header, 54, 56, 1)
    struct.pack_into('<I', header, 64, 3 if interpreter else 1)
    return header


class BinaryTests(unittest.TestCase):
    def test_both_static_architectures(self):
        verify.validate_binary(elf(62), 'amd64')
        verify.validate_binary(elf(183), 'arm64')

    def test_amd64_binary_in_arm64_image_fails(self):
        with self.assertRaisesRegex(ValueError, 'does not match arm64'):
            verify.validate_binary(elf(62), 'arm64')

    def test_dynamic_binary_fails(self):
        with self.assertRaisesRegex(ValueError, 'statically linked'):
            verify.validate_binary(elf(183, interpreter=True), 'arm64')

    def test_invalid_and_truncated_binary_fail(self):
        for data in (b'not an executable', elf(183)[:80]):
            with self.assertRaises(ValueError):
                verify.validate_binary(data, 'arm64')


class IndexTests(unittest.TestCase):
    def setUp(self):
        self.image = 'ghcr.io/example/platform@sha256:index'
        self.amd64 = {'digest': 'sha256:amd64', 'platform': {'os': 'linux', 'architecture': 'amd64'}}
        self.arm64 = {'digest': 'sha256:arm64', 'platform': {'os': 'linux', 'architecture': 'arm64', 'variant': 'v8'}}
        self.attestation = {'digest': 'sha256:attestation', 'platform': {'os': 'unknown', 'architecture': 'unknown'}}

    def test_selects_architecture_and_ignores_attestations(self):
        index = {'manifests': [self.amd64, self.arm64, self.attestation]}
        self.assertEqual(verify.resolve(self.image, 'linux/arm64', index), 'ghcr.io/example/platform@sha256:arm64')
        self.assertEqual(verify.resolve(self.image, 'linux/amd64', index), 'ghcr.io/example/platform@sha256:amd64')

    def test_missing_or_duplicate_platform_fails(self):
        for manifests in ([self.amd64, self.attestation], [self.arm64, self.arm64]):
            with self.assertRaisesRegex(ValueError, 'exactly one linux/arm64'):
                verify.resolve(self.image, 'linux/arm64', {'manifests': manifests})

class WorkflowTests(unittest.TestCase):
    def workflow(self, name):
        root = Path(__file__).resolve().parents[3]
        return json.loads(subprocess.check_output(['yq', '-o', 'json', '.', str(root / '.github/workflows' / name)], text=True))

    def test_publishing_requires_both_native_verifiers_before_index_provenance(self):
        workflow = self.workflow('build-push.yml')
        jobs = workflow['jobs']
        push = next(step for step in jobs['build-and-push-image']['steps'] if step.get('id') == 'push')
        self.assertEqual(set(push['with']['platforms'].split(',')), {'linux/amd64', 'linux/arm64'})
        self.assertEqual(push['with']['provenance'], 'mode=max')
        runners = jobs['verify-platform']['strategy']['matrix']['include']
        self.assertEqual({(entry['platform'], entry['runner']) for entry in runners}, {('linux/amd64', 'ubuntu-latest'), ('linux/arm64', 'ubuntu-24.04-arm')})
        self.assertIn('verify-platform', jobs['attest-index']['needs'])
        sbom = next(step for step in jobs['verify-platform']['steps'] if step['name'] == 'Generate platform SBOM')
        self.assertEqual(sbom['with']['image'], '${{ steps.platform.outputs.image }}')

    def test_pull_requests_build_each_platform_image_on_both_architectures(self):
        matrix = self.workflow('docker.yml')['jobs']['buildkit-check']['strategy']['matrix']
        self.assertEqual({entry['platform'] for entry in matrix['target']}, {'linux/amd64', 'linux/arm64'})
        self.assertEqual({entry['dockerfile'] for entry in matrix['image']}, {'./api/Dockerfile', './agent/Dockerfile', './controller/Dockerfile', './builder/Dockerfile', './observability-proxy/Dockerfile', './web/Dockerfile'})


if __name__ == '__main__':
    unittest.main()
