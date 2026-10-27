import importlib.util
import json
import os
import struct
import subprocess
from pathlib import Path
from types import SimpleNamespace

import pytest

SPEC = importlib.util.spec_from_file_location(
    "verify", Path(__file__).resolve().parents[1] / "verify.py"
)
verify = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(verify)


def _elf(machine, interpreter=False):
    header = bytearray(128)
    header[:6] = b"\x7fELF\x02\x01"
    struct.pack_into("<H", header, 18, machine)
    struct.pack_into("<Q", header, 32, 64)
    struct.pack_into("<HH", header, 54, 56, 1)
    struct.pack_into("<I", header, 64, 3 if interpreter else 1)
    return header


def test_both_static_architectures():
    verify.validate_binary(_elf(62), "amd64")
    verify.validate_binary(_elf(183), "arm64")


def test_amd64_binary_in_arm64_image_fails():
    with pytest.raises(ValueError, match="does not match arm64"):
        verify.validate_binary(_elf(62), "arm64")


def test_dynamic_binary_fails():
    with pytest.raises(ValueError, match="statically linked"):
        verify.validate_binary(_elf(183, interpreter=True), "arm64")


def test_invalid_and_truncated_binary_fail():
    for data in (b"not an executable", _elf(183)[:80]):
        with pytest.raises(ValueError):
            verify.validate_binary(data, "arm64")


@pytest.fixture
def platform_index():
    platform_index = SimpleNamespace()
    platform_index.image = "ghcr.io/example/platform@sha256:index"
    platform_index.amd64 = {
        "digest": "sha256:amd64",
        "platform": {"os": "linux", "architecture": "amd64"},
    }
    platform_index.arm64 = {
        "digest": "sha256:arm64",
        "platform": {"os": "linux", "architecture": "arm64", "variant": "v8"},
    }
    platform_index.attestation = {
        "digest": "sha256:attestation",
        "platform": {"os": "unknown", "architecture": "unknown"},
    }
    return platform_index


def test_selects_architecture_and_ignores_attestations(platform_index):
    index = {
        "manifests": [
            platform_index.amd64,
            platform_index.arm64,
            platform_index.attestation,
        ]
    }
    assert (
        verify.resolve(platform_index.image, "linux/arm64", index)
        == "ghcr.io/example/platform@sha256:arm64"
    )
    assert (
        verify.resolve(platform_index.image, "linux/amd64", index)
        == "ghcr.io/example/platform@sha256:amd64"
    )


def test_missing_or_duplicate_platform_fails(platform_index):
    for manifests in (
        [platform_index.amd64, platform_index.attestation],
        [platform_index.arm64, platform_index.arm64],
    ):
        with pytest.raises(ValueError, match="exactly one linux/arm64"):
            verify.resolve(
                platform_index.image, "linux/arm64", {"manifests": manifests}
            )


def _workflow(name):
    root = Path(__file__).resolve().parents[3]
    return json.loads(
        subprocess.check_output(
            ["yq", "-o", "json", ".", str(root / ".github/workflows" / name)], text=True
        )
    )


def test_publishing_requires_both_native_verifiers_before_index_provenance():
    workflow = _workflow("build-push.yml")
    jobs = workflow["jobs"]
    push = next(
        (
            step
            for step in jobs["build-and-push-image"]["steps"]
            if step.get("id") == "push"
        )
    )
    platforms = set(push["with"]["platforms"].split(","))
    assert platforms == {"linux/amd64", "linux/arm64"}
    assert push["with"]["provenance"] == "mode=max"
    runners = jobs["verify-platform"]["strategy"]["matrix"]["include"]
    assert {entry["platform"] for entry in runners} == platforms
    assert len({entry["runner"] for entry in runners}) == len(runners)
    assert "verify-platform" in jobs["attest-index"]["needs"]
    sbom = next(
        (
            step
            for step in jobs["verify-platform"]["steps"]
            if step["name"] == "Generate platform SBOM"
        )
    )
    assert sbom["with"]["image"] == "${{ steps.platform.outputs.image }}"


PLATFORM_DOCKERFILES = {
    "./api/Dockerfile",
    "./agent/Dockerfile",
    "./controller/Dockerfile",
    "./builder/Dockerfile",
    "./observability-proxy/Dockerfile",
    "./web/Dockerfile",
}


def _select_images(cwd, output, **env):
    script = _workflow("docker.yml")["jobs"]["changes"]["steps"][1]["run"]
    subprocess.run(
        ["bash", "-c", script],
        cwd=cwd,
        env={"PATH": os.environ["PATH"], "GITHUB_OUTPUT": str(output), **env},
        check=True,
    )
    outputs = dict(line.split("=", 1) for line in output.read_text().splitlines())
    images = {entry["dockerfile"] for entry in json.loads(outputs["images"])}
    platforms = {entry["platform"] for entry in json.loads(outputs["targets"])}
    return images, platforms


def test_main_builds_each_platform_image_on_both_architectures(tmp_path):
    workflow = _workflow("docker.yml")
    assert "tools/platform-images/**" in workflow["on"]["pull_request"]["paths"]
    assert "tests/versions/**" in workflow["on"]["pull_request"]["paths"]
    root = Path(__file__).resolve().parents[3]
    assert _select_images(root, tmp_path / "output", EVENT="push") == (
        PLATFORM_DOCKERFILES,
        {"linux/amd64", "linux/arm64"},
    )


@pytest.mark.parametrize(
    ("path", "expected"),
    [
        ("tools/platform-images/verify.py", PLATFORM_DOCKERFILES),
        (".github/workflows/docker.yml", PLATFORM_DOCKERFILES),
        (
            "internal/buildinfo/version.go",
            {
                "./api/Dockerfile",
                "./agent/Dockerfile",
                "./observability-proxy/Dockerfile",
            },
        ),
        (
            "k8sapi/v1/application_types.go",
            {"./api/Dockerfile", "./agent/Dockerfile", "./controller/Dockerfile"},
        ),
        ("api/server.go", {"./api/Dockerfile"}),
        ("docs/content/index.md", {"./web/Dockerfile"}),
        ("schemas/loco.v1.json", {"./web/Dockerfile"}),
        ("mise.toml", set()),
    ],
)
def test_pull_requests_build_changed_images_on_amd64(tmp_path, path, expected):
    repo = tmp_path / "repo"
    repo.mkdir()

    def git(*args):
        return subprocess.check_output(
            ["git", "-C", str(repo), *args], text=True
        ).strip()

    git("init", "-q")
    git("config", "user.name", "CI fixture")
    git("config", "user.email", "ci@example.test")
    git("commit", "-q", "--allow-empty", "-m", "base")
    base = git("rev-parse", "HEAD")
    changed = repo / path
    changed.parent.mkdir(parents=True, exist_ok=True)
    changed.write_text("changed")
    git("add", path)
    git("commit", "-qm", "head")
    head = git("rev-parse", "HEAD")
    selected = _select_images(
        repo, tmp_path / "output", EVENT="pull_request", BASE=base, HEAD=head
    )
    assert selected == (expected, {"linux/amd64"})
