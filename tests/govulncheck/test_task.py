import json
import os
import shutil
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
TASK = ROOT / 'mise-tasks/lint/govulncheck'


class GovulncheckTask(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name).resolve()
        subprocess.run(['git', 'init', '-q', str(self.root)], check=True)
        self.modules = ['.', 'api', 'agent', 'controller', 'builder', 'k8sapi',
                        'observability-proxy', 'e2e/fixtures/app']
        for module in self.modules:
            path = self.root / module / 'go.mod'
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text('module example.test/app\n')
        subprocess.run(['git', 'add', '--', '*.mod'], cwd=self.root, check=True)
        untracked = self.root / 'untracked/go.mod'
        untracked.parent.mkdir()
        untracked.write_text('module example.test/untracked\n')
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        scanner = self.bin / 'govulncheck'
        scanner.write_text('''#!/usr/bin/env bash
set -euo pipefail
printf '%s|%s\\n' "${PWD#"$SCAN_ROOT"}" "$*" >> "$SCAN_LOG"
if [[ ${PWD##*/} == "${FAIL_MODULE:-}" ]]; then
    exit 3
fi
''')
        scanner.chmod(0o755)
        self.log = self.root / 'scan.log'

    def scan(self, **environment):
        return subprocess.run(['bash', str(TASK)], cwd=self.root, capture_output=True,
                              text=True, env={**os.environ, 'PATH': f'{self.bin}:{os.environ["PATH"]}',
                                              'SCAN_ROOT': str(self.root), 'SCAN_LOG': str(self.log),
                                              **environment})

    def assert_scanned_modules(self):
        calls = self.log.read_text().splitlines()
        self.assertEqual(len(calls), len(self.modules))
        expected = {'' if module == '.' else '/' + module for module in self.modules}
        self.assertEqual({call.split('|')[0] for call in calls}, expected)
        self.assertTrue(all(call.endswith('|-mode=source -scan=symbol -format=text ./...')
                            for call in calls))

    def test_scans_every_tracked_module_at_symbol_level(self):
        result = self.scan()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_scanned_modules()

    def test_reachable_finding_fails_after_remaining_modules_are_scanned(self):
        result = self.scan(FAIL_MODULE='api')
        self.assertNotEqual(result.returncode, 0)
        self.assert_scanned_modules()


class GovulncheckReachability(unittest.TestCase):
    def test_only_reachable_symbol_fails_the_real_scanner_task(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            database = root / 'database'
            (database / 'index').mkdir(parents=True)
            (database / 'ID').mkdir()
            modified = '2026-01-01T00:00:00Z'
            identifier = 'GO-2099-0001'
            (database / 'index/db.json').write_text(json.dumps({'modified': modified}))
            (database / 'index/modules.json').write_text(json.dumps([
                {'path': 'stdlib', 'vulns': [{'id': identifier, 'modified': modified}]}]))
            (database / f'ID/{identifier}.json').write_text(json.dumps({
                'schema_version': '1.3.1', 'id': identifier, 'modified': modified,
                'published': modified, 'summary': 'Synthetic scanner test finding',
                'database_specific': {'url': 'https://example.invalid/scanner-test'},
                'affected': [{'package': {'name': 'stdlib', 'ecosystem': 'Go'},
                              'ranges': [{'type': 'SEMVER', 'events': [{'introduced': '0'}]}],
                              'ecosystem_specific': {'imports': [
                                  {'path': 'fmt', 'symbols': ['Println']}]} }]}))
            application = root / 'application'
            application.mkdir()
            subprocess.run(['git', 'init', '-q', str(application)], check=True)
            go_version = next(line for line in (ROOT / 'go.mod').read_text().splitlines()
                              if line.startswith('go '))
            (application / 'go.mod').write_text(f'module example.test/scanner\n\n{go_version}\n')
            subprocess.run(['git', 'add', 'go.mod'], cwd=application, check=True)
            scanner = shutil.which('govulncheck')
            self.assertIsNotNone(scanner)
            binaries = root / 'bin'
            binaries.mkdir()
            wrapper = binaries / 'govulncheck'
            wrapper.write_text('#!/usr/bin/env bash\nexec "$REAL_GOVULNCHECK" -db="$SCAN_DATABASE" "$@"\n')
            wrapper.chmod(0o755)
            environment = {**os.environ, 'PATH': f'{binaries}:{os.environ["PATH"]}',
                           'REAL_GOVULNCHECK': scanner, 'SCAN_DATABASE': database.as_uri()}
            source = application / 'main.go'
            source.write_text('package main\nimport "fmt"\nfunc main() { fmt.Println("test") }\n')
            reachable = subprocess.run(['bash', str(TASK)], cwd=application,
                                       env=environment, capture_output=True, text=True)
            self.assertNotEqual(reachable.returncode, 0, reachable.stdout + reachable.stderr)
            self.assertIn(identifier, reachable.stdout)
            self.assertIn('fmt.Println', reachable.stdout, reachable.stderr)
            source.write_text('package main\nimport "fmt"\nfunc main() { _ = fmt.Sprintf("test") }\n')
            unreachable = subprocess.run(['bash', str(TASK)], cwd=application,
                                         env=environment, capture_output=True, text=True)
            self.assertEqual(unreachable.returncode, 0, unreachable.stdout + unreachable.stderr)


if __name__ == '__main__':
    unittest.main()
