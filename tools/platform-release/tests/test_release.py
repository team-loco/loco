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


def test_bundle_pins_every_source_by_digest(bundle):
    resources = [
        json.loads(value)
        for path, value in bundle.files.items()
        if path.endswith(".yaml") and path != "layers.yaml"
    ]
    sources = [value for value in resources if value["kind"] == "OCIRepository"]
    assert len(sources) == 5
    for source in sources:
        assert source["spec"]["ref"] == {"digest": bundle.digest}


def test_bundle_lock_records_the_commit_and_images(bundle):
    lock = json.loads(bundle.files["release.json"])
    assert lock["coreCommit"] == bundle.commit
    assert lock["images"] == bundle.images


def test_worker_release_pins_each_image_by_commit_and_digest(bundle):
    worker = json.loads(bundle.files["releases/loco-worker-helmrelease.yaml"])
    values = worker["spec"]["values"]
    for image in (
        values["agent"]["image"],
        values["controller"]["image"],
        values["builds"]["builderImage"],
    ):
        assert image["tag"] == f"sha-{bundle.commit}@{bundle.digest}"
    assert values["agent"]["image"]["repository"] == "ghcr.io/example/platform-agent"
    assert values["controller"]["image"]["repository"] == "ghcr.io/example/platform-controller"
    assert values["builds"]["builderImage"]["repository"] == "ghcr.io/example/platform-builder"


def test_worker_release_does_not_own_crds(bundle):
    worker = json.loads(bundle.files["releases/loco-worker-helmrelease.yaml"])
    assert "crd" not in worker["spec"]["values"]


def test_crd_kustomizations_wait_without_pruning(bundle):
    for name in ("applications", "builds", "gateway"):
        ks = json.loads(bundle.files[f"crds/loco-{name}-crd-kustomization.yaml"])
        assert not ks["spec"]["prune"]
        assert ks["spec"]["wait"]


def test_control_plane_chart_is_published_without_installing_on_workers(bundle):
    assert "loco-control-plane" in release.CHARTS
    assert not [name for name in bundle.files if "loco-control-plane" in name]


def test_worker_release_reads_its_values_sources(bundle):
    hr = json.loads(bundle.files["releases/loco-worker-helmrelease.yaml"])
    assert hr["spec"]["targetNamespace"] == "loco-system"
    assert hr["spec"]["install"]["createNamespace"] is False
    assert [(source["kind"], source["name"]) for source in hr["spec"]["valuesFrom"]] == [
        ("ConfigMap", "loco-worker-values"),
        ("Secret", "loco-worker-secrets"),
    ]
    assert [dep["name"] for dep in hr["spec"]["dependsOn"]] == ["cert-manager"]
    for dep in hr["spec"]["dependsOn"]:
        assert f"releases/{dep['name']}-helmrelease.yaml" in bundle.files


def test_releases_carry_the_bundle_release_label(bundle):
    hr = json.loads(bundle.files["releases/loco-worker-helmrelease.yaml"])
    assert hr["metadata"]["labels"]["platform.loco.io/release"] == "0.7.0-rc.1"


def test_release_dependencies_must_be_current_at_the_same_release(bundle):
    hr = json.loads(bundle.files["releases/loco-worker-helmrelease.yaml"])
    for dep in hr["spec"]["dependsOn"]:
        assert "dep.status.observedGeneration" in dep["readyExpr"]
        assert "platform.loco.io/release" in dep["readyExpr"]


def test_failed_release_upgrades_roll_back(bundle):
    hr = json.loads(bundle.files["releases/loco-worker-helmrelease.yaml"])
    assert hr["spec"]["upgrade"]["remediation"]["strategy"] == "rollback"


def test_bundle_archive_contains_only_declared_files(bundle):
    data = release.archive(bundle.files)
    with tarfile.open(fileobj=io.BytesIO(data)) as tar:
        assert set(tar.getnames()) == set(bundle.files)
        for member in tar.getmembers():
            assert not member.name.startswith("/")
            assert ".." not in Path(member.name).parts


def _layers(bundle):
    crds, platform = [
        json.loads(item) for item in bundle.files["layers.yaml"].split(b"\n---\n")
    ]
    return crds, platform


def test_layers_build_from_one_source_with_one_substitution_set(bundle):
    crds, platform = _layers(bundle)
    assert crds["spec"]["sourceRef"] == platform["spec"]["sourceRef"]
    assert crds["spec"]["postBuild"] == platform["spec"]["postBuild"]


def test_platform_layer_waits_for_the_crds_layer(bundle):
    _, platform = _layers(bundle)
    assert platform["spec"]["dependsOn"] == [{"name": "crds"}]
    assert "readyExpr" not in platform["spec"]["dependsOn"][0]


def test_crds_layer_is_healthy_only_at_the_current_bundle_revision(bundle):
    crds, _ = _layers(bundle)
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
            registry.publish("example/platform/chart", "0.7.0", release.archive({}), {})
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


def test_ensure_publishes_an_absent_version(registry):
    with (
        patch.object(registry, "annotations", return_value=None),
        patch.object(registry, "publish", return_value="sha256:" + "a" * 64) as publish,
    ):
        registry.ensure("example/platform/chart", "0.1.0", b"data", {})
    publish.assert_called_once()


def test_ensure_skips_a_version_published_with_the_same_content(registry):
    data = release.archive({"Chart.yaml": b"name: chart"})
    annotations = {release.CONTENT_ANNOTATION: release.content(data)}
    with (
        patch.object(registry, "annotations", return_value=annotations),
        patch.object(registry, "manifest", return_value="sha256:" + "a" * 64),
        patch.object(registry, "publish") as publish,
    ):
        assert registry.ensure("example/platform/chart", "0.1.0", data, {}) == "sha256:" + "a" * 64
    publish.assert_not_called()


def test_ensure_rejects_a_version_published_with_other_content(registry, subtests):
    data = release.archive({"Chart.yaml": b"name: chart"})
    other = release.archive({"Chart.yaml": b"name: other"})
    for annotations in ({release.CONTENT_ANNOTATION: release.content(other)}, {}):
        with (
            subtests.test(annotations=annotations),
            patch.object(registry, "annotations", return_value=annotations),
            patch.object(registry, "publish") as publish,
        ):
            with pytest.raises(RuntimeError, match="different content"):
                registry.ensure("example/platform/chart", "0.1.0", data, {})
            publish.assert_not_called()


def test_content_ignores_archive_metadata():
    files = {"chart/Chart.yaml": b"name: chart", "chart/values.yaml": b"{}"}
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as tar:
        for name, data in reversed(files.items()):
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mtime = 1234567890
            tar.addfile(info, io.BytesIO(data))
    assert release.content(output.getvalue()) == release.content(release.archive(files))


def test_published_manifest_records_content(registry):
    data = release.archive({"Chart.yaml": b"name: chart"})
    with (
        patch.object(registry, "assert_absent"),
        patch.object(registry, "blob", return_value="sha256:" + "a" * 64),
        patch.object(registry, "request") as request,
        patch.object(registry, "manifest", side_effect=lambda *args: release.digest(request.call_args.kwargs["data"])),
    ):
        registry.publish("example/platform/chart", "0.1.0", data, {})
    manifest = json.loads(request.call_args.kwargs["data"])
    assert manifest["annotations"][release.CONTENT_ANNOTATION] == release.content(data)


VERSIONS = {
    "loco-worker": "0.3.0",
    "loco-control-plane": "0.2.0",
    "loco-applications-crd": "0.1.1",
    "loco-builds-crd": "0.1.0",
    "loco-gateway-crd": "0.4.0",
    "cert-manager-chart": "v1.0.0",
}


@pytest.fixture
def publication():
    with ExitStack() as cleanup:
        publication = SimpleNamespace()
        publication.directory = tempfile.TemporaryDirectory()
        cleanup.callback(publication.directory.cleanup)
        publication.root = Path(publication.directory.name)
        (publication.root / "charts/loco-worker/charts").mkdir(parents=True)
        (
            publication.root / "charts/loco-worker/charts/gateway-helm-test.tgz"
        ).write_bytes(b"gateway")
        publication.commit = "b" * 40
        publication.calls = []
        publication.registry = Mock(
            spec=release.Registry, repository="example/platform"
        )
        publication.registry.manifest.return_value = "sha256:" + "a" * 64
        publication.registry.ensure.return_value = "sha256:" + "a" * 64
        publication.registry.publish.return_value = "sha256:" + "a" * 64
        publication.public = Mock()
        publication.public.manifest.return_value = "sha256:" + "a" * 64
        for patcher in (
            patch.object(release, "ROOT", publication.root),
            patch.object(release, "validate_commit"),
            patch.object(release, "versions", return_value=dict(VERSIONS)),
            patch.object(release, "value", side_effect=_value),
            patch.object(release, "run", side_effect=partial(_command, publication)),
            patch.object(release, "protected_crd", return_value=b"crd"),
            patch.object(release, "gateway_crds", return_value=b"gateway"),
            patch.object(release, "Registry", return_value=publication.public),
        ):
            patcher.start()
            cleanup.callback(patcher.stop)
        yield publication


def _value(path, expression, ref=None):
    if path == release.CERT_MANAGER:
        return {"repository": "https://charts.example.test", "chart": "cert-manager", "version": VERSIONS["cert-manager-chart"]}
    return {"name": Path(path).parent.name, "version": VERSIONS[Path(path).parent.name]}


def _command(publication, *args):
    publication.calls.append(args)
    if args[:2] == ("helm", "package"):
        name = Path(args[2]).name
        (Path(args[-1]) / f"{name}-{VERSIONS[name]}.tgz").write_bytes(name.encode())
    if args[:2] == ("helm", "pull"):
        (Path(args[-1]) / "cert-manager-fixture.tgz").write_bytes(b"cert")
    return ""


@pytest.fixture
def published_artifacts(publication):
    with patch("builtins.print"):
        release.publish_artifacts(publication.commit, publication.registry)
    return publication


def test_artifacts_publish_each_chart_and_crd_at_its_own_version(published_artifacts):
    ensured = {call.args[0]: call.args[1] for call in published_artifacts.registry.ensure.call_args_list}
    names = release.repositories(published_artifacts.registry)
    assert ensured == {names[name]: version for name, version in VERSIONS.items()}
    data = {call.args[0]: call.args[2] for call in published_artifacts.registry.ensure.call_args_list}
    assert data[names["loco-worker"]] == b"loco-worker"
    assert data[names["loco-gateway-crd"]] == b"gateway"


def test_artifacts_look_up_each_version_once(published_artifacts):
    assert published_artifacts.public.manifest.call_count == len(VERSIONS)


def test_artifacts_publish_no_bundle(published_artifacts):
    published_artifacts.registry.publish.assert_not_called()
    assert not [call for call in published_artifacts.calls if call[:2] == ("gh", "attestation")]


def test_artifacts_stop_at_a_version_published_with_other_content(publication):
    publication.registry.ensure.side_effect = RuntimeError("different content")
    with pytest.raises(RuntimeError, match="different content"):
        release.publish_artifacts(publication.commit, publication.registry)
    publication.public.manifest.assert_not_called()


def test_private_artifact_fails_publication(publication):
    publication.public.manifest.side_effect = RuntimeError("unauthorized")
    with pytest.raises(RuntimeError, match="unauthorized"):
        release.publish_artifacts(publication.commit, publication.registry)


@pytest.fixture
def published_bundle(publication):
    with patch("builtins.print"):
        release.publish_bundle("0.7.0-rc.1", publication.commit, publication.registry)
    return publication


def test_bundle_pins_the_published_versions(published_bundle):
    names = release.repositories(published_bundle.registry)
    looked_up = {call.args[0]: call.args[1] for call in published_bundle.public.manifest.call_args_list if call.kwargs.get("missing")}
    assert looked_up == {names[name]: version for name, version in VERSIONS.items()}


def test_bundle_publishes_only_itself(published_bundle):
    names = release.repositories(published_bundle.registry)
    published_bundle.registry.ensure.assert_not_called()
    assert [call.args[0] for call in published_bundle.registry.publish.call_args_list] == [names["platform"]]
    published_bundle.registry.assert_absent.assert_called_once_with(names["platform"], "0.7.0-rc.1")
    assert not [call for call in published_bundle.calls if call[0] == "helm"]


def test_bundle_verifies_each_image_attestation_from_main(published_bundle):
    attestations = [
        call
        for call in published_bundle.calls
        if call[:3] == ("gh", "attestation", "verify")
    ]
    assert len(attestations) == len(release.IMAGES)
    for call in attestations:
        assert "refs/heads/main" in call
        assert published_bundle.commit in call
        assert "example/platform/.github/workflows/build-push.yml" in call
        assert "--deny-self-hosted-runners" in call
        assert "@sha256:" in call[3]


def test_bundle_requires_each_image_platform(published_bundle):
    assert published_bundle.registry.require_image_platforms.call_count == len(
        release.IMAGES
    )


def test_bundle_fails_when_a_pinned_version_is_not_published(publication):
    publication.public.manifest.side_effect = lambda repository, reference, missing=False: None if repository.endswith("crds/builds") else "sha256:" + "a" * 64
    with pytest.raises(RuntimeError, match="loco-builds-crd 0.1.0 is not published"):
        release.publish_bundle("0.7.0-rc.1", publication.commit, publication.registry)
    publication.registry.publish.assert_not_called()


def test_existing_bundle_version_aborts_before_images(publication):
    publication.registry.assert_absent.side_effect = RuntimeError("exists")
    with pytest.raises(RuntimeError, match="exists"):
        release.publish_bundle("0.7.0-rc.1", publication.commit, publication.registry)
    publication.registry.manifest.assert_not_called()
    publication.registry.publish.assert_not_called()


def test_single_architecture_image_never_publishes_bundle(publication):
    publication.registry.require_image_platforms.side_effect = RuntimeError(
        "missing linux/arm64"
    )
    with pytest.raises(RuntimeError, match="missing linux/arm64"):
        release.publish_bundle("0.7.0-rc.1", publication.commit, publication.registry)
    publication.registry.publish.assert_not_called()


def test_unverified_image_never_publishes_bundle(publication):
    with patch.object(release, "run", side_effect=RuntimeError("invalid provenance")):
        with pytest.raises(RuntimeError, match="invalid provenance"):
            release.publish_bundle("0.7.0-rc.1", publication.commit, publication.registry)
    publication.registry.publish.assert_not_called()


def test_invalid_bundle_version_fails():
    with pytest.raises(ValueError):
        release.publish_bundle("v0.7.0", "b" * 40, Mock())


WORKER_CHART = """apiVersion: v2
name: loco-worker
version: {version}
dependencies:
  - name: gateway-helm
    version: {gateway}
    repository: oci://docker.io/envoyproxy
  - name: opentelemetry-collector
    version: {otel}
    repository: https://open-telemetry.github.io/opentelemetry-helm-charts
"""


@pytest.fixture
def repository(tmp_path):
    def git(*args):
        release.subprocess.run(["git", *args], cwd=tmp_path, check=True, capture_output=True)

    def write(path, text):
        (tmp_path / path).parent.mkdir(parents=True, exist_ok=True)
        (tmp_path / path).write_text(text)

    write("charts/loco-worker/Chart.yaml", WORKER_CHART.format(version="0.1.0", gateway="1.0.0", otel="1.0.0"))
    write("charts/loco-worker/values.yaml", "replicas: 1\n")
    write("charts/loco-control-plane/Chart.yaml", "name: loco-control-plane\nversion: 0.1.0\n")
    write(release.CRD_SOURCES["applications"][0], "kind: CustomResourceDefinition\nmetadata: {name: applications}\n")
    write(release.CRD_SOURCES["builds"][0], "kind: CustomResourceDefinition\nmetadata: {name: builds}\n")
    write(release.CRD_VERSIONS, "applications: 0.1.0\nbuilds: 0.1.0\ngateway: 0.1.0\n")
    write("cmd/loco/main.go", "package main\n")
    git("init", "-q")
    git("add", ".")
    git("-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "base")
    with patch.object(release, "ROOT", tmp_path):
        yield SimpleNamespace(write=write)


def test_unchanged_artifacts_need_no_bump(repository):
    repository.write("cmd/loco/main.go", "package main\n\nfunc main() {}\n")
    assert release.unbumped("HEAD") == []


def test_changed_chart_needs_a_higher_version(repository, subtests):
    for version, stale in (("0.1.0", ["loco-worker"]), ("0.0.9", ["loco-worker"]), ("0.1.1-rc.0", []), ("0.2.0", [])):
        with subtests.test(version=version):
            repository.write("charts/loco-worker/values.yaml", "replicas: 2\n")
            repository.write("charts/loco-worker/Chart.yaml", WORKER_CHART.format(version=version, gateway="1.0.0", otel="1.0.0"))
            assert release.unbumped("HEAD") == stale


def test_changed_crd_needs_its_bundle_version_bumped(repository, subtests):
    repository.write(release.CRD_SOURCES["builds"][0], "kind: CustomResourceDefinition\nmetadata: {name: builds, labels: {a: b}}\n")
    for versions, stale in (("applications: 0.1.0\nbuilds: 0.1.0\ngateway: 0.1.0\n", ["loco-builds-crd"]), ("applications: 0.1.0\nbuilds: 0.1.1\ngateway: 0.1.0\n", [])):
        with subtests.test(versions=versions):
            repository.write(release.CRD_VERSIONS, versions)
            assert release.unbumped("HEAD") == stale


def test_gateway_dependency_bump_needs_chart_and_gateway_crd_bumps(repository, subtests):
    for chart, gateway, stale in (
        ("0.1.0", "0.1.0", ["loco-worker", "loco-gateway-crd"]),
        ("0.1.1", "0.1.0", ["loco-gateway-crd"]),
        ("0.1.1", "0.2.0", []),
    ):
        with subtests.test(chart=chart, gateway=gateway):
            repository.write("charts/loco-worker/Chart.yaml", WORKER_CHART.format(version=chart, gateway="1.1.0", otel="1.0.0"))
            repository.write(release.CRD_VERSIONS, f"applications: 0.1.0\nbuilds: 0.1.0\ngateway: {gateway}\n")
            assert release.unbumped("HEAD") == stale


def test_other_worker_dependency_bump_leaves_gateway_crds_alone(repository):
    repository.write("charts/loco-worker/Chart.yaml", WORKER_CHART.format(version="0.1.0", gateway="1.0.0", otel="1.1.0"))
    assert release.unbumped("HEAD") == ["loco-worker"]


def test_repository_versions_are_valid():
    versions = release.versions()
    assert set(versions) == set(release.repositories(release.Registry("example/platform", "", ""))) - {"platform"}
