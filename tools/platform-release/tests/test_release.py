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
        "gateway-chart",
    )
    bundle.refs = {
        name: "ghcr.io/example/platform/" + name + "@" + bundle.digest for name in names
    }
    bundle.images = {
        name: "ghcr.io/example/platform-" + name + "@" + bundle.digest
        for name in release.IMAGES
    }
    bundle.files = release.bundle(
        "0.7.0", bundle.commit, bundle.refs, bundle.images
    )
    return bundle


def test_bundle_pins_every_source_by_digest(bundle):
    sources = [json.loads(data) for path, data in bundle.files.items() if path.endswith(".yaml") and json.loads(data).get("kind") == "OCIRepository"]
    assert sources
    assert all(source["spec"]["ref"] == {"digest": bundle.digest} for source in sources)


def test_bundle_lock_records_the_commit_and_images(bundle):
    lock = json.loads(bundle.files["release.json"])
    assert lock == {"version": "0.7.0", "coreCommit": bundle.commit, "artifacts": bundle.refs, "images": bundle.images}


def helm_release(bundle, layer, name):
    return json.loads(bundle.files[f"layers/{layer}/{name}-helmrelease.yaml"])


def test_worker_release_pins_each_image_by_commit_and_digest(bundle):
    values = helm_release(bundle, "worker", "loco-worker")["spec"]["values"]
    for name, image in (("agent", values["agent"]["image"]), ("controller", values["controller"]["image"]), ("builder", values["builds"]["builderImage"])):
        assert image == {"repository": "ghcr.io/example/platform-" + name, "tag": f"sha-{bundle.commit}@{bundle.digest}"}
    assert values["gateway-helm"]["enabled"] is False


def test_control_plane_release_pins_its_images(bundle):
    values = helm_release(bundle, "control-plane", "loco-control-plane")["spec"]["values"]
    for field, name in (("api", "api"), ("ui", "ui"), ("obsProxy", "obs-proxy")):
        assert values[field]["image"] == {"repository": "ghcr.io/example/platform-" + name, "tag": f"sha-{bundle.commit}@{bundle.digest}"}


@pytest.mark.parametrize("layer,name", [("worker", "loco-worker"), ("control-plane", "loco-control-plane"), ("infra", "envoy-gateway"), ("infra", "cert-manager")])
def test_release_values_sources_are_optional(bundle, layer, name):
    spec = helm_release(bundle, layer, name)["spec"]
    assert spec["install"]["createNamespace"] is False
    assert spec["valuesFrom"] == [
        {"kind": kind, "name": name + suffix, "valuesKey": "values.yaml", "optional": True}
        for kind, suffix in (("ConfigMap", "-values"), ("Secret", "-secrets"))
    ]


def test_crd_kustomizations_wait_without_pruning(bundle):
    for name in ("applications", "builds", "gateway"):
        spec = json.loads(bundle.files[f"layers/crds/loco-{name}-crd-kustomization.yaml"])["spec"]
        assert spec["wait"] and not spec["prune"]


@pytest.mark.parametrize("entry,selected", [("control-plane", {"control-plane"}), ("worker", {"worker"}), ("all", {"control-plane", "worker"})])
def test_entry_points_select_their_layers(bundle, entry, selected):
    names = {path.removesuffix(".yaml") for path in json.loads(bundle.files[entry + "/kustomization.yaml"])["resources"]}
    assert names == {"namespaces", "crds", "infra"} | selected


def test_layers_wait_for_their_dependencies(bundle):
    for name, deps in (("namespaces", set()), ("crds", {"loco-namespaces"}), ("infra", {"loco-crds"}), ("control-plane", {"loco-infra", "loco-cluster-config"}), ("worker", {"loco-infra", "loco-cluster-config"})):
        spec = json.loads(bundle.files[f"all/{name}.yaml"])["spec"]
        assert {dep["name"] for dep in spec["dependsOn"]} == deps
        assert spec["wait"]
        assert spec.get("serviceAccountName") == (None if name == "namespaces" else "platform-reconciler")
        for dep in spec["dependsOn"]:
            if dep["name"] != "loco-cluster-config":
                assert "dep.status.observedGeneration" in dep["readyExpr"]
                assert "platform.loco.io/revision" in dep["readyExpr"]


@pytest.mark.parametrize("entry", ["control-plane", "worker", "all"])
@pytest.mark.parametrize("layer", ["namespaces", "crds"])
def test_persistent_layers_do_not_prune(bundle, entry, layer):
    assert json.loads(bundle.files[f"{entry}/{layer}.yaml"])["spec"]["prune"] is False


def test_crds_health_requires_the_expected_artifact_revision(bundle):
    spec = json.loads(bundle.files["all/crds.yaml"])["spec"]
    assert spec["prune"] is False
    health = spec["healthCheckExprs"][0]["current"]
    assert "platform.loco.io/artifact-digest" in health
    for name in ("applications", "builds", "gateway"):
        resource = json.loads(bundle.files[f"layers/crds/loco-{name}-crd-kustomization.yaml"])
        assert resource["metadata"]["annotations"]["platform.loco.io/artifact-digest"] == bundle.digest
    assert "status.lastAppliedRevision" in health and "status.observedGeneration" in health


def test_gateway_has_an_independent_release(bundle):
    spec = helm_release(bundle, "infra", "envoy-gateway")["spec"]
    assert spec["install"]["crds"] == spec["upgrade"]["crds"] == "Skip"
    assert spec["values"]["crds"]["enabled"] is False
    assert spec["values"]["config"]["envoyGateway"]["provider"]["kubernetes"]["deploy"]["type"] == "GatewayNamespace"


def test_bundle_namespace_layer_supplies_reconciliation_identity(bundle):
    objects = [json.loads(data) for path, data in bundle.files.items() if path.startswith("layers/namespaces/")]
    assert {obj["metadata"]["name"] for obj in objects if obj["kind"] == "Namespace"} == {"loco-system", "cert-manager"}
    namespace = next(obj for obj in objects if obj["kind"] == "Namespace" and obj["metadata"]["name"] == "loco-system")
    assert namespace["metadata"]["labels"]["pod-security.kubernetes.io/enforce"] == "restricted"
    account = next(obj for obj in objects if obj["kind"] == "ServiceAccount")
    assert account["metadata"] == {"name": "platform-reconciler", "namespace": "flux-system"}


def test_local_content_changes_advance_dependency_revision(bundle):
    files = release.bundle("0.7.0", bundle.commit, bundle.refs, {**bundle.images, "api": "ghcr.io/example/platform-api@sha256:" + "c"*64})
    for layer in ("namespaces", "crds", "infra", "control-plane", "worker"):
        before = json.loads(bundle.files[f"all/{layer}.yaml"])
        after = json.loads(files[f"all/{layer}.yaml"])
        assert before["spec"]["postBuild"] != after["spec"]["postBuild"]
        assert before["metadata"]["annotations"] != after["metadata"]["annotations"]


def test_bundle_archive_contains_only_declared_files(bundle):
    with tarfile.open(fileobj=io.BytesIO(release.archive(bundle.files))) as tar:
        assert set(tar.getnames()) == set(bundle.files)
        assert all(not name.startswith("/") and ".." not in Path(name).parts for name in tar.getnames())


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
    for version in ("0.7.0", "0.7.0"):
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
    "gateway-chart": "1.0.0",
}


@pytest.fixture
def publication():
    with ExitStack() as cleanup:
        publication = SimpleNamespace()
        publication.directory = tempfile.TemporaryDirectory()
        cleanup.callback(publication.directory.cleanup)
        publication.root = Path(publication.directory.name)
        (publication.root / "schemas").mkdir()
        (publication.root / "schemas/installation.v1.json").write_bytes((release.ROOT / "schemas/installation.v1.json").read_bytes())
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
    if path == 'manifests/namespaces/loco-system.yaml':
        return {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': 'loco-system', 'labels': {'pod-security.kubernetes.io/enforce': 'restricted'}}}
    if path == 'charts/loco-worker/Chart.yaml' and '.version' in expression:
        return VERSIONS['gateway-chart']
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
        release.publish_bundle("0.7.0", publication.commit, publication.registry)
    return publication


def test_bundle_pins_the_published_versions(published_bundle):
    names = release.repositories(published_bundle.registry)
    looked_up = {call.args[0]: call.args[1] for call in published_bundle.public.manifest.call_args_list if call.kwargs.get("missing")}
    assert looked_up == {names[name]: version for name, version in VERSIONS.items()}


def test_bundle_publishes_only_itself(published_bundle):
    names = release.repositories(published_bundle.registry)
    published_bundle.registry.ensure.assert_not_called()
    assert [call.args[0] for call in published_bundle.registry.publish.call_args_list] == [names["platform"]]
    published_bundle.registry.assert_absent.assert_called_once_with(names["platform"], "0.7.0")
    assert not [call for call in published_bundle.calls if call[0] == "helm"]


def test_bundle_prints_the_published_release_manifest(publication, capsys):
    release.publish_bundle("0.7.0", publication.commit, publication.registry)
    data = publication.registry.publish.call_args.args[2]
    with tarfile.open(fileobj=io.BytesIO(data)) as tar:
        published = tar.extractfile("release.json").read()
    assert json.loads(capsys.readouterr().out) == json.loads(published)


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
        release.publish_bundle("0.7.0", publication.commit, publication.registry)
    publication.registry.publish.assert_not_called()


def test_existing_bundle_version_aborts_before_images(publication):
    publication.registry.assert_absent.side_effect = RuntimeError("exists")
    with pytest.raises(RuntimeError, match="exists"):
        release.publish_bundle("0.7.0", publication.commit, publication.registry)
    publication.registry.manifest.assert_not_called()
    publication.registry.publish.assert_not_called()


def test_single_architecture_image_never_publishes_bundle(publication):
    publication.registry.require_image_platforms.side_effect = RuntimeError(
        "missing linux/arm64"
    )
    with pytest.raises(RuntimeError, match="missing linux/arm64"):
        release.publish_bundle("0.7.0", publication.commit, publication.registry)
    publication.registry.publish.assert_not_called()


def test_unverified_image_never_publishes_bundle(publication):
    with patch.object(release, "run", side_effect=RuntimeError("invalid provenance")):
        with pytest.raises(RuntimeError, match="invalid provenance"):
            release.publish_bundle("0.7.0", publication.commit, publication.registry)
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


def test_changed_crd_needs_its_bundle_version_bumped(repository, subtests):
    repository.write(release.CRD_SOURCES["builds"][0], "kind: CustomResourceDefinition\nmetadata: {name: builds, labels: {a: b}}\n")
    for versions, stale in (("applications: 0.1.0\nbuilds: 0.1.0\ngateway: 0.1.0\n", ["loco-builds-crd"]), ("applications: 0.1.0\nbuilds: 0.1.1\ngateway: 0.1.0\n", [])):
        with subtests.test(versions=versions):
            repository.write(release.CRD_VERSIONS, versions)
            assert release.unbumped("HEAD") == stale


def test_gateway_dependency_bump_needs_a_gateway_crd_bump(repository, subtests):
    for gateway, stale in (("0.1.0", ["loco-gateway-crd"]), ("0.2.0", [])):
        with subtests.test(gateway=gateway):
            repository.write("charts/loco-worker/Chart.yaml", WORKER_CHART.format(version="0.1.1", gateway="1.1.0", otel="1.0.0"))
            repository.write(release.CRD_VERSIONS, f"applications: 0.1.0\nbuilds: 0.1.0\ngateway: {gateway}\n")
            assert release.unbumped("HEAD") == stale


def test_other_worker_dependency_bump_leaves_gateway_crds_alone(repository):
    repository.write("charts/loco-worker/Chart.yaml", WORKER_CHART.format(version="0.1.1", gateway="1.0.0", otel="1.1.0"))
    assert release.unbumped("HEAD") == []


def test_repository_versions_are_valid():
    versions = release.versions()
    assert set(versions) == set(release.repositories(release.Registry("example/platform", "", ""))) - {"platform"}


def test_local_registry_uses_http_and_basic_auth():
    registry = release.Registry("example/platform", "push", "secret", "localhost:15000")
    challenge = urllib.error.HTTPError(
        "http://localhost:15000", 401, "auth", {"WWW-Authenticate": 'Basic realm="zot"'}, io.BytesIO(b"")
    )
    manifest = io.BytesIO(b'{"schemaVersion":2}')
    manifest.headers = {}
    with patch("urllib.request.urlopen", side_effect=[challenge, manifest]) as request:
        registry.manifest("example/platform/chart", "0.7.0")
    retried = request.call_args_list[1].args[0]
    assert retried.full_url == "http://localhost:15000/v2/example/platform/chart/manifests/0.7.0"
    assert retried.get_header("Authorization") == "Basic cHVzaDpzZWNyZXQ="


def test_basic_challenge_without_credentials_fails():
    registry = release.Registry("example/platform", "", "", "localhost:15000")
    challenge = urllib.error.HTTPError(
        "http://localhost:15000", 401, "auth", {"WWW-Authenticate": 'Basic realm="zot"'}, io.BytesIO(b"")
    )
    with patch("urllib.request.urlopen", side_effect=challenge):
        with pytest.raises(RuntimeError, match="unexpected registry authentication challenge"):
            registry.manifest("example/platform/chart", "0.7.0")


def test_replacing_publish_skips_the_absence_checks(registry):
    with (
        patch.object(registry, "assert_absent") as absent,
        patch.object(registry, "blob", return_value="sha256:" + "a" * 64),
        patch.object(registry, "request") as request,
        patch.object(registry, "manifest", side_effect=lambda *args: release.digest(request.call_args.kwargs["data"])),
    ):
        registry.publish("example/platform/chart", "0.0.0-local.gabc", release.archive({}), {}, replace=True)
    absent.assert_not_called()


def test_main_release_uses_the_newest_commit_with_every_image(publication, capsys):
    older, newer = "c" * 40, "d" * 40
    publication.registry.manifest.side_effect = lambda repository, reference, missing=False: (
        None if missing and reference == "sha-" + newer and repository.endswith("-ui") else "sha256:" + "a" * 64
    )
    commands = partial(_command, publication)
    with patch.object(release, "run", side_effect=lambda *args: f"{newer}\n{older}" if args[1] == "rev-list" else commands(*args)):
        release.main_release(None, publication.registry)
    lock = json.loads(capsys.readouterr().out)
    assert lock["coreCommit"] == older
    assert lock["version"] == "0.0.0-main.g" + older[:12]
    assert set(lock["images"]) == set(release.IMAGES)
    release.versions.assert_called_once_with(older)


def test_main_release_verifies_each_image_attestation(publication):
    with patch("builtins.print"):
        release.main_release(publication.commit, publication.registry)
    attestations = [call for call in publication.calls if call[:3] == ("gh", "attestation", "verify")]
    assert len(attestations) == len(release.IMAGES)
    assert ("git", "merge-base", "--is-ancestor", publication.commit, "origin/main") in publication.calls


def test_main_release_publishes_nothing(publication):
    with patch("builtins.print"):
        release.main_release(publication.commit, publication.registry)
    publication.registry.publish.assert_not_called()
    publication.registry.ensure.assert_not_called()


@pytest.fixture
def local_release(publication, capsys):
    publication.registry.host = "localhost:15000"
    publication.registry.username = "push"
    publication.registry.password = "secret"
    commands = partial(_command, publication)
    with patch.object(release, "run", side_effect=lambda *args: publication.commit if args[:2] == ("git", "rev-parse") else commands(*args)):
        release.local_release(publication.registry, "registry.example.localhost:15000")
    publication.lock = json.loads(capsys.readouterr().out)
    return publication


def test_local_release_replaces_each_artifact_at_the_local_version(local_release):
    names = release.repositories(local_release.registry)
    version = "0.0.0-local.g" + local_release.commit[:12]
    published = {call.args[0]: (call.args[1], call.kwargs) for call in local_release.registry.publish.call_args_list}
    assert published == {names[name]: (version, {"replace": True}) for name in (*VERSIONS, "platform")}
    local_release.registry.ensure.assert_not_called()


def test_local_release_builds_and_pushes_each_image(local_release):
    version = "0.0.0-local.g" + local_release.commit[:12]
    builds = [call for call in local_release.calls if call[:2] == ("docker", "build")]
    pushes = [call for call in local_release.calls if "push" in call and call[0] == "docker"]
    assert [call[-2:] for call in builds] == [
        ("localhost:15000/example/platform-" + name + ":" + version, ".") for name in release.IMAGES
    ]
    assert [call[-1] for call in pushes] == [
        "localhost:15000/example/platform-" + name + ":" + version for name in release.IMAGES
    ]
    assert not [call for call in local_release.calls if call[:2] == ("gh", "attestation")]


def test_local_release_names_everything_at_the_cluster_address(local_release):
    assert local_release.lock["version"] == "0.0.0-local.g" + local_release.commit[:12]
    assert local_release.lock["coreCommit"] == local_release.commit
    references = [*local_release.lock["artifacts"].values(), *local_release.lock["images"].values()]
    assert len(references) == len(VERSIONS) + len(release.IMAGES)
    for reference in references:
        assert reference.startswith("registry.example.localhost:15000/example/platform")
        assert "@sha256:" in reference


def test_local_release_reuses_images_and_isolates_push_credentials(publication, capsys):
    images = "prebuilt-*:test"
    publication.registry.host = "localhost:15000"
    publication.registry.username = "push"
    publication.registry.password = "secret"
    commands = partial(_command, publication)
    configs = []

    def command(*args):
        if args[:2] == ("git", "rev-parse"):
            return publication.commit
        if args[0] == "docker" and "push" in args:
            config = Path(args[args.index("--config") + 1]) / "config.json"
            configs.append(json.loads(config.read_text()))
            assert config.stat().st_mode & 0o777 == 0o600
            assert args[args.index("--host") + 1] == "unix:///test-docker.sock"
        if args[:3] == ("docker", "context", "inspect"):
            return "unix:///test-docker.sock"
        return commands(*args)

    with patch.object(release, "run", side_effect=command):
        release.local_release(publication.registry, "registry.example.localhost:15000", images)
    lock = json.loads(capsys.readouterr().out)
    assert len(configs) == len(release.IMAGES)
    assert all(config == {"auths": {"localhost:15000": {"auth": "cHVzaDpzZWNyZXQ="}}} for config in configs)
    assert not [call for call in publication.calls if call[:2] == ("docker", "build")]
    tags = [call for call in publication.calls if call[:2] == ("docker", "tag")]
    assert [call[2] for call in tags] == [images.replace("*", name) for name in release.IMAGES]
    assert set(lock["images"]) == set(release.IMAGES)
    assert all("@sha256:" in ref for ref in lock["images"].values())

def test_prerelease_bundle_is_rejected():
    with pytest.raises(ValueError, match="stable X.Y.Z"):
        release.publish_bundle("0.7.0-rc.1", "b" * 40, Mock())


def test_control_plane_installs_embedded_operator_crds(bundle):
    spec = helm_release(bundle, "control-plane", "loco-control-plane")["spec"]
    assert spec["install"]["crds"] == "Create"
    assert spec["upgrade"]["crds"] == "CreateReplace"


def test_crd_readiness_expression_survives_artifact_revision_changes(bundle):
    changed = {name: reference.replace(bundle.digest, "sha256:" + "c" * 64) for name, reference in bundle.refs.items()}
    updated = release.bundle("0.8.0", "d" * 40, changed, bundle.images)
    old = json.loads(bundle.files["all/crds.yaml"])
    new = json.loads(updated["all/crds.yaml"])
    assert old["spec"]["healthCheckExprs"] == new["spec"]["healthCheckExprs"]
def test_local_source_uses_selected_checkout(monkeypatch, tmp_path):
    source = tmp_path / "baseline"
    source.mkdir()
    monkeypatch.setattr(release, "ROOT", release.ROOT)
    monkeypatch.setattr("sys.argv", ["release.py", "local", "--source", str(source),
        "--repository", "example/platform", "--push", "localhost:15000", "--address", "registry.localhost",
        "--username", "fixture", "--password", "fixture"])
    with patch.object(release, "local_release") as publish:
        release.main()
    assert release.ROOT == source
    publish.assert_called_once()
