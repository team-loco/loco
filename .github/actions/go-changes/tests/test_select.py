import importlib.util
import json
import os
import sys
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / 'select_go.py'
spec = importlib.util.spec_from_file_location('selection', SCRIPT)
selection = importlib.util.module_from_spec(spec)
spec.loader.exec_module(selection)
ROOT = SCRIPT.parents[3]


class GoSelection(unittest.TestCase):
    def setUp(self):
        self.dependencies = selection.module_dependencies(ROOT)

    def selected(self, *paths):
        return selection.select_modules(paths, self.dependencies)

    def test_stacked_proxy_test_and_docs_select_only_proxy(self):
        self.assertEqual(self.selected('observability-proxy/pkg/auth/validator_test.go',
                                       'docs/content/operations/self-hosting.md'), {'observability-proxy'})

    def test_api_changes_do_not_start_other_modules(self):
        self.assertEqual(self.selected('api/service/token.go'), {'api'})

    def test_root_libraries_select_consumers(self):
        expected = {'.', 'api', 'agent', 'observability-proxy'}
        for path in ('internal/buildinfo/version.go', 'gen/go/loco/token/v1/token.pb.go', 'go.mod'):
            with self.subTest(path=path):
                self.assertEqual(self.selected(path), expected)

    def test_cli_entrypoint_does_not_select_library_consumers(self):
        self.assertEqual(self.selected('cmd/loco/deploy.go', 'main.go'), {'.'})

    def test_kubernetes_types_select_every_importing_module(self):
        self.assertEqual(self.selected('k8sapi/v1/application_types.go'), {'k8sapi', 'api', 'agent', 'controller'})
        self.assertEqual(self.selected('k8sapi/v1/application_test.go'), {'k8sapi'})

    def test_proxy_external_test_inputs(self):
        self.assertEqual(self.selected('charts/loco-obs/values.yaml'), {'observability-proxy'})
        self.assertEqual(self.selected('controller/Makefile'), {'controller', 'observability-proxy'})

    def test_toolchain_and_selector_changes_select_every_module(self):
        for path in ('mise.toml', '.github/actions/go-changes/select.py', '.github/workflows/test.yml'):
            with self.subTest(path=path):
                self.assertEqual(self.selected(path), set(self.dependencies))

    def test_documentation_does_not_select_go_jobs(self):
        self.assertEqual(self.selected('docs/content/operations/self-hosting.md'), set())

    def test_pull_request_diff_excludes_changes_only_on_base(self):
        with tempfile.TemporaryDirectory() as directory:
            def git(*args):
                return subprocess.check_output(['git', '-C', directory, *args], text=True).strip()
            git('init', '-q')
            git('config', 'user.name', 'CI fixture')
            git('config', 'user.email', 'ci@example.test')
            (Path(directory) / 'root').write_text('base')
            git('add', 'root')
            git('commit', '-qm', 'base')
            ancestor = git('rev-parse', 'HEAD')
            (Path(directory) / 'base-only').write_text('base update')
            git('add', 'base-only')
            git('commit', '-qm', 'base update')
            base = git('rev-parse', 'HEAD')
            git('checkout', '-qb', 'stacked-pr', ancestor)
            (Path(directory) / 'proxy-test').write_text('test change')
            git('add', 'proxy-test')
            git('commit', '-qm', 'PR change')
            head = git('rev-parse', 'HEAD')
            previous = Path.cwd()
            try:
                os.chdir(directory)
                self.assertEqual(selection.changed_files('pull_request', base, head), ['proxy-test'])
                self.assertEqual(set(selection.changed_files('push', base, head)), {'base-only', 'proxy-test'})
            finally:
                os.chdir(previous)

    def test_selector_command_outputs_only_proxy_jobs_for_pr_658_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            def git(*args):
                return subprocess.check_output(['git', '-C', directory, *args], text=True).strip()
            git('init', '-q')
            git('config', 'user.name', 'CI fixture')
            git('config', 'user.email', 'ci@example.test')
            (root / 'go.mod').write_text('module fixture\n')
            (root / 'observability-proxy').mkdir()
            (root / 'observability-proxy/go.mod').write_text('module fixture/proxy\nreplace fixture => ../\n')
            git('add', 'go.mod', 'observability-proxy/go.mod')
            git('commit', '-qm', 'base')
            base = git('rev-parse', 'HEAD')
            (root / 'observability-proxy/validator_test.go').write_text('test')
            (root / 'docs').mkdir()
            (root / 'docs/self-hosting.md').write_text('documentation')
            git('add', 'observability-proxy/validator_test.go', 'docs/self-hosting.md')
            git('commit', '-qm', 'proxy test and docs')
            output = root / 'outputs'
            matrix = [{'component': 'CLI', 'working-directory': '.'},
                      {'component': 'Proxy', 'working-directory': 'observability-proxy'}]
            env = {**os.environ, 'EVENT': 'pull_request', 'BASE': base, 'HEAD': git('rev-parse', 'HEAD'),
                   'GITHUB_OUTPUT': str(output), 'GO_MATRIX': json.dumps(matrix)}
            subprocess.check_call([sys.executable, str(SCRIPT)], cwd=root, env=env, stdout=subprocess.DEVNULL)
            values = dict(line.split('=', 1) for line in output.read_text().splitlines())
            self.assertEqual(values['api'], 'false')
            self.assertEqual(values['run'], 'true')
            self.assertEqual(json.loads(values['matrix']), {'include': [matrix[1]]})
