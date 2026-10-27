import importlib.util
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[1] / "select_go.py"
spec = importlib.util.spec_from_file_location("selection", SCRIPT)
selection = importlib.util.module_from_spec(spec)
spec.loader.exec_module(selection)
ROOT = SCRIPT.parents[3]


@pytest.fixture
def module_dependencies():
    return selection.module_dependencies(ROOT)


def _selected(module_dependencies, *paths):
    return selection.select_modules(paths, module_dependencies)


def test_stacked_proxy_test_and_docs_select_only_proxy(module_dependencies):
    assert _selected(
        module_dependencies,
        "observability-proxy/pkg/auth/validator_test.go",
        "docs/content/operations/self-hosting.md",
    ) == {"observability-proxy"}


def test_api_changes_do_not_start_other_modules(module_dependencies):
    assert _selected(module_dependencies, "api/service/token.go") == {"api"}


def test_root_libraries_select_consumers(module_dependencies, subtests):
    expected = {".", "cmd", "api", "agent", "observability-proxy"}
    for path in (
        "internal/buildinfo/version.go",
        "gen/go/loco/token/v1/token.pb.go",
        "go.mod",
    ):
        with subtests.test(path=path):
            assert _selected(module_dependencies, path) == expected


def test_cli_changes_select_only_the_cli_module(module_dependencies):
    assert _selected(
        module_dependencies,
        "cmd/loco/main.go",
        "cmd/loco/main_test.go",
        "cmd/internal/ui/select.go",
        "cmd/go.mod",
    ) == {"cmd"}


def test_kubernetes_types_select_every_importing_module(module_dependencies):
    assert _selected(module_dependencies, "k8sapi/v1/application_types.go") == {
        "k8sapi",
        "api",
        "agent",
        "controller",
    }
    assert _selected(module_dependencies, "k8sapi/v1/application_test.go") == {"k8sapi"}


def test_proxy_external_test_inputs(module_dependencies, subtests):
    for path in (
        "mise-tasks/test/counter-metrics",
        "mise-tasks/test/lib/ports.sh",
        "charts/loco-worker/values.yaml",
        "charts/loco-control-plane/values.yaml",
    ):
        with subtests.test(path=path):
            assert _selected(module_dependencies, path) == {"observability-proxy"}
    assert _selected(module_dependencies, "controller/Makefile") == {"controller"}


def test_toolchain_and_selector_changes_select_every_module(
    module_dependencies, subtests
):
    for path in (
        "mise.toml",
        ".github/actions/go-changes/select.py",
        ".github/workflows/test.yml",
    ):
        with subtests.test(path=path):
            assert _selected(module_dependencies, path) == set(module_dependencies)


def test_documentation_does_not_select_go_jobs(module_dependencies):
    assert (
        _selected(module_dependencies, "docs/content/operations/self-hosting.md")
        == set()
    )


def test_pull_request_diff_excludes_changes_only_on_base(module_dependencies):
    with tempfile.TemporaryDirectory() as directory:

        def git(*args):
            return subprocess.check_output(
                ["git", "-C", directory, *args], text=True
            ).strip()

        git("init", "-q")
        git("config", "user.name", "CI fixture")
        git("config", "user.email", "ci@example.test")
        (Path(directory) / "root").write_text("base")
        git("add", "root")
        git("commit", "-qm", "base")
        ancestor = git("rev-parse", "HEAD")
        (Path(directory) / "base-only").write_text("base update")
        git("add", "base-only")
        git("commit", "-qm", "base update")
        base = git("rev-parse", "HEAD")
        git("checkout", "-qb", "stacked-pr", ancestor)
        (Path(directory) / "proxy-test").write_text("test change")
        git("add", "proxy-test")
        git("commit", "-qm", "PR change")
        head = git("rev-parse", "HEAD")
        previous = Path.cwd()
        try:
            os.chdir(directory)
            assert selection.changed_files("pull_request", base, head) == ["proxy-test"]
            assert set(selection.changed_files("push", base, head)) == {
                "base-only",
                "proxy-test",
            }
        finally:
            os.chdir(previous)


def test_selector_command_outputs_only_proxy_jobs_for_pr_658_files(module_dependencies):
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)

        def git(*args):
            return subprocess.check_output(
                ["git", "-C", directory, *args], text=True
            ).strip()

        git("init", "-q")
        git("config", "user.name", "CI fixture")
        git("config", "user.email", "ci@example.test")
        (root / "go.mod").write_text("module fixture\n")
        (root / "observability-proxy").mkdir()
        (root / "observability-proxy/go.mod").write_text(
            "module fixture/proxy\nreplace fixture => ../\n"
        )
        git("add", "go.mod", "observability-proxy/go.mod")
        git("commit", "-qm", "base")
        base = git("rev-parse", "HEAD")
        (root / "observability-proxy/validator_test.go").write_text("test")
        (root / "docs").mkdir()
        (root / "docs/self-hosting.md").write_text("documentation")
        git("add", "observability-proxy/validator_test.go", "docs/self-hosting.md")
        git("commit", "-qm", "proxy test and docs")
        output = root / "outputs"
        matrix = [
            {"component": "CLI", "working-directory": "."},
            {"component": "Proxy", "working-directory": "observability-proxy"},
        ]
        env = {
            **os.environ,
            "EVENT": "pull_request",
            "BASE": base,
            "HEAD": git("rev-parse", "HEAD"),
            "GITHUB_OUTPUT": str(output),
            "GO_MATRIX": json.dumps(matrix),
        }
        subprocess.check_call(
            [sys.executable, str(SCRIPT)], cwd=root, env=env, stdout=subprocess.DEVNULL
        )
        values = dict((line.split("=", 1) for line in output.read_text().splitlines()))
        assert values["api"] == "false"
        assert values["run"] == "true"
        assert json.loads(values["matrix"]) == {"include": [matrix[1]]}
