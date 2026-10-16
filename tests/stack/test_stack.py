import ipaddress
import os
import shutil
import subprocess
import tempfile
from contextlib import ExitStack
from pathlib import Path
from types import SimpleNamespace

import pytest

ROOT = Path(__file__).resolve().parents[2]
STACK = "env/local/stack.sh"
LB_RANGE = "mise-tasks/cluster/lb-range"
STACK_NAMES = (
    "LOCO_STACK_SLOT",
    "LOCO_STACK_CLAIM",
    "KIND_CLUSTER",
    "COMPOSE_PROJECT_NAME",
    "REGISTRY_ALIAS",
    "S3_ALIAS",
    "POSTGRES_PORT",
    "VALKEY_PORT",
    "REGISTRY_PORT",
    "S3_PORT",
    "DEX_PORT",
    "TILT_PORT",
    "WEB_PORT",
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
KIND_SUBNET = "192.168.97.0/24"
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
        (local_stack.main / "env" / "local").mkdir(parents=True)
        shutil.copyfile(ROOT / STACK, local_stack.main / STACK)
        shutil.copyfile(ROOT / ".env.example", local_stack.main / ".env.example")
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
        shutil.copyfile(local_stack.main / ".env.example", local_stack.main / ".env")
        yield local_stack


def _worktree(local_stack, name):
    path = local_stack.directory / name
    git(local_stack.main, "worktree", "add", "--quiet", "--detach", str(path))
    (path / ".env").symlink_to(local_stack.main / ".env")
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


def _dotenv(local_stack):
    values = {}
    for line in (local_stack.main / ".env").read_text().splitlines():
        if "=" in line and (not line.startswith("#")):
            key, value = line.split("=", 1)
            values[key] = value.strip("'")
    return values


def test_main_checkout_keeps_the_env_file_values(local_stack):
    stack = _stack(local_stack, local_stack.main)
    dotenv = _dotenv(local_stack)
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
        assert stack[name] == dotenv[name], name


def test_worktrees_get_their_own_names_and_ports(local_stack):
    first = _stack(local_stack, _worktree(local_stack, "first"))
    second = _stack(local_stack, _worktree(local_stack, "second"))
    main = _stack(local_stack, local_stack.main)
    assert first["LOCO_STACK_SLOT"] == "1"
    assert second["LOCO_STACK_SLOT"] == "2"
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
    ports = (
        "POSTGRES_PORT",
        "VALKEY_PORT",
        "REGISTRY_PORT",
        "S3_PORT",
        "DEX_PORT",
        "TILT_PORT",
        "WEB_PORT",
    )
    used = [stack[name] for stack in (main, first, second) for name in ports]
    used += [stack["APP_PORT"] for stack in (main, first, second)]
    assert len(used) == len({port.lstrip(":") for port in used})
    for name in ("KIND_CLUSTER", "COMPOSE_PROJECT_NAME", "REGISTRY_ALIAS", "S3_ALIAS"):
        assert len({main[name], first[name], second[name]}) == 3, name


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


def test_a_checkout_without_an_env_file_fails(local_stack):
    (local_stack.main / ".env").unlink()
    with pytest.raises(AssertionError, match="\\.env is missing"):
        _stack(local_stack, local_stack.main)


def _lb_range(slot):
    with tempfile.TemporaryDirectory() as directory:
        docker = Path(directory) / "docker"
        docker.write_text(f"#!/usr/bin/env bash\necho {KIND_SUBNET}\n")
        docker.chmod(0o755)
        env = {
            **os.environ,
            "PATH": f"{directory}:{os.environ['PATH']}",
            "LOCO_STACK_SLOT": str(slot),
        }
        output = subprocess.check_output([str(ROOT / LB_RANGE)], env=env, text=True)
        first, last = (ipaddress.ip_address(address) for address in output.split())
        return first, last


def test_the_main_checkout_keeps_its_range():
    assert _lb_range(0) == (
        ipaddress.ip_address("192.168.97.200"),
        ipaddress.ip_address("192.168.97.249"),
    )


def test_slots_get_ranges_that_do_not_overlap():
    taken = set()
    for slot in range(MAX_SLOT + 1):
        first, last = _lb_range(slot)
        addresses = set(range(int(first), int(last) + 1))
        assert not addresses & taken, slot
        taken |= addresses
