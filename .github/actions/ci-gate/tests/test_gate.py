import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[4]


class PullRequestGate(unittest.TestCase):
    def test_gate_runs_for_pull_request_head(self):
        workflow = ROOT / '.github/workflows/ci-gate.yml'
        event = subprocess.check_output(['yq', '-r', '.on | keys | .[]', str(workflow)], text=True)
        self.assertEqual(event.strip(), 'pull_request')
        condition = subprocess.check_output(['yq', '-r', '.jobs.gate.if', str(workflow)], text=True)
        self.assertEqual(condition.strip(), 'null')
        sha = subprocess.check_output(['yq', '-r', '.jobs.gate.steps[0].env.SHA', str(workflow)], text=True)
        self.assertEqual(sha.strip(), '${{ github.event.pull_request.head.sha }}')

    def run_gate(self, rows):
        workflow = ROOT / '.github/workflows/ci-gate.yml'
        script = subprocess.check_output(['yq', '-r', '.jobs.gate.steps[0].run', str(workflow)], text=True)
        script = script.replace('settle_seconds=30', 'settle_seconds=0').replace('settle_seconds_empty=120', 'settle_seconds_empty=0')
        with tempfile.TemporaryDirectory() as directory:
            command = Path(directory) / 'gh'
            command.write_text('#!/bin/sh\nprintf "%s" "$CHECK_ROWS"\n')
            command.chmod(0o755)
            env = {**os.environ, 'PATH': directory + os.pathsep + os.environ['PATH'],
                   'CHECK_ROWS': rows, 'SELF': 'CI Gate', 'REPO': 'fixture/repo', 'SHA': 'fixture'}
            return subprocess.run(['bash', '-c', script], env=env, capture_output=True, text=True)

    def test_successful_and_skipped_checks_pass(self):
        result = self.run_gate('Proxy Tests\t1\tcompleted\tsuccess\nAPI Tests\t2\tcompleted\tskipped\n')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_failed_check_blocks_merging(self):
        result = self.run_gate('Proxy Tests\t1\tcompleted\tfailure\n')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('failing checks', result.stdout)

    def test_latest_success_supersedes_an_old_failure(self):
        result = self.run_gate('Proxy Tests\t1\tcompleted\tfailure\nProxy Tests\t2\tcompleted\tsuccess\n')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
