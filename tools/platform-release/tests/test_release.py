import importlib.util
import io
import json
import tarfile
import tempfile
import urllib.error
from contextlib import ExitStack
from functools import partial
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch

import pytest

SPEC = importlib.util.spec_from_file_location(
    "release", Path(__file__).parents[1] / "release.py"
)
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)


@pytest.fixture
def bundle():
    bundle = SimpleNamespace()
    bundle.digest = "sha256:" + "a" * 64
    bundle.commit = "b" * 40
    names = (
        *release.CHARTS,
        "loco-applications-crd",
        "loco-builds-crd",
        "loco-gateway-crd",
        "cert-manager-chart",
    )
    bundle.refs = {
        name: "ghcr.io/example/platform/" + name + "@" + bundle.digest for name in names
    }
    bundle.images = {
        name: "ghcr.io/example/platform-" + name + "@" + bundle.digest
        for name in release.IMAGES
    }
    bundle.files = release.bundle(
        "0.7.0-rc.1", bundle.commit, bundle.refs, bundle.images
    )
    return bundle


def test_bundle_pins_every_source_and_image(bundle):
    resources = [
        json.loads(value)
        for path, value in bundle.files.items()
        if path.endswith(".yaml") and path != "layers.yaml"
    ]
    sources = [value for value in resources if value["kind"] == "OCIRepository"]
    assert len(sources) == 7
    for source in sources:
        assert source["spec"]["ref"] == {"digest": bundle.digest}
    lock = json.loads(bundle.files["release.json"])
    assert lock["coreCommit"] == bundle.commit
    assert lock["images"] == bundle.images
    core = json.loads(bundle.files["releases/loco-core-helmrelease.yaml"])
    assert (
        core["spec"]["values"]["agent"]["image"]["tag"]
        == f"sha-{bundle.commit}@{bundle.digest}"
    )


def test_crds_are_protected_and_owned_separately(bundle):
    operator = json.loads(bundle.files["releases/loco-operator-helmrelease.yaml"])
    assert "crd" not in operator["spec"]["values"]
    for name in ("applications", "builds", "gateway"):
        ks = json.loads(bundle.files[f"crds/loco-{name}-crd-kustomization.yaml"])
        assert not ks["spec"]["prune"]
        assert ks["spec"]["wait"]


def test_control_plane_chart_is_published_without_installing_on_workers(bundle):
    assert "loco-control-plane" in release.CHARTS
    assert not [name for name in bundle.files if "loco-control-plane" in name]


def test_only_the_core_release_reads_secret_values(bundle):
    for name in ("loco-core", "loco-operator", "loco-obs"):
        hr = json.loads(bundle.files[f"releases/{name}-helmrelease.yaml"])
        secrets = [source for source in hr["spec"]["valuesFrom"] if source["kind"] == "Secret"]
        assert bool(secrets) == (name == "loco-core"), name


def test_releases_wait_for_matching_release_and_current_generation(bundle):
    for name in ("loco-core", "loco-operator", "loco-obs"):
        hr = json.loads(bundle.files[f"releases/{name}-helmrelease.yaml"])
        assert hr["metadata"]["labels"]["platform.loco.io/release"] == "0.7.0-rc.1"
        for dep in hr["spec"]["dependsOn"]:
            assert "dep.status.observedGeneration" in dep["readyExpr"]
            assert "platform.loco.io/release" in dep["readyExpr"]
        assert hr["spec"]["upgrade"]["remediation"]["strategy"] == "rollback"


def test_bundle_archive_contains_only_declared_files(bundle):
    data = release.archive(bundle.files)
    with tarfile.open(fileobj=io.BytesIO(data)) as tar:
        assert set(tar.getnames()) == set(bundle.files)
        for member in tar.getmembers():
            assert not member.name.startswith("/")
            assert ".." not in Path(member.name).parts


def test_parent_layers_gate_current_bundle_and_child_crd_digests(bundle):
    crds, platform = [
        json.loads(item) for item in bundle.files["layers.yaml"].split(b"\n---\n")
    ]
    assert crds["spec"]["sourceRef"] == platform["spec"]["sourceRef"]
    assert platform["spec"]["dependsOn"] == [{"name": "crds"}]
    assert "readyExpr" not in platform["spec"]["dependsOn"][0]
    assert crds["spec"]["postBuild"] == platform["spec"]["postBuild"]
    health = crds["spec"]["healthCheckExprs"][0]["current"]
    assert "status.lastAppliedRevision" in health
    assert bundle.digest in health
    assert "status.observedGeneration" in health


def test_archive_is_reproducible(bundle):
    assert release.archive(bundle.files) == release.archive(bundle.files)


def test_missing_digest_fails(bundle):
    with pytest.raises(ValueError):
        release.source("bad", "ghcr.io/example/chart@latest")


@pytest.fixture
def registry():
    return release.Registry("example/platform", "user", "token")


def test_image_index_requires_both_architectures(registry, subtests):
    amd64 = {"platform": {"os": "linux", "architecture": "amd64"}}
    arm64 = {"platform": {"os": "linux", "architecture": "arm64", "variant": "v8"}}
    attestation = {"platform": {"os": "unknown", "architecture": "unknown"}}
    for manifests, valid in (
        ([amd64, arm64, attestation], True),
        ([amd64, attestation], False),
        ([arm64], False),
        ([amd64, arm64, arm64], False),
    ):
        data = release.encoded({"manifests": manifests})
        with (
            subtests.test(manifests=manifests),
            patch.object(registry, "request", return_value=(data, {})),
        ):
            if valid:
                registry.require_image_platforms(
                    "example/platform-api", release.digest(data)
                )
            else:
                with pytest.raises(RuntimeError, match="exactly one linux/"):
                    registry.require_image_platforms(
                        "example/platform-api", release.digest(data)
                    )


def test_image_index_content_must_match_digest(registry):
    with patch.object(registry, "request", return_value=(b"{}", {})):
        with pytest.raises(RuntimeError, match="digest mismatch"):
            registry.require_image_platforms(
                "example/platform-api", "sha256:" + "a" * 64
            )


def test_existing_version_fails_before_upload(registry):
    with (
        patch.object(registry, "manifest", return_value="sha256:" + "a" * 64),
        patch.object(registry, "blob") as upload,
    ):
        with pytest.raises(RuntimeError, match="immutable release already exists"):
            registry.publish("example/platform/chart", "0.7.0", b"data", {})
        upload.assert_not_called()


def test_only_confirmed_not_found_allows_new_version(registry):
    error = urllib.error.HTTPError(
        "https://ghcr.io",
        404,
        "not found",
        {},
        io.BytesIO(b'{"errors":[{"code":"MANIFEST_UNKNOWN"}]}'),
    )
    with patch("urllib.request.urlopen", side_effect=error):
        assert (
            registry.manifest("example/platform/chart", "0.7.0", missing=True) is None
        )


def test_registry_auth_and_server_failures_do_not_mean_absent(registry, subtests):
    for status in (401, 403, 429, 500, 502):
        error = urllib.error.HTTPError(
            "https://ghcr.io", status, "failed", {}, io.BytesIO(b"")
        )
        with (
            subtests.test(status=status),
            patch("urllib.request.urlopen", side_effect=error),
        ):
            with pytest.raises(RuntimeError):
                registry.assert_absent("example/platform/chart", "0.7.0")


def test_unknown_not_found_is_rejected(registry, subtests):
    for body in (b"", b"{}", b'{"errors":[{"code":"UNAUTHORIZED"}]}'):
        error = urllib.error.HTTPError(
            "https://ghcr.io", 404, "failed", {}, io.BytesIO(body)
        )
        with (
            subtests.test(body=body),
            patch("urllib.request.urlopen", side_effect=error),
        ):
            with pytest.raises(RuntimeError):
                registry.assert_absent("example/platform/chart", "0.7.0")


def test_publish_rechecks_tag_after_blob_upload(registry):
    with (
        patch.object(
            registry,
            "assert_absent",
            side_effect=[None, RuntimeError("occupied")],
        ) as absent,
        patch.object(registry, "blob", return_value="sha256:" + "a" * 64),
        patch.object(registry, "request") as request,
    ):
        with pytest.raises(RuntimeError, match="occupied"):
            registry.publish("example/platform/chart", "0.7.0", b"data", {})
        assert absent.call_count == 2
        request.assert_not_called()


def test_anonymous_registry_auth_requests_pull_only(registry):
    registry = release.Registry("example/platform", "", "")
    headers = {
        "WWW-Authenticate": 'Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:example/platform/chart:pull"'
    }
    challenge = urllib.error.HTTPError(
        "https://ghcr.io", 401, "auth", headers, io.BytesIO(b"")
    )
    token = io.BytesIO(b'{"token":"anonymous"}')
    manifest = io.BytesIO(b'{"schemaVersion":2}')
    manifest.headers = {}
    with patch(
        "urllib.request.urlopen", side_effect=[challenge, token, manifest]
    ) as request:
        registry.manifest("example/platform/chart", "0.7.0")
    token_request = request.call_args_list[1].args[0]
    assert "%3Apull" in token_request.full_url
    assert "push" not in token_request.full_url
    assert token_request.get_header("Authorization") is None


def test_valid_versions_only(registry):
    for version in ("0.7.0", "0.7.0-rc.1"):
        assert release.VERSION.fullmatch(version) is not None
    for version in (
        "v0.7.0",
        "0.7",
        "0.7.0-dev.1",
        "0.7.0-rc.01",
        "00.7.0",
        "../latest",
        "0.7.0+build",
    ):
        assert release.VERSION.fullmatch(version) is None


@pytest.fixture
def publication():
    with ExitStack() as cleanup:
        publication = SimpleNamespace()
        publication.directory = tempfile.TemporaryDirectory()
        cleanup.callback(publication.directory.cleanup)
        publication.root = Path(publication.directory.name)
        (publication.root / "charts/loco-core/charts").mkdir(parents=True)
        (
            publication.root / "charts/loco-core/charts/gateway-helm-test.tgz"
        ).write_bytes(b"gateway")
        publication.commit = "b" * 40
        publication.published = []
        publication.calls = []
        publication.registry = Mock(
            spec=release.Registry, repository="example/platform"
        )
        publication.registry.manifest.return_value = "sha256:" + "a" * 64
        publication.registry.publish.side_effect = partial(_publish, publication)
        publication.public = Mock()
        publication.public.manifest.return_value = "sha256:" + "a" * 64
        for patcher in (
            patch.object(release, "ROOT", publication.root),
            patch.object(release, "validate_identity"),
            patch.object(release, "run", side_effect=partial(_command, publication)),
            patch.object(release, "protected_crd", return_value=b"crd"),
            patch.object(release, "gateway_crds", return_value=b"gateway"),
            patch.object(release, "Registry", return_value=publication.public),
        ):
            patcher.start()
            cleanup.callback(patcher.stop)
        yield publication


def _command(publication, *args):
    publication.calls.append(args)
    if args[:2] == ("helm", "package"):
        name = Path(args[2]).name
        (Path(args[-1]) / (name + "-0.7.0-rc.1.tgz")).write_bytes(b"chart")
    if args[:2] == ("helm", "pull"):
        (Path(args[-1]) / "cert-manager-fixture-version.tgz").write_bytes(b"cert")
    if args[0] == "yq" and args[-1].endswith("charts/cert-manager.yaml"):
        return '{"repository":"https://charts.example.test","chart":"cert-manager","version":"fixture-version"}'
    if args[0] == "yq":
        return '{"name":"fixture","version":"0.0.0"}'
    return ""


def _publish(publication, repository, version, data, config, *args):
    publication.published.append(repository)
    return "sha256:" + "a" * 64


def test_bundle_is_last_after_all_artifacts_and_provenance(publication):
    with patch("builtins.print"):
        release.publish("0.7.0-rc.1", publication.commit, publication.registry)
    assert len(publication.published) == 9
    assert publication.published[-1] == "example/platform/platform"
    assert publication.registry.assert_absent.call_count == 9
    attestations = [
        call
        for call in publication.calls
        if call[:3] == ("gh", "attestation", "verify")
    ]
    assert len(attestations) == len(release.IMAGES)
    assert publication.registry.require_image_platforms.call_count == len(
        release.IMAGES
    )
    for call in attestations:
        assert "refs/heads/main" in call
        assert publication.commit in call
        assert "example/platform/.github/workflows/build-push.yml" in call
        assert "--deny-self-hosted-runners" in call
        assert "@sha256:" in call[3]


def test_single_architecture_image_never_publishes_artifacts(publication):
    publication.registry.require_image_platforms.side_effect = RuntimeError(
        "missing linux/arm64"
    )
    with pytest.raises(RuntimeError, match="missing linux/arm64"):
        release.publish("0.7.0-rc.1", publication.commit, publication.registry)
    publication.registry.publish.assert_not_called()


def test_any_existing_tag_aborts_before_images_or_uploads(publication):
    publication.registry.assert_absent.side_effect = RuntimeError("exists")
    with pytest.raises(RuntimeError, match="exists"):
        release.publish("0.7.0-rc.1", publication.commit, publication.registry)
    publication.registry.manifest.assert_not_called()
    publication.registry.publish.assert_not_called()


def test_failed_artifact_never_publishes_bundle(publication):
    publication.registry.publish.side_effect = RuntimeError("upload failed")
    with pytest.raises(RuntimeError, match="upload failed"):
        release.publish("0.7.0-rc.1", publication.commit, publication.registry)
    assert "example/platform/platform" not in publication.published


def test_private_artifact_never_publishes_bundle(publication):
    publication.public.manifest.side_effect = ["sha256:" + "a" * 64] * len(
        release.IMAGES
    ) + [RuntimeError("unauthorized")]
    with pytest.raises(RuntimeError, match="unauthorized"):
        release.publish("0.7.0-rc.1", publication.commit, publication.registry)
    assert "example/platform/platform" not in publication.published


def test_unverified_image_never_publishes_artifacts(publication):
    with patch.object(release, "run", side_effect=RuntimeError("invalid provenance")):
        with pytest.raises(RuntimeError, match="invalid provenance"):
            release.publish("0.7.0-rc.1", publication.commit, publication.registry)
    publication.registry.publish.assert_not_called()
