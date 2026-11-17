import argparse
import http.server
import json
import subprocess
import threading
import urllib.error
import urllib.request
from pathlib import Path

import pytest
from harness import evidence


def test_ui_forwards_only_the_api_prefix(tmp_path):
    received = []

    class Backend(http.server.BaseHTTPRequestHandler):
        def do_POST(self):
            received.append(
                (self.path, self.rfile.read(int(self.headers["Content-Length"])))
            )
            self.send_response(400)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":"upstream"}')

        def log_message(self, *args):
            pass

    (tmp_path / "index.html").write_text("Dashboard")
    backend = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Backend)
    api_url = f"http://127.0.0.1:{backend.server_port}"

    def handler(*args, **kwargs):
        return evidence.UIHandler(
            *args,
            api_url=api_url,
            directory=tmp_path,
            csp="connect-src 'self';",
            **kwargs,
        )

    ui = evidence.UIServer(("127.0.0.1", 0), handler)
    threads = [
        threading.Thread(target=server.serve_forever) for server in (backend, ui)
    ]
    for thread in threads:
        thread.start()
    origin = f"http://127.0.0.1:{ui.server_port}"
    try:
        with urllib.request.urlopen(origin + "/runtime-config.js") as response:
            assert json.loads(
                response.read().decode().split("=", 1)[1].strip().rstrip(";")
            ) == {"apiURL": "/api"}
        request = urllib.request.Request(
            origin + "/api/example?cursor=one", data=b"payload"
        )
        with pytest.raises(urllib.error.HTTPError) as error:
            urllib.request.urlopen(request)
        assert error.value.code == 400
        assert error.value.read() == b'{"error":"upstream"}'
        assert received == [("/example?cursor=one", b"payload")]
        with urllib.request.urlopen(origin + "/apix/example") as response:
            assert response.read() == b"Dashboard"
            assert response.headers["Content-Security-Policy"] == "connect-src 'self';"
        assert len(received) == 1
    finally:
        for server in (backend, ui):
            server.shutdown()
            server.server_close()
        for thread in threads:
            thread.join()


def test_reap_removed_checkout(tmp_path, monkeypatch):
    root = tmp_path / "expired"
    root.mkdir()
    evidence.write_json(
        root / "state.json",
        {
            "id": "expired",
            "root": str(root),
            "worktree": str(root / "src"),
            "compose": "loco-ui-evidence-expired",
            "expires_at": 0,
        },
    )
    live = tmp_path / "live"
    evidence.write_json(
        live / "state.json",
        {
            "id": "live",
            "root": str(live),
            "expires_at": evidence.time.time() + evidence.LEASE_S,
        },
    )
    monkeypatch.setattr(evidence, "BASE_DIR", tmp_path)
    commands = []

    def run(cmd, **kwargs):
        commands.append(cmd)
        if cmd[0] == "git":
            return str(Path(__file__).resolve().parents[2])
        if "yq" in cmd:
            assert Path(cmd[-1]).exists()
            return "openbao:test"
        return ""

    monkeypatch.setattr(evidence, "run", run)
    monkeypatch.setattr(
        evidence.subprocess,
        "run",
        lambda *a, **kw: subprocess.CompletedProcess(
            a,
            0,
            stdout="live-openbao\tloco-ui-evidence-live\nforeign-openbao\tforeign-project\n",
        ),
    )
    evidence.reap()
    assert not root.exists()
    assert (live / "state.json").exists()
    down = next(cmd for cmd in commands if "down" in cmd)
    assert down[down.index("-p") + 1] == "loco-ui-evidence-expired"
    assert [cmd for cmd in commands if "down" in cmd] == [down]


def test_postgres_pulled_before_start(tmp_path, monkeypatch):
    repo = Path(__file__).resolve().parents[2]
    monkeypatch.setattr(evidence, "BASE_DIR", tmp_path)
    monkeypatch.setenv(evidence.OPENBAO_ENV, "http://openbao")
    monkeypatch.setattr(evidence, "repo_root", lambda: repo)
    monkeypatch.setattr(evidence, "reap", lambda: None)
    monkeypatch.setattr(evidence, "stop_state", lambda state: None)
    scenario = tmp_path / "scenario.json"
    scenario.write_text("{}")
    commands = []

    def run(cmd, **kwargs):
        commands.append(cmd)
        return "commit"

    monkeypatch.setattr(evidence, "run", run)
    monkeypatch.setattr(
        evidence,
        "wait_postgres",
        lambda container: (_ for _ in ()).throw(RuntimeError("started")),
    )
    with pytest.raises(RuntimeError, match="started"):
        evidence.cmd_up(argparse.Namespace(name="test", ref="HEAD", scenario=scenario))
    pull = next(cmd for cmd in commands if "./tools/pullimage" in cmd)
    start = next(cmd for cmd in commands if cmd[:2] == ["docker", "run"])
    assert commands.index(pull) < commands.index(start)
    assert pull[-1] == start[-1]
    assert start[start.index("--pull") + 1] == "never"


def test_capture_scrolls_drag_target_into_view(tmp_path):
    from playwright.sync_api import sync_playwright

    html = """<style>body {margin:0} #axis {margin-top:1200px;width:400px;height:100px;background:gray}</style>
    <div id="axis"></div><div id="ready" hidden>Brushed</div>
    <script>
    const axis = document.querySelector('#axis');
    let start;
    axis.onmousedown = e => start = e.offsetX;
    axis.onmouseup = e => {
      if (Math.abs(start - 80) < 2 && Math.abs(e.offsetX - 240) < 2)
        document.querySelector('#ready').hidden = false;
    };
    </script>"""
    with sync_playwright() as p:
        browser = p.chromium.launch()
        browser_context = browser.new_context

        def context(**options):
            result = browser_context(**options)
            result.route(
                "http://evidence.test/**",
                lambda route: route.fulfill(body=html, content_type="text/html"),
            )
            return result

        browser.new_context = context
        target = tmp_path / "drag.png"
        evidence.capture(
            browser,
            {"users": {}, "ui_url": "http://evidence.test"},
            {
                "name": "brush",
                "path": "/",
                "ready_selector": "#ready",
                "steps": [
                    {
                        "drag": {
                            "selector": "#axis",
                            "from": [0.2, 0.5],
                            "to": [0.6, 0.5],
                        }
                    }
                ],
            },
            "light",
            False,
            target,
        )
        assert target.exists()
        browser.close()


def test_suite_failure_records_browser_diagnostics(tmp_path):
    import json

    from harness import open_page, session

    with pytest.raises(RuntimeError, match="suite failed"):
        with session(tmp_path) as browser:
            context, page, errors = open_page(browser, user=None, state={})
            page.route(
                "http://evidence.test/",
                lambda route: route.fulfill(
                    body='<script>fetch("/broken").catch(() => {}); fetch("/unavailable"); throw new Error("page broke")</script>',
                    content_type="text/html",
                ),
            )
            page.route(
                "http://evidence.test/broken", lambda route: route.abort("failed")
            )
            page.route(
                "http://evidence.test/unavailable",
                lambda route: route.fulfill(status=503, body="unavailable"),
            )
            page.goto("http://evidence.test/")
            page.wait_for_function("document.readyState === 'complete'")
            # Flush pending failed-request events before triggering the suite failure.
            page.wait_for_load_state("networkidle")
            raise RuntimeError("suite failed")
    detail = json.loads((tmp_path / "failure-0-0.json").read_text())
    assert detail["url"] == "http://evidence.test/"
    assert detail["page_errors"] == ["page broke"]
    assert sorted(detail["failed_requests"], key=lambda request: request["url"]) == [
        {"url": "http://evidence.test/broken", "failure": "net::ERR_FAILED"},
        {"url": "http://evidence.test/unavailable", "status": 503},
    ]


def test_settle_excludes_only_named_targets():
    from playwright.sync_api import sync_playwright

    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page()
        page.set_content('<div id="countdown"></div><div id="transition"></div>')
        page.evaluate("""() => {
          window.countdown = document.querySelector('#countdown').animate({opacity: [1, 0]}, 30000);
          window.transition = document.querySelector('#transition').animate({opacity: [0, 1]}, 300);
        }""")
        evidence.settle(page, exclude_animations=["#countdown"])
        assert page.evaluate("countdown.playState") == "running"
        assert page.evaluate("transition.playState") == "finished"
        browser.close()
