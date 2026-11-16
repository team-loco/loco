import json
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Event, Thread

from playwright.sync_api import sync_playwright

import evidence


PAGE = b"""<!doctype html>
<html>
<button hidden>Show output</button>
<script>
document.documentElement.classList.toggle("dark", localStorage.getItem("loco:mode:v1") === "dark");
const button = document.querySelector("button");
fetch("/stream").then(async response => {
  await response.body.getReader().read();
  button.hidden = false;
});
button.onclick = () => setTimeout(() => {
  const output = document.createElement("p");
  output.id = "output";
  output.textContent = "Live output";
  document.body.append(output);
}, 500);
</script>
</html>
"""


def test_capture_waits_for_selector_with_open_stream(tmp_path):
    stop = Event()

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            if self.path == "/stream":
                self.send_header("Content-Type", "application/octet-stream")
                self.end_headers()
                self.wfile.write(b"live output\n")
                self.wfile.flush()
                stop.wait()
            else:
                self.send_header("Content-Type", "text/html")
                self.end_headers()
                self.wfile.write(PAGE)

    with ThreadingHTTPServer(("127.0.0.1", 0), Handler) as server:
        thread = Thread(target=server.serve_forever)
        thread.start()
        try:
            state = {"ui_url": f"http://127.0.0.1:{server.server_port}", "users": {"user": {"token": "fixture"}}}
            shot = {
                "name": "live-output",
                "user": "user",
                "path": "/",
                "steps": [{"click": "button"}],
                "ready_selector": "#output",
                "crop": {"selector": "#output"},
            }
            target = tmp_path / "live-output.png"
            with sync_playwright() as playwright, playwright.chromium.launch() as browser:
                evidence.capture(browser, state, evidence.validate_spec({"shots": [shot]})[0], "dark", False, target)
            assert target.read_bytes().startswith(b"\x89PNG\r\n\x1a\n")
        finally:
            stop.set()
            server.shutdown()
            thread.join()


def test_serve_ui_serves_this_runs_addresses_from_a_shared_build(tmp_path):
    (tmp_path / "index.html").write_text("app")
    (tmp_path / "runtime-config.js").write_text("window.locoConfig = {};\n")
    handler = lambda *a, **kw: evidence.UIHandler(*a, directory=str(tmp_path), api_url="http://127.0.0.1:9", **kw)
    with evidence.UIServer(("127.0.0.1", 0), handler) as server:
        thread = Thread(target=server.serve_forever)
        thread.start()
        try:
            origin = f"http://127.0.0.1:{server.server_port}"
            fetch = lambda path: urllib.request.urlopen(origin + path, timeout=5)
            with fetch("/runtime-config.js") as response:
                assert response.headers["Content-Type"] == "text/javascript"
                assert response.read() == b'window.locoConfig = {"apiURL": "http://127.0.0.1:9"};\n'
            with fetch(evidence.DISCOVERY_PATH) as response:
                assert json.loads(response.read())["issuer"] == origin + "/dex"
            with fetch("/org/a/wks/b") as response:
                assert response.read() == b"app"
        finally:
            server.shutdown()
            thread.join()


def test_openbao_pulls_the_chart_image_before_starting(monkeypatch, tmp_path):
    calls = []

    def run(cmd, **kwargs):
        calls.append(cmd)
        if cmd[0:4] == ["mise", "exec", "--", "yq"]:
            return "registry.test/openbao:fixture\n"
        if "port" in cmd:
            return "127.0.0.1:18200\n"
        return ""

    monkeypatch.setattr(evidence, "run", run)
    assert evidence.start_openbao(tmp_path, "fixture", {}) == "http://127.0.0.1:18200"
    assert calls[1] == ["mise", "exec", "--", "go", "run", "./tools/pullimage", "registry.test/openbao:fixture"]
    assert calls[2][-6:] == ["up", "-d", "--wait", "--pull", "never", "openbao"]
    assert calls[3][-5:] == ["run", "--rm", "--pull", "never", "openbao-seed"]


def test_openbao_failure_keeps_diagnostics_before_cleanup(monkeypatch, tmp_path, capsys):
    import pytest

    calls = []
    failure = evidence.EvidenceError("seed failed (37): initialization refused")

    def run(cmd, **kwargs):
        calls.append(cmd)
        if cmd[0:4] == ["mise", "exec", "--", "yq"]:
            return "registry.test/openbao:fixture\n"
        if "openbao-seed" in cmd:
            raise failure
        if "logs" in cmd:
            return "server: initialization refused"
        if "-q" in cmd:
            return "fixture-container\n"
        if "inspect" in cmd:
            return '{"OOMKilled":true,"ExitCode":137} restart_count=2'
        return ""

    monkeypatch.setattr(evidence, "run", run)
    monkeypatch.setattr(evidence, "stop_compose", lambda project: calls.append(["cleanup", project]))
    with pytest.raises(evidence.EvidenceError) as caught:
        evidence.start_openbao(tmp_path, "fixture", {})
    assert caught.value is failure
    assert calls[-1] == ["cleanup", "fixture"]
    assert "server: initialization refused" in capsys.readouterr().err
    assert any("inspect" in cmd for cmd in calls[:-1])


def test_openbao_pull_failure_stops_startup(monkeypatch, tmp_path):
    import pytest

    calls = []
    failure = evidence.EvidenceError("pull failed after 5 attempts")

    def run(cmd, **kwargs):
        calls.append(cmd)
        if "yq" in cmd:
            return "registry.test/openbao:fixture\n"
        raise failure

    monkeypatch.setattr(evidence, "run", run)
    with pytest.raises(evidence.EvidenceError) as caught:
        evidence.start_openbao(tmp_path, "fixture", {})
    assert caught.value is failure
    assert len(calls) == 2


def test_ui_task_preserves_startup_failure_status_and_cleans_up(tmp_path):
    import os
    import subprocess

    (tmp_path / "uv").write_text('#!/bin/sh\ncase "$*" in\n*build) exit 0 ;;\n*openbao*) echo "seed refused" >&2; exit 37 ;;\nesac\n')
    (tmp_path / "docker").write_text('#!/bin/sh\necho "$*" >> "$CLEANUP_LOG"\nexit 49\n')
    for name in ("uv", "docker"):
        (tmp_path / name).chmod(0o755)
    cleanup = tmp_path / "cleanup.log"
    root = evidence.Path(__file__).resolve().parents[4]
    result = subprocess.run(
        ["bash", str(root / "mise-tasks/test/ui"), "audit_ui"], cwd=root,
        env=dict(os.environ, PATH=f"{tmp_path}:{os.environ['PATH']}", CLEANUP_LOG=str(cleanup)),
        capture_output=True, text=True,
    )
    assert result.returncode == 37, result.stderr
    assert "seed refused" in result.stderr
    assert "down -v --remove-orphans" in cleanup.read_text()
