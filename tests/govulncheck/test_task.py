import json
import os
import shutil
import subprocess
import tempfile
from contextlib import ExitStack
from pathlib import Path
from types import SimpleNamespace

import pytest

ROOT = Path(__file__).resolve().parents[2]
TASK = ROOT / "mise-tasks/lint/govulncheck"


@pytest.fixture
def govulncheck_task():
    with ExitStack() as cleanup:
        govulncheck_task = SimpleNamespace()
        govulncheck_task.directory = tempfile.TemporaryDirectory()
        cleanup.callback(govulncheck_task.directory.cleanup)
        govulncheck_task.root = Path(govulncheck_task.directory.name).resolve()
        subprocess.run(["git", "init", "-q", str(govulncheck_task.root)], check=True)
        govulncheck_task.modules = [
            ".",
            "api",
            "agent",
            "controller",
            "builder",
            "k8sapi",
            "observability-proxy",
            "tests/e2e/fixtures/app",
        ]
        for module in govulncheck_task.modules:
            path = govulncheck_task.root / module / "go.mod"
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("module example.test/app\n")
        subprocess.run(
            ["git", "add", "--", "*.mod"], cwd=govulncheck_task.root, check=True
        )
        untracked = govulncheck_task.root / "untracked/go.mod"
        untracked.parent.mkdir()
        untracked.write_text("module example.test/untracked\n")
        govulncheck_task.bin = govulncheck_task.root / "bin"
        govulncheck_task.bin.mkdir()
        scanner = govulncheck_task.bin / "govulncheck"
        scanner.write_text(
            '#!/usr/bin/env bash\nset -euo pipefail\nprintf \'%s|%s\\n\' "${PWD#"$SCAN_ROOT"}" "$*" >> "$SCAN_LOG"\nif [[ ${PWD##*/} == "${FAIL_MODULE:-}" ]]; then\n    exit 3\nfi\n'
        )
        scanner.chmod(0o755)
        govulncheck_task.log = govulncheck_task.root / "scan.log"
        yield govulncheck_task


def _scan(govulncheck_task, **environment):
    return subprocess.run(
        ["bash", str(TASK)],
        cwd=govulncheck_task.root,
        capture_output=True,
        text=True,
        env={
            **os.environ,
            "PATH": f"{govulncheck_task.bin}:{os.environ['PATH']}",
            "SCAN_ROOT": str(govulncheck_task.root),
            "SCAN_LOG": str(govulncheck_task.log),
            **environment,
        },
    )


def _assert_scanned_modules(govulncheck_task):
    calls = govulncheck_task.log.read_text().splitlines()
    assert len(calls) == len(govulncheck_task.modules)
    expected = {
        "" if module == "." else "/" + module for module in govulncheck_task.modules
    }
    assert {call.split("|")[0] for call in calls} == expected
    for call in calls:
        arguments = call.split("|")[1].split()
        assert "-scan=symbol" in arguments, call
        assert arguments[-1] == "./...", call


def test_scans_every_tracked_module_at_symbol_level(govulncheck_task):
    result = _scan(govulncheck_task)
    assert result.returncode == 0, result.stderr
    _assert_scanned_modules(govulncheck_task)


def test_reachable_finding_fails_after_remaining_modules_are_scanned(govulncheck_task):
    result = _scan(govulncheck_task, FAIL_MODULE="api")
    assert result.returncode != 0
    _assert_scanned_modules(govulncheck_task)


def test_only_reachable_symbol_fails_the_real_scanner_task():
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory).resolve()
        database = root / "database"
        (database / "index").mkdir(parents=True)
        (database / "ID").mkdir()
        modified = "2026-01-01T00:00:00Z"
        identifier = "GO-2099-0001"
        (database / "index/db.json").write_text(json.dumps({"modified": modified}))
        (database / "index/modules.json").write_text(
            json.dumps(
                [
                    {
                        "path": "stdlib",
                        "vulns": [{"id": identifier, "modified": modified}],
                    }
                ]
            )
        )
        (database / f"ID/{identifier}.json").write_text(
            json.dumps(
                {
                    "schema_version": "1.3.1",
                    "id": identifier,
                    "modified": modified,
                    "published": modified,
                    "summary": "Synthetic scanner test finding",
                    "database_specific": {
                        "url": "https://example.invalid/scanner-test"
                    },
                    "affected": [
                        {
                            "package": {"name": "stdlib", "ecosystem": "Go"},
                            "ranges": [
                                {"type": "SEMVER", "events": [{"introduced": "0"}]}
                            ],
                            "ecosystem_specific": {
                                "imports": [{"path": "fmt", "symbols": ["Println"]}]
                            },
                        }
                    ],
                }
            )
        )
        application = root / "application"
        application.mkdir()
        subprocess.run(["git", "init", "-q", str(application)], check=True)
        go_version = next(
            (
                line
                for line in (ROOT / "go.mod").read_text().splitlines()
                if line.startswith("go ")
            )
        )
        (application / "go.mod").write_text(
            f"module example.test/scanner\n\n{go_version}\n"
        )
        subprocess.run(["git", "add", "go.mod"], cwd=application, check=True)
        scanner = shutil.which("govulncheck")
        assert scanner is not None
        binaries = root / "bin"
        binaries.mkdir()
        wrapper = binaries / "govulncheck"
        wrapper.write_text(
            '#!/usr/bin/env bash\nexec "$REAL_GOVULNCHECK" -db="$SCAN_DATABASE" "$@"\n'
        )
        wrapper.chmod(0o755)
        environment = {
            **os.environ,
            "PATH": f"{binaries}:{os.environ['PATH']}",
            "REAL_GOVULNCHECK": scanner,
            "SCAN_DATABASE": database.as_uri(),
        }
        source = application / "main.go"
        source.write_text(
            'package main\nimport "fmt"\nfunc main() { fmt.Println("test") }\n'
        )
        reachable = subprocess.run(
            ["bash", str(TASK)],
            cwd=application,
            env=environment,
            capture_output=True,
            text=True,
        )
        assert reachable.returncode != 0, reachable.stdout + reachable.stderr
        assert identifier in reachable.stdout
        assert "fmt.Println" in reachable.stdout, reachable.stderr
        source.write_text(
            'package main\nimport "fmt"\nfunc main() { _ = fmt.Sprintf("test") }\n'
        )
        unreachable = subprocess.run(
            ["bash", str(TASK)],
            cwd=application,
            env=environment,
            capture_output=True,
            text=True,
        )
        assert unreachable.returncode == 0, unreachable.stdout + unreachable.stderr
