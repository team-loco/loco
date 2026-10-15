from pathlib import Path
import subprocess
import tempfile
import tomllib
import unittest

ROOT = Path(__file__).resolve().parents[2]


class VersionPinTests(unittest.TestCase):
    def check(self, tag):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'docs').mkdir()
            (root / 'mise.toml').write_text((ROOT / 'mise.toml').read_text())
            (root / 'docs/pyproject.toml').write_text((ROOT / 'docs/pyproject.toml').read_text())
            (root / 'Dockerfile').write_text(f'FROM --platform=$BUILDPLATFORM golang:{tag}@sha256:' + 'a' * 64 + ' AS builder\n')
            subprocess.run(['git', 'init', '-q', str(root)], check=True)
            subprocess.run(['git', '-C', str(root), 'add', 'Dockerfile', 'mise.toml', 'docs/pyproject.toml'], check=True)
            return subprocess.run(['bash', str(ROOT / 'mise-tasks/lint/versions')], cwd=root, text=True, capture_output=True)

    def test_build_platform_stage_does_not_hide_version_drift(self):
        result = self.check('0.0.0')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('mise.toml pins', result.stdout)

    def test_build_platform_stage_accepts_pinned_version(self):
        pinned = tomllib.loads((ROOT / 'mise.toml').read_text())['tools']['go']
        result = self.check(pinned)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == '__main__':
    unittest.main()
