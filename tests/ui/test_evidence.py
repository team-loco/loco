import argparse
import importlib.util
import subprocess
from pathlib import Path

import pytest

spec = importlib.util.spec_from_file_location(
    "evidence", Path(__file__).resolve().parents[2] / ".agents/skills/ui-evidence/scripts/evidence.py",
)
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)


def test_reap_removed_checkout(tmp_path, monkeypatch):
    root = tmp_path / "expired"
    root.mkdir()
    evidence.write_json(root / "state.json", {
        "id": "expired", "root": str(root), "worktree": str(root / "src"),
        "compose": "loco-ui-evidence-expired", "expires_at": 0,
    })
    live = tmp_path / "live"
    evidence.write_json(live / "state.json", {
        "id": "live", "root": str(live), "expires_at": evidence.time.time() + evidence.LEASE_S,
    })
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
    monkeypatch.setattr(evidence.subprocess, "run", lambda *a, **kw: subprocess.CompletedProcess(
        a, 0, stdout="live-openbao\tloco-ui-evidence-live\nforeign-openbao\tforeign-project\n",
    ))
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
    monkeypatch.setattr(evidence, "wait_postgres", lambda container: (_ for _ in ()).throw(RuntimeError("started")))
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
            result.route("http://evidence.test/**", lambda route: route.fulfill(body=html, content_type="text/html"))
            return result

        browser.new_context = context
        target = tmp_path / "drag.png"
        evidence.capture(browser, {"users": {}, "ui_url": "http://evidence.test"}, {
            "name": "brush", "path": "/", "ready_selector": "#ready",
            "steps": [{"drag": {"selector": "#axis", "from": [0.2, 0.5], "to": [0.6, 0.5]}}],
        }, "light", False, target)
        assert target.exists()
        browser.close()
