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


@pytest.fixture
def leases(tmp_path, monkeypatch):
    """Stands in for tools/testres: records its calls and hands out lease directories."""
    calls = []
    created = []

    def testres(*args):
        args = tuple(map(str, args))
        calls.append(args)
        if args[0] == "lease":
            root = (
                tmp_path / "leases" / f"{args[args.index('-name') + 1]}-{len(created)}"
            )
            root.mkdir(parents=True)
            created.append(root)
            return str(root)
        if args[0] == "list":
            return "\n".join(map(str, created))
        if args[0] == "ports":
            return "\n".join(str(20000 + i) for i in range(int(args[2])))
        return ""

    monkeypatch.setattr(evidence, "testres", testres)
    return calls


def test_capture_renews_the_shared_openbao_it_uses(leases, monkeypatch, capsys):
    monkeypatch.setattr(evidence, "repo_root", lambda: Path(__file__).parents[2])
    monkeypatch.setattr(
        evidence, "start_openbao", lambda *args: "http://127.0.0.1:8200"
    )
    evidence.cmd_openbao(argparse.Namespace())
    address = capsys.readouterr().out.strip()
    openbao = next(call[1] for call in leases if call[0] == "add")
    assert ("add", openbao, "compose", Path(openbao).name) in leases

    monkeypatch.setenv("TMPDIR", str(Path(openbao).parent / "elsewhere"))
    capture = {"id": "capture", "root": "/capture", "openbao": address}
    evidence.renew(capture)
    assert leases[-1] == ("renew", "/capture", openbao)


def test_postgres_pulled_and_recorded_before_start(tmp_path, monkeypatch, leases):
    repo = Path(__file__).resolve().parents[2]
    monkeypatch.setenv(evidence.OPENBAO_ENV, "http://openbao")
    monkeypatch.setattr(evidence, "repo_root", lambda: repo)
    monkeypatch.setattr(evidence, "stop_state", lambda state: None)
    scenario = tmp_path / "scenario.json"
    scenario.write_text("{}")

    def run(cmd, **kwargs):
        leases.append(tuple(cmd))
        return "commit"

    monkeypatch.setattr(evidence, "run", run)
    monkeypatch.setattr(
        evidence,
        "wait_postgres",
        lambda container: (_ for _ in ()).throw(RuntimeError("started")),
    )
    with pytest.raises(RuntimeError, match="started"):
        evidence.start_state(name="test", ref="HEAD", scenario=scenario)
    pull = next(call for call in leases if "./tools/pullimage" in call)
    start = next(call for call in leases if call[:2] == ("docker", "run"))
    name = start[start.index("--name") + 1]
    recorded = next(call for call in leases if call[0] == "add" and call[3] == name)
    assert leases.index(pull) < leases.index(start)
    assert leases.index(recorded) < leases.index(start)
    assert pull[-1] == start[-1]
    assert start[start.index("--pull") + 1] == "never"
    assert f"{evidence.HOST}:20000:5432" in start


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
