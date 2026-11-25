"""Black-box checks for the terminal evidence command; only the helper needs uv packages."""

import struct
import subprocess
import sys
import tempfile
import time
import unittest
import zlib
from pathlib import Path

CAPTURE = Path(__file__).with_name("capture.py")


def png_pixels(path):
    png = path.read_bytes()
    assert png[:8] == b"\x89PNG\r\n\x1a\n"
    chunks = []
    offset = 8
    while offset < len(png):
        size, kind = struct.unpack_from(">I4s", png, offset)
        if kind == b"IDAT":
            chunks.append(png[offset + 8 : offset + 8 + size])
        offset += size + 12
    return zlib.decompress(b"".join(chunks))


class CaptureTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.out = Path(directory.name)

    def capture(self, command, *flags):
        return subprocess.run(
            [
                "uv",
                "run",
                "--script",
                str(CAPTURE),
                "--out",
                str(self.out),
                "--name",
                "sample",
                "--cols",
                "30",
                "--rows",
                "5",
                *flags,
                "--",
                sys.executable,
                "-c",
                command,
            ],
            capture_output=True,
            text=True,
            timeout=30,
        )

    def test_captures_both_themes_and_real_terminal_output(self):
        # The child interacts with a PTY and will fail if the theme query is not answered.
        result = self.capture(
            "import os, sys, tty; assert sys.stdout.isatty(); tty.setraw(0); "
            "os.write(1, b'\\x1b]11;?\\x07\\x1b[c'); response = b''\n"
            "while not response.endswith(b'\\x1b[?1;2c'): response += os.read(0, 64)\n"
            "expected = b'rgb:1414/1414/1414' if os.environ['COLORFGBG'] == '15;0' else b'rgb:ffff/ffff/ffff'; "
            "assert expected in response; "
            "print('Ready', end='', flush=True)"
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        dark = png_pixels(self.out / "sample-dark.png")
        light = png_pixels(self.out / "sample-light.png")
        self.assertEqual(dark[1:4], bytes([20, 20, 20]))
        self.assertEqual(light[1:4], bytes([255, 255, 255]))
        self.assertNotEqual(dark, light)

    def test_captures_running_screen_and_stops_child(self):
        result = self.capture(
            "import time; print('Waiting for approval', flush=True); time.sleep(60)",
            "--theme",
            "dark",
            "--until",
            "Waiting for approval",
            "--timeout",
            "3",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.out / "sample-dark.png").exists())
        self.assertFalse((self.out / "sample-light.png").exists())

    def test_missing_screen_fails_without_screenshot(self):
        result = self.capture(
            "print('different screen')", "--until", "Waiting", "--theme", "dark"
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("command exited before showing", result.stderr)
        self.assertFalse((self.out / "sample-dark.png").exists())

    def test_expected_failure_can_be_captured(self):
        result = self.capture(
            "print('error'); raise SystemExit(3)",
            "--expect-exit",
            "3",
            "--theme",
            "light",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.out / "sample-light.png").exists())

    def test_unexpected_exit_fails(self):
        result = self.capture("raise SystemExit(3)", "--theme", "light")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("command exited 3, expected 0", result.stderr)
        self.assertFalse((self.out / "sample-light.png").exists())

    def test_failed_retry_removes_stale_screenshot(self):
        first = self.capture("print('old')", "--theme", "dark")
        self.assertEqual(first.returncode, 0, first.stderr)
        screenshot = self.out / "sample-dark.png"
        self.assertTrue(screenshot.exists())
        second = self.capture("raise SystemExit(1)", "--theme", "dark")
        self.assertNotEqual(second.returncode, 0)
        self.assertFalse(screenshot.exists())

    def test_temporary_directory_removed_after_capture(self):
        marker = self.out / "temp-dir.txt"
        command = (
            "import os, pathlib, time; "
            f"pathlib.Path({str(marker)!r}).write_text(os.environ['TMPDIR']); "
            "print('Ready', flush=True); time.sleep(60)"
        )
        result = self.capture(command, "--theme", "dark", "--until", "Ready")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(Path(marker.read_text()).exists())

    def test_capture_stops_descendants_that_ignore_term(self):
        marker = self.out / "worker-leaked"
        command = (
            "import os, pathlib, signal, time\n"
            "r, w = os.pipe()\n"
            "if os.fork() == 0:\n"
            "    signal.signal(signal.SIGTERM, signal.SIG_IGN)\n"
            "    signal.signal(signal.SIGHUP, signal.SIG_IGN)\n"
            "    os.write(w, b'1')\n"
            "    time.sleep(0.5)\n"
            f"    pathlib.Path({str(marker)!r}).touch()\n"
            "    os._exit(0)\n"
            "os.read(r, 1)\n"
            "print('Ready', flush=True)\n"
            "time.sleep(60)"
        )
        result = self.capture(command, "--theme", "dark", "--until", "Ready")
        self.assertEqual(result.returncode, 0, result.stderr)
        time.sleep(0.6)
        self.assertFalse(marker.exists(), "captured command left a worker running")

    def test_timeout_stops_child(self):
        result = self.capture(
            "import time; time.sleep(60)", "--theme", "dark", "--timeout", "0.5"
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("timed out", result.stderr)
        self.assertFalse((self.out / "sample-dark.png").exists())


if __name__ == "__main__":
    unittest.main()
