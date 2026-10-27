import os
import shutil
import subprocess
import tempfile
from contextlib import ExitStack
from pathlib import Path
from types import SimpleNamespace

import pytest

ROOT = Path(__file__).resolve().parents[2]
STACK = "dev/stack.sh"
API_ENV = "dev/api.env"
STACK_NAMES = (
    "LOCO_STACK_SLOT",
    "LOCO_STACK_CLAIM",
    "KIND_CLUSTER",
    "COMPOSE_PROJECT_NAME",
    "COMPOSE_FILE",
    "REGISTRY_ALIAS",
    "S3_ALIAS",
    "POSTGRES_PORT",
    "VALKEY_PORT",
    "REGISTRY_PORT",
    "S3_PORT",
    "DEX_PORT",
    "TILT_PORT",
    "WEB_PORT",
    "OBS_OTLP_GRPC_PORT",
    "OBS_OTLP_HTTP_PORT",
    "APP_PORT",
    "PUBLIC_API_URL",
    "WEB_URL",
    "DATABASE_URL",
    "CACHE_ADDR",
    "CORS_ALLOWED_ORIGINS",
    "AUTH_ISSUERS",
    "LOCO_REGISTRY_HOST",
    "LOCO_REGISTRY_URL",
    "LOCO_SOURCE_BUCKET_ENDPOINT",
)
MAX_SLOT = 9


def git(cwd, *args):
    subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True)


@pytest.fixture
def local_stack():
    with ExitStack() as cleanup:
        local_stack = SimpleNamespace()
        local_stack.directory = Path(tempfile.mkdtemp())
        cleanup.callback(shutil.rmtree, local_stack.directory)
        local_stack.main = local_stack.directory / "main"
        (local_stack.main / "dev").mkdir(parents=True)
        for path in (STACK, API_ENV):
            shutil.copyfile(ROOT / path, local_stack.main / path)
        git(local_stack.main, "init", "--quiet", "--initial-branch=main")
        git(local_stack.main, "add", ".")
        git(
            local_stack.main,
            "-c",
            "user.name=test",
            "-c",
            "user.email=test@example.test",
            "commit",
            "--quiet",
            "-m",
            "init",
        )
        yield local_stack


def _worktree(local_stack, name):
    path = local_stack.directory / name
    git(local_stack.main, "worktree", "add", "--quiet", "--detach", str(path))
    return path


def _stack(local_stack, checkout, **env):
    script = f"""source {STACK} && for name in {" ".join(STACK_NAMES)}; do printf "%s=%s\\n" "$name" "${{!name}}"; done"""
    clean = {key: value for key, value in os.environ.items() if key not in STACK_NAMES}
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=checkout,
        env={**clean, **env},
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise AssertionError(result.stderr)
    return dict((line.split("=", 1) for line in result.stdout.splitlines()))


def _api_env(local_stack):
    values = {}
    for line in (local_stack.main / API_ENV).read_text().splitlines():
        if "=" in line and (not line.startswith("#")):
            key, value = line.split("=", 1)
            values[key] = value.strip("'")
    return values


def test_main_checkout_keeps_the_api_env_values(local_stack):
    stack = _stack(local_stack, local_stack.main)
    api_env = _api_env(local_stack)
    assert stack["LOCO_STACK_SLOT"] == "0"
    assert stack["LOCO_STACK_CLAIM"] == ""
    assert stack["KIND_CLUSTER"] == "loco-cluster-local"
    assert stack["COMPOSE_PROJECT_NAME"] == "loco-dev"
    assert stack["TILT_PORT"] == "10350"
    assert stack["WEB_PORT"] == "5173"
    for name in (
        "APP_PORT",
        "WEB_URL",
        "DATABASE_URL",
        "CACHE_ADDR",
        "CORS_ALLOWED_ORIGINS",
        "AUTH_ISSUERS",
        "LOCO_REGISTRY_HOST",
        "LOCO_REGISTRY_URL",
        "LOCO_SOURCE_BUCKET_ENDPOINT",
    ):
        assert stack[name] == api_env[name], name


@pytest.fixture
def stacks(local_stack):
    stacks = SimpleNamespace()
    stacks.first = _stack(local_stack, _worktree(local_stack, "first"))
    stacks.second = _stack(local_stack, _worktree(local_stack, "second"))
    stacks.main = _stack(local_stack, local_stack.main)
    return stacks


def test_worktrees_take_slots_in_creation_order(stacks):
    assert stacks.first["LOCO_STACK_SLOT"] == "1"
    assert stacks.second["LOCO_STACK_SLOT"] == "2"


def test_a_worktree_offsets_its_stack_by_its_slot(stacks):
    second = stacks.second
    assert second["KIND_CLUSTER"] == "loco-cluster-local-2"
    assert second["COMPOSE_PROJECT_NAME"] == "loco-dev-2"
    assert second["APP_PORT"] == ":8200"
    assert second["PUBLIC_API_URL"] == "http://localhost:8200"
    assert second["WEB_URL"] == "http://localhost:5373"
    assert second["CORS_ALLOWED_ORIGINS"] == "http://localhost:5373"
    assert (
        second["DATABASE_URL"]
        == "postgres://loco_user:loco_password@localhost:5632/loco?sslmode=disable"
    )
    assert second["CACHE_ADDR"] == "valkey://127.0.0.1:6579"
    assert '"issuer":"http://localhost:5756/dex"' in second["AUTH_ISSUERS"]
    assert second["LOCO_REGISTRY_HOST"] == "loco-registry-2:5000"
    assert second["LOCO_REGISTRY_URL"] == "http://localhost:5201"
    assert second["LOCO_SOURCE_BUCKET_ENDPOINT"] == "http://loco-s3-2.localhost:9200"


def test_stacks_share_no_port_or_name(stacks):
    every = (stacks.main, stacks.first, stacks.second)
    ports = (
        "POSTGRES_PORT",
        "VALKEY_PORT",
        "REGISTRY_PORT",
        "S3_PORT",
        "DEX_PORT",
        "TILT_PORT",
        "WEB_PORT",
        "OBS_OTLP_GRPC_PORT",
        "OBS_OTLP_HTTP_PORT",
    )
    used = [stack[name] for stack in every for name in ports]
    used += [stack["APP_PORT"] for stack in every]
    assert len(used) == len({port.lstrip(":") for port in used})
    for name in ("KIND_CLUSTER", "COMPOSE_PROJECT_NAME", "REGISTRY_ALIAS", "S3_ALIAS"):
        assert len({stack[name] for stack in every}) == 3, name


def test_compose_reads_the_checkouts_own_files(local_stack):
    for checkout in (local_stack.main, _worktree(local_stack, "compose")):
        stack = _stack(local_stack, checkout)
        root = checkout.resolve()
        assert stack["COMPOSE_FILE"] == str(root / "dev" / "compose.yaml")


def test_a_worktree_keeps_its_slot(local_stack):
    checkout = _worktree(local_stack, "kept")
    _worktree(local_stack, "other")
    assert _stack(local_stack, checkout)["LOCO_STACK_SLOT"] == "1"
    assert _stack(local_stack, checkout)["LOCO_STACK_SLOT"] == "1"


def test_a_removed_worktree_frees_its_slot(local_stack):
    removed = _worktree(local_stack, "removed")
    assert _stack(local_stack, removed)["LOCO_STACK_SLOT"] == "1"
    git(local_stack.main, "worktree", "remove", "--force", str(removed))
    assert _stack(local_stack, _worktree(local_stack, "next"))["LOCO_STACK_SLOT"] == "1"


def test_a_worktree_fails_when_every_slot_is_claimed(local_stack):
    for slot in range(MAX_SLOT):
        _stack(local_stack, _worktree(local_stack, f"claimed-{slot}"))
    with pytest.raises(AssertionError, match="every local stack slot"):
        _stack(local_stack, _worktree(local_stack, "unclaimed"))


def test_a_slot_can_be_chosen(local_stack):
    assert (
        _stack(local_stack, local_stack.main, LOCO_STACK_SLOT="4")["KIND_CLUSTER"]
        == "loco-cluster-local-4"
    )
    with pytest.raises(AssertionError, match="LOCO_STACK_SLOT must be a slot"):
        _stack(local_stack, local_stack.main, LOCO_STACK_SLOT="10")

