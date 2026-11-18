import argparse
import subprocess
from pathlib import Path

import evidence
import pytest


@pytest.fixture(autouse=True)
def no_docker(monkeypatch):
    class Popen(subprocess.Popen):
        def __init__(self, args, *rest, **kwargs):
            program = args.split()[0] if isinstance(args, str) else args[0]
            if Path(program).name == "docker":
                pytest.fail(f"test ran real docker: {args}")
            super().__init__(args, *rest, **kwargs)

    monkeypatch.setattr(subprocess, "Popen", Popen)


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
            "container": "loco-ui-evidence-expired",
            "expires_at": 0,
        },
    )
    live = tmp_path / "live"
    evidence.write_json(
        live / "state.json",
        {
            "id": "live",
            "root": str(live),
            "compose": "loco-ui-evidence-live",
            "container": "loco-ui-evidence-live",
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
    removed = []

    def docker(cmd, **kwargs):
        if cmd[:2] == ["docker", "rm"]:
            removed.append(cmd[-1])
        return subprocess.CompletedProcess(
            cmd,
            0,
            stdout=f"live-openbao\tloco-ui-evidence-live\t{live / 'state.json'}\n"
            f"loco-ui-evidence-live\t\t{live / 'state.json'}\n"
            f"loco-ui-evidence-expired\t\t{root / 'state.json'}\n"
            "foreign-openbao\tforeign-project\t\n",
        )

    monkeypatch.setattr(evidence.subprocess, "run", docker)
    evidence.reap()
    assert set(removed) == {"loco-ui-evidence-expired"}
    assert not root.exists()
    assert (live / "state.json").exists()
    down = next(cmd for cmd in commands if "down" in cmd)
    assert down[down.index("-p") + 1] == "loco-ui-evidence-expired"
    assert [cmd for cmd in commands if "down" in cmd] == [down]


def test_shared_openbao_outlives_its_starting_shell(tmp_path, monkeypatch, capsys):
    repo = Path(__file__).resolve().parents[2]
    clock = [1000.0]
    monkeypatch.setattr(evidence.time, "time", lambda: clock[0])
    monkeypatch.setattr(evidence, "BASE_DIR", tmp_path)
    monkeypatch.setattr(evidence, "repo_root", lambda: repo)
    projects = []

    def start_openbao(worktree, project, env, state_path):
        projects.append(project)
        return "http://127.0.0.1:8200"

    monkeypatch.setattr(evidence, "start_openbao", start_openbao)
    commands = []

    def run(cmd, **kwargs):
        commands.append(cmd)
        return "openbao:test" if "yq" in cmd else ""

    monkeypatch.setattr(evidence, "run", run)
    monkeypatch.setattr(
        evidence.subprocess,
        "run",
        lambda *a, **kw: subprocess.CompletedProcess(
            a,
            0,
            stdout="".join(
                f"openbao-1\t{p}\t{tmp_path / p.removeprefix('loco-ui-evidence-') / 'state.json'}\n"
                for p in projects
            ),
        ),
    )
    evidence.cmd_openbao(argparse.Namespace())
    address = capsys.readouterr().out.strip()

    def stopped():
        return [cmd[cmd.index("-p") + 1] for cmd in commands if "down" in cmd]

    capture = tmp_path / "capture"
    evidence.write_json(
        capture / "state.json",
        {"id": "capture", "root": str(capture), "openbao": address},
    )
    clock[0] += evidence.LEASE_S - 1
    evidence.renew(evidence.load_state(capture / "state.json"))
    clock[0] += 2
    evidence.reap()
    assert stopped() == []
    clock[0] += evidence.LEASE_S
    evidence.reap()
    assert not capture.exists()
    assert projects[0] in stopped()


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
        evidence.start_state(name="test", ref="HEAD", scenario=scenario)
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
                    body='<script>Promise.allSettled([fetch("/broken"), fetch("/unavailable")]).then(() => { document.title = "fetched"; }); throw new Error("page broke")</script>',
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
            page.wait_for_function("document.title === 'fetched'")
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


def test_reap_keeps_containers_whose_run_lives_elsewhere(tmp_path, monkeypatch):
    other = tmp_path / "other-tmpdir" / "capture"
    evidence.write_json(
        other / "state.json",
        {
            "id": "capture",
            "root": str(other),
            "expires_at": evidence.time.time() + evidence.LEASE_S,
        },
    )
    expired = tmp_path / "other-tmpdir" / "expired"
    evidence.write_json(
        expired / "state.json",
        {
            "id": "expired",
            "root": str(expired),
            "container": "loco-ui-evidence-expired",
            "expires_at": 0,
        },
    )
    gone = tmp_path / "other-tmpdir" / "gone" / "state.json"
    monkeypatch.setattr(evidence, "BASE_DIR", tmp_path / "own")
    monkeypatch.setattr(evidence, "run", lambda cmd, **kwargs: "")
    removed = []

    def docker(cmd, **kwargs):
        if cmd[:2] == ["docker", "rm"]:
            removed.append(cmd[-1])
        return subprocess.CompletedProcess(
            cmd,
            0,
            stdout=f"loco-ui-evidence-capture\t\t{other / 'state.json'}\n"
            f"loco-ui-evidence-expired\t\t{expired / 'state.json'}\n"
            f"loco-ui-evidence-gone\t\t{gone}\n"
            "loco-ui-evidence-unlabelled\t\t\n",
        )

    monkeypatch.setattr(evidence.subprocess, "run", docker)
    evidence.reap()
    assert removed == ["loco-ui-evidence-expired", "loco-ui-evidence-gone"]
    assert (other / "state.json").exists()
    assert not expired.exists()
