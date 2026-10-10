import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import urllib.error

SPEC = importlib.util.spec_from_file_location('release', Path(__file__).parents[1] / 'release.py')
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)


class BundleTests(unittest.TestCase):
    def setUp(self):
        self.digest = 'sha256:' + 'a' * 64
        self.commit = 'b' * 40
        names = (*release.CHARTS, 'loco-applications-crd', 'loco-builds-crd', 'loco-gateway-crd', 'cert-manager-chart')
        self.refs = {name: 'ghcr.io/example/platform/' + name + '@' + self.digest for name in names}
        self.images = {name: 'ghcr.io/example/platform-' + name + '@' + self.digest for name in release.IMAGES}
        self.files = release.bundle('0.7.0-rc.1', self.commit, self.refs, self.images)

    def test_bundle_pins_every_source_and_image(self):
        resources = [json.loads(value) for path, value in self.files.items() if path.endswith('.yaml') and path != 'layers.yaml']
        sources = [value for value in resources if value['kind'] == 'OCIRepository']
        self.assertEqual(len(sources), 7)
        for source in sources:
            self.assertEqual(source['spec']['ref'], {'digest': self.digest})
        lock = json.loads(self.files['release.json'])
        self.assertEqual(lock['coreCommit'], self.commit)
        self.assertEqual(lock['images'], self.images)
        core = json.loads(self.files['releases/loco-core-helmrelease.yaml'])
        self.assertEqual(core['spec']['values']['agent']['image']['tag'], f'sha-{self.commit}@{self.digest}')

    def test_crds_are_protected_and_owned_separately(self):
        operator = json.loads(self.files['releases/loco-operator-helmrelease.yaml'])
        self.assertFalse(operator['spec']['values']['crd']['enable'])
        for name in ('applications', 'builds', 'gateway'):
            ks = json.loads(self.files[f'crds/loco-{name}-crd-kustomization.yaml'])
            self.assertFalse(ks['spec']['prune'])
            self.assertTrue(ks['spec']['wait'])

    def test_releases_wait_for_matching_release_and_current_generation(self):
        for name in ('loco-core', 'loco-operator', 'loco-obs'):
            hr = json.loads(self.files[f'releases/{name}-helmrelease.yaml'])
            self.assertEqual(hr['metadata']['labels']['platform.loco.io/release'], '0.7.0-rc.1')
            for dep in hr['spec']['dependsOn']:
                self.assertIn('dep.status.observedGeneration', dep['readyExpr'])
                self.assertIn("platform.loco.io/release", dep['readyExpr'])
            self.assertEqual(hr['spec']['upgrade']['remediation']['strategy'], 'rollback')

    def test_bundle_archive_contains_only_declared_files(self):
        data = release.archive(self.files)
        with tarfile.open(fileobj=io.BytesIO(data)) as tar:
            self.assertEqual(set(tar.getnames()), set(self.files))
            for member in tar.getmembers():
                self.assertFalse(member.name.startswith('/'))
                self.assertNotIn('..', Path(member.name).parts)

    def test_parent_layers_gate_current_bundle_and_child_crd_digests(self):
        crds, platform = [json.loads(item) for item in self.files['layers.yaml'].split(b'\n---\n')]
        self.assertEqual(crds['spec']['sourceRef'], platform['spec']['sourceRef'])
        self.assertEqual(platform['spec']['dependsOn'], [{'name': 'crds'}])
        self.assertNotIn('readyExpr', platform['spec']['dependsOn'][0])
        self.assertEqual(crds['spec']['postBuild'], platform['spec']['postBuild'])
        health = crds['spec']['healthCheckExprs'][0]['current']
        self.assertIn('status.lastAppliedRevision', health)
        self.assertIn(self.digest, health)
        self.assertIn('status.observedGeneration', health)

    def test_archive_is_reproducible(self):
        self.assertEqual(release.archive(self.files), release.archive(self.files))

    def test_missing_digest_fails(self):
        with self.assertRaises(ValueError):
            release.source('bad', 'ghcr.io/example/chart@latest')


class RegistryTests(unittest.TestCase):
    def setUp(self):
        self.registry = release.Registry('example/platform', 'user', 'token')

    def test_existing_version_fails_before_upload(self):
        with patch.object(self.registry, 'manifest', return_value='sha256:' + 'a' * 64), patch.object(self.registry, 'blob') as upload:
            with self.assertRaisesRegex(RuntimeError, 'immutable release already exists'):
                self.registry.publish('example/platform/chart', '0.7.0', b'data', {})
            upload.assert_not_called()

    def test_only_confirmed_not_found_allows_new_version(self):
        error = urllib.error.HTTPError('https://ghcr.io', 404, 'not found', {}, io.BytesIO(b'{"errors":[{"code":"MANIFEST_UNKNOWN"}]}'))
        with patch('urllib.request.urlopen', side_effect=error):
            self.assertIsNone(self.registry.manifest('example/platform/chart', '0.7.0', missing=True))

    def test_registry_auth_and_server_failures_do_not_mean_absent(self):
        for status in (401, 403, 429, 500, 502):
            error = urllib.error.HTTPError('https://ghcr.io', status, 'failed', {}, io.BytesIO(b''))
            with self.subTest(status=status), patch('urllib.request.urlopen', side_effect=error):
                with self.assertRaises(RuntimeError):
                    self.registry.assert_absent('example/platform/chart', '0.7.0')

    def test_unknown_not_found_is_rejected(self):
        for body in (b'', b'{}', b'{"errors":[{"code":"UNAUTHORIZED"}]}'):
            error = urllib.error.HTTPError('https://ghcr.io', 404, 'failed', {}, io.BytesIO(body))
            with self.subTest(body=body), patch('urllib.request.urlopen', side_effect=error):
                with self.assertRaises(RuntimeError):
                    self.registry.assert_absent('example/platform/chart', '0.7.0')

    def test_publish_rechecks_tag_after_blob_upload(self):
        with patch.object(self.registry, 'assert_absent', side_effect=[None, RuntimeError('occupied')]) as absent, patch.object(self.registry, 'blob', return_value='sha256:' + 'a' * 64), patch.object(self.registry, 'request') as request:
            with self.assertRaisesRegex(RuntimeError, 'occupied'):
                self.registry.publish('example/platform/chart', '0.7.0', b'data', {})
            self.assertEqual(absent.call_count, 2)
            request.assert_not_called()

    def test_anonymous_registry_auth_requests_pull_only(self):
        registry = release.Registry('example/platform', '', '')
        headers = {'WWW-Authenticate': 'Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:example/platform/chart:pull"'}
        challenge = urllib.error.HTTPError('https://ghcr.io', 401, 'auth', headers, io.BytesIO(b''))
        token = io.BytesIO(b'{"token":"anonymous"}')
        manifest = io.BytesIO(b'{"schemaVersion":2}')
        manifest.headers = {}
        with patch('urllib.request.urlopen', side_effect=[challenge, token, manifest]) as request:
            registry.manifest('example/platform/chart', '0.7.0')
        token_request = request.call_args_list[1].args[0]
        self.assertIn('%3Apull', token_request.full_url)
        self.assertNotIn('push', token_request.full_url)
        self.assertIsNone(token_request.get_header('Authorization'))

    def test_valid_versions_only(self):
        for version in ('0.7.0', '0.7.0-rc.1'):
            self.assertIsNotNone(release.VERSION.fullmatch(version))
        for version in ('v0.7.0', '0.7', '0.7.0-dev.1', '0.7.0-rc.01', '00.7.0', '../latest', '0.7.0+build'):
            self.assertIsNone(release.VERSION.fullmatch(version))


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        (self.root / 'charts/loco-core/charts').mkdir(parents=True)
        (self.root / 'charts/loco-core/charts/gateway-helm-test.tgz').write_bytes(b'gateway')
        (self.root / 'helmfile.yaml.gotmpl').write_text('chart: jetstack/cert-manager\n  version: fixture-version\n')
        self.commit = 'b' * 40
        self.published = []
        self.calls = []
        self.registry = unittest.mock.Mock(spec=release.Registry, repository='example/platform')
        self.registry.manifest.return_value = 'sha256:' + 'a' * 64
        self.registry.publish.side_effect = self.publish
        self.public = unittest.mock.Mock()
        self.public.manifest.return_value = 'sha256:' + 'a' * 64
        for patcher in (patch.object(release, 'ROOT', self.root), patch.object(release, 'validate_identity'), patch.object(release, 'run', side_effect=self.command), patch.object(release, 'protected_crd', return_value=b'crd'), patch.object(release, 'gateway_crds', return_value=b'gateway'), patch.object(release, 'Registry', return_value=self.public)):
            patcher.start()
            self.addCleanup(patcher.stop)

    def command(self, *args):
        self.calls.append(args)
        if args[:2] == ('helm', 'package'):
            name = Path(args[2]).name
            (Path(args[-1]) / (name + '-0.7.0-rc.1.tgz')).write_bytes(b'chart')
        if args[:2] == ('helm', 'pull'):
            (Path(args[-1]) / 'cert-manager-fixture-version.tgz').write_bytes(b'cert')
        if args[0] == 'yq':
            return '{"name":"fixture","version":"0.0.0"}'
        return ''

    def publish(self, repository, version, data, config, *args):
        self.published.append(repository)
        return 'sha256:' + 'a' * 64

    def test_bundle_is_last_after_all_artifacts_and_provenance(self):
        with patch('builtins.print'):
            release.publish('0.7.0-rc.1', self.commit, self.registry)
        self.assertEqual(len(self.published), 9)
        self.assertEqual(self.published[-1], 'example/platform/platform')
        self.assertEqual(self.registry.assert_absent.call_count, 9)
        attestations = [call for call in self.calls if call[:3] == ('gh', 'attestation', 'verify')]
        self.assertEqual(len(attestations), len(release.IMAGES))
        for call in attestations:
            self.assertIn('refs/heads/main', call)
            self.assertIn(self.commit, call)
            self.assertIn('example/platform/.github/workflows/build-push.yml', call)
            self.assertIn('--deny-self-hosted-runners', call)
            self.assertIn('@sha256:', call[3])

    def test_any_existing_tag_aborts_before_images_or_uploads(self):
        self.registry.assert_absent.side_effect = RuntimeError('exists')
        with self.assertRaisesRegex(RuntimeError, 'exists'):
            release.publish('0.7.0-rc.1', self.commit, self.registry)
        self.registry.manifest.assert_not_called()
        self.registry.publish.assert_not_called()

    def test_failed_artifact_never_publishes_bundle(self):
        self.registry.publish.side_effect = RuntimeError('upload failed')
        with self.assertRaisesRegex(RuntimeError, 'upload failed'):
            release.publish('0.7.0-rc.1', self.commit, self.registry)
        self.assertNotIn('example/platform/platform', self.published)

    def test_private_artifact_never_publishes_bundle(self):
        self.public.manifest.side_effect = ['sha256:' + 'a' * 64] * len(release.IMAGES) + [RuntimeError('unauthorized')]
        with self.assertRaisesRegex(RuntimeError, 'unauthorized'):
            release.publish('0.7.0-rc.1', self.commit, self.registry)
        self.assertNotIn('example/platform/platform', self.published)

    def test_unverified_image_never_publishes_artifacts(self):
        with patch.object(release, 'run', side_effect=RuntimeError('invalid provenance')):
            with self.assertRaisesRegex(RuntimeError, 'invalid provenance'):
                release.publish('0.7.0-rc.1', self.commit, self.registry)
        self.registry.publish.assert_not_called()


if __name__ == '__main__':
    unittest.main()
