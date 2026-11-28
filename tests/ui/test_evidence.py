import argparse
import json
import re
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
        if args[0] == "share":
            root = tmp_path / "leases" / args[1]
            root.mkdir(parents=True, exist_ok=True)
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
    monkeypatch.setattr(evidence, "openbao_env", lambda source, env: env)
    monkeypatch.setattr(evidence, "compose", lambda *args, **kwargs: "")
    evidence.cmd_openbao(argparse.Namespace())
    address = capsys.readouterr().out.strip()
    openbao = next(call[1] for call in leases if call[0] == "add")
    assert ("add", openbao, "compose", Path(openbao).name) in leases

    monkeypatch.setenv("TMPDIR", str(Path(openbao).parent / "elsewhere"))
    capture = {"id": "capture", "root": "/capture", "openbao": address}
    evidence.renew(capture)
    assert leases[-1] == ("renew", "/capture", openbao)


@pytest.mark.parametrize(
    "platform, serves",
    [
        ("darwin", ("-p", f"{evidence.HOST}:20000:5432")),
        ("linux", ("--network", "host", "-e", "PGPORT=20000")),
    ],
)
def test_postgres_pulled_and_recorded_before_start(
    tmp_path, monkeypatch, leases, platform, serves
):
    monkeypatch.setattr(evidence.sys, "platform", platform)
    repo = Path(__file__).resolve().parents[2]
    monkeypatch.setenv(evidence.OPENBAO_ENV, "http://openbao")
    monkeypatch.setenv(evidence.BUILD_ENV, str(repo))
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
    assert pull[-1] in start
    assert start[start.index("--pull") + 1] == "never"
    at = start.index(serves[0])
    assert start[at : at + len(serves)] == serves


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


def test_repeated_suite_runs_keep_their_own_failure_artifacts(tmp_path, monkeypatch):
    import json

    from harness import open_page, session

    out = tmp_path / "settings-name"
    for label, url in (
        ("settings_name_ui", "http://evidence.test/first"),
        ("settings_name_ui#2", "http://evidence.test/second"),
    ):
        monkeypatch.setenv("UI_RUN_LABEL", label)
        with pytest.raises(RuntimeError, match="suite failed"):
            with session(out) as browser:
                context, page, errors = open_page(browser, user=None, state={})
                page.route(url, lambda route: route.fulfill(body="ok"))
                page.goto(url)
                raise RuntimeError("suite failed")
    for directory, url in (
        (out, "http://evidence.test/first"),
        (tmp_path / "settings-name#2", "http://evidence.test/second"),
    ):
        assert json.loads((directory / "failure-0-0.json").read_text())["url"] == url


TCP4 = """  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:69AA 00000000:0000 0A 00000000:00000002 00:00000000 00000000  1000        0 101 1
   1: 0100007F:1F90 0100007F:C351 01 00000010:00000020 00:00000000 00000000  1000        0 102 1
"""
TCP6 = """  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:C350 00000000000000000000000001000000:69AA 01 000003E8:00000000 00:00000000 00000000  1000        0 103 1
"""


def fake_tcp_tables(tmp_path, monkeypatch):
    import harness

    (tmp_path / "tcp").write_text(TCP4)
    (tmp_path / "tcp6").write_text(TCP6)
    monkeypatch.setattr(
        harness,
        "TCP_TABLES",
        (tmp_path / "tcp", tmp_path / "tcp6", tmp_path / "missing"),
    )


def test_tcp_sockets_lists_the_ports_ipv4_and_ipv6_connections(tmp_path, monkeypatch):
    from harness import tcp_sockets

    fake_tcp_tables(tmp_path, monkeypatch)
    assert tcp_sockets(27050) == [
        {
            "local": 27050,
            "remote": 0,
            "state": "LISTEN",
            "send_queue": 0,
            "receive_queue": 2,
        },
        {
            "local": 50000,
            "remote": 27050,
            "state": "ESTABLISHED",
            "send_queue": 1000,
            "receive_queue": 0,
        },
    ]


def test_tcp_sockets_without_proc_is_empty(tmp_path, monkeypatch):
    import harness

    monkeypatch.setattr(harness, "TCP_TABLES", (tmp_path / "tcp",))
    assert harness.tcp_sockets(27050) == []


def test_suite_failure_records_ui_sockets_when_navigation_never_commits(
    tmp_path, monkeypatch
):
    import harness
    from harness import open_page, session
    from playwright.sync_api import TimeoutError as PlaywrightTimeout

    fake_tcp_tables(tmp_path, monkeypatch)
    (tmp_path / "ui.log").write_text("served\n")
    monkeypatch.setattr(harness, "UI_SERVERS", [(27050, tmp_path / "ui.log")])
    out = tmp_path / "out"
    held = []
    with pytest.raises(PlaywrightTimeout):
        with session(out) as browser:
            context, page, errors = open_page(browser, user=None, state={})
            page.set_default_timeout(1000)
            page.route("http://evidence.test/", lambda route: held.append(route))
            page.goto("http://evidence.test/")
    assert json.loads((out / "failure-0-0.json").read_text())["url"] == "about:blank"
    assert [
        s["local"] for s in json.loads((out / "ui-0-sockets.json").read_text())
    ] == [
        27050,
        50000,
    ]
    assert (out / "ui-0.log").read_text() == "served\n"


def test_probe_reports_a_document_still_loading(tmp_path):
    from harness import open_page, probe, session

    held = []
    with session(tmp_path) as browser:
        context, page, errors = open_page(browser, user=None, state={})
        page.route(
            "http://evidence.test/",
            lambda route: route.fulfill(
                body='<script src="/held.js"></script>', content_type="text/html"
            ),
        )
        page.route("http://evidence.test/held.js", lambda route: held.append(route))
        page.goto("http://evidence.test/", wait_until="commit")
        page.wait_for_function("() => document.scripts.length === 1")
        assert probe(context, page)["document"] == "loading"
        context.close()


def test_ui_logs_the_client_port_once_a_body_is_written(tmp_path, monkeypatch):
    import http.client
    import threading

    (tmp_path / "index.html").write_text("app")
    lines = []
    monkeypatch.setattr(evidence, "log", lines.append)

    def handler(*a, **kw):
        return evidence.UIHandler(*a, directory=str(tmp_path), api_url="", **kw)

    with evidence.UIServer((evidence.HOST, 0), handler) as server:
        threading.Thread(target=server.serve_forever, daemon=True).start()
        connection = http.client.HTTPConnection(evidence.HOST, server.server_address[1])
        connection.connect()
        port = connection.sock.getsockname()[1]
        connection.request("GET", "/org/x?env=production")
        assert connection.getresponse().read() == b"app"
        server.shutdown()
    assert any(line.endswith(f"port {port} wrote body") for line in lines)


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


def test_settle_waits_for_the_loading_screen_to_leave():
    from playwright.sync_api import sync_playwright

    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page()
        page.set_content('<div data-slot="app-loader"></div><main>Ready</main>')
        page.evaluate("""() => setTimeout(
          () => document.querySelector('[data-slot="app-loader"]').remove(), 300)""")
        evidence.settle(page)
        assert page.locator('[data-slot="app-loader"]').count() == 0
        browser.close()


def test_down_all_stops_every_evidence_lease(leases, monkeypatch):
    monkeypatch.setattr(
        evidence, "run", lambda cmd, **kwargs: leases.append(tuple(cmd))
    )
    early = evidence.testres("lease", "-name", f"{evidence.LABEL}-before")
    other = evidence.testres("lease", "-name", "loco-clickhouse-test")
    evidence.cmd_down(argparse.Namespace(all=True, state=None))
    assert ("release", early) in leases
    assert ("release", other) not in leases
    assert ("git", "worktree", "prune") in leases


PLACEHOLDER_IDS = {
    "org": {"acme": "org-1"},
    "workspace": {"acme/shared": "ws-1"},
    "resource": {"acme/shared/web": "res-1"},
    "environment": {"acme/shared/production": "env-1"},
}
NOW = 1_800_000_000


def test_placeholders_fill_ids_and_times():
    respond = {
        "series": [
            {
                "resourceId": "{resource:acme/shared/web}",
                "points": [{"timestamp": "{now-15m}", "value": 0.31}],
            }
        ],
        "environmentId": "{environment:acme/shared/production}",
        "at": "{now+1h:s}",
        "late": "{now+2d}",
    }
    assert evidence.fill_placeholders(respond, PLACEHOLDER_IDS, NOW) == {
        "series": [
            {
                "resourceId": "res-1",
                "points": [{"timestamp": "2027-01-15T07:45:00Z", "value": 0.31}],
            }
        ],
        "environmentId": "env-1",
        "at": NOW + 3600,
        "late": "2027-01-17T08:00:00Z",
    }
    path = "/org/{org:acme}/wks/{workspace:acme/shared}/observability?from={now-1h:ms}&to={now:ms}&at={now}"
    assert (
        evidence.fill_placeholders(path, PLACEHOLDER_IDS, NOW)
        == f"/org/org-1/wks/ws-1/observability?from={(NOW - 3600) * 1000}&to={NOW * 1000}&at=2027-01-15T08:00:00Z"
    )


def test_placeholders_leave_other_braces():
    text = '{"level": "info"} {nowhere}'
    assert evidence.fill_placeholders(text, PLACEHOLDER_IDS, NOW) == text


@pytest.mark.parametrize(
    "value, named",
    [
        ("{resource:acme/shared/api}", "{resource:acme/shared/api}"),
        ("/org/{org}", "{org}"),
        ("{workspace:acme}", "{workspace:acme}"),
        ("{now-15x}", "{now-15x}"),
        ("{now:us}", "{now:us}"),
    ],
)
def test_placeholder_errors_name_the_placeholder(value, named):
    with pytest.raises(evidence.EvidenceError, match=re.escape(named)):
        evidence.fill_placeholders({"a": [value]}, PLACEHOLDER_IDS, NOW)


SCENARIO = {
    "users": {"owner": {"email": "owner@acme.test"}},
    "orgs": [{"name": "acme", "owner": "owner", "workspaces": ["shared"]}],
    "resources": [{"workspace": "acme/shared", "name": "web", "deployments": []}],
    "events": [
        {"type": "deployment.ready", "resource": "acme/shared/web", "at": "{now-5m}"}
    ],
}
METRICS = "**/loco.observability.v1.ObservabilityService/QueryMetrics"


def shot(**fields):
    return {
        "name": "page",
        "user": "owner",
        "path": "/",
        "ready_selector": "main",
        **fields,
    }


def test_check_passes_a_correct_spec():
    spec = {
        "shots": [
            shot(
                path="/org/{org:acme}/wks/{workspace:acme/shared}/resource/{resource:acme/shared/web}",
                respond=[
                    {"url": METRICS, "match": {"byPod": True}, "json": {}},
                    {
                        "url": METRICS,
                        "json": {"at": "{environment:acme/shared/production}"},
                    },
                ],
            ),
            {
                "name": "landing",
                "signedOut": True,
                "path": "/",
                "ready_selector": "main",
            },
        ]
    }
    assert evidence.check_spec(spec, SCENARIO, {METRICS[3:]}) == []


@pytest.mark.parametrize(
    "fields, error",
    [
        (
            {"respond": [{"url": "**/ObservabilityService/QueryMetrics", "json": {}}]},
            "matches no RPC",
        ),
        ({"hold_requests": ["**/loco.x.v1.S/Missing"]}, "hold_requests"),
        ({"user": None}, "user is missing"),
        ({"user": "member"}, "not a scenario user"),
        (
            {
                "respond": [
                    {"url": METRICS, "json": {}},
                    {"url": METRICS, "match": {"byPod": True}, "json": {}},
                ]
            },
            "respond entry 1",
        ),
        ({"path": "/{resource:acme/shared/api}"}, "{resource:acme/shared/api}"),
        ({"respond": [{"url": METRICS, "json": {"t": "{now-5x}"}}]}, "{now-5x}"),
        ({"themes": ["sepia"]}, "themes"),
    ],
)
def test_check_names_the_shot_and_field(fields, error):
    page = {k: v for k, v in shot(**fields).items() if v is not None}
    errors = evidence.check_spec({"shots": [page]}, SCENARIO, {METRICS[3:]})
    assert len(errors) == 1 and error in errors[0], errors
    assert "shot page" in errors[0]


def test_failure_summary_lists_failing_suites_and_shots():
    suites = "\n".join(
        [
            "::group::events_ui",
            "ok line",
            "::endgroup::",
            "events_ui passed in 4s",
            "::group::trace_ui",
            "Traceback (most recent call last):",
            "AssertionError: expected 3 rows",
            "::endgroup::",
            "trace_ui failed in 9s",
        ]
    )
    shots = "error: something\nFAILED replicas-after-light.png: selector never became visible\n"
    summary = evidence.failure_summary(suites, shots)
    assert (
        "## Suite trace_ui" in summary and "AssertionError: expected 3 rows" in summary
    )
    assert "events_ui" not in summary
    assert "## Screenshots" in summary and "FAILED replicas-after-light.png" in summary


def test_spec_respond_follows_each_shots_own_entries():
    own = {"url": METRICS, "match": {"byPod": True}, "json": {"own": 1}}
    shared = {"url": METRICS, "json": {"shared": 1}}
    spec = {"respond": [shared], "shots": [shot(respond=[own]), shot(name="other")]}
    first, second = evidence.validate_spec(spec)
    assert first["respond"] == [own, shared]
    assert second["respond"] == [shared]


def test_spec_respond_entries_are_validated():
    spec = {"respond": [{"url": METRICS}], "shots": [shot()]}
    with pytest.raises(evidence.EvidenceError, match="spec: each respond entry"):
        evidence.validate_spec(spec)


def test_deployment_build_is_seeded_and_named_by_the_spec(monkeypatch):
    statements = []

    def psql(state, sql):
        if sql.startswith("SELECT json_build_object"):
            return '{"org": "o", "workspace": "w", "slug": "web", "environments": {"production": "e"}}'
        statements.extend(sql.splitlines())
        return ""

    monkeypatch.setattr(evidence, "psql", psql)
    policy = {
        "restartPolicy": "RESTART_POLICY_ON_FAILURE",
        "maxRestarts": 2,
        "deployTimeoutSeconds": 600,
    }
    monkeypatch.setattr(
        evidence, "rpc", lambda *args: {"serviceDefaults": {"failurePolicy": policy}}
    )
    state = {
        "api_url": "http://api.invalid",
        "users": {"owner": {"id": "u"}},
        "orgs": {},
    }
    spec = {
        "build": {"type": "dockerfile", "image": "example.invalid/web:1"},
        "regions": {"us-ca-1": {"cpu": "500m", "memory": "256Mi", "minReplicas": 1}},
    }
    deployment = {
        "environment": "production",
        "status": "failed",
        "live": False,
        "build": {"status": "succeeded", "finished_at": "{now-6m}"},
        "spec": spec,
    }
    evidence.seed_deployments(state, "r", [deployment])
    [build] = [s for s in statements if s.startswith("INSERT INTO builds(")]
    [inserted] = [s for s in statements if s.startswith("INSERT INTO deployments(")]
    build_id = re.search(r"VALUES\('([0-9a-f-]{36})'", build).group(1)
    assert f'"buildId": "{build_id}"' in inserted
    assert "'succeeded'" in build and re.search(r"'\d{4}-\d\d-\d\dT[\d:]+Z'", build)
    assert "buildId" not in spec["build"]
    assert f'"failurePolicy": {json.dumps(policy)}' in inserted


def test_ui_serves_the_issuer_sign_in_page(tmp_path):
    import threading
    import urllib.request

    (tmp_path / "index.html").write_text("app")

    def handler(*a, **kw):
        return evidence.UIHandler(*a, directory=str(tmp_path), api_url="", **kw)

    with evidence.UIServer((evidence.HOST, 0), handler) as server:
        threading.Thread(target=server.serve_forever, daemon=True).start()
        base = f"http://{evidence.HOST}:{server.server_address[1]}"
        with urllib.request.urlopen(base + evidence.DISCOVERY_PATH) as response:
            authorize = json.load(response)["authorization_endpoint"]
        with urllib.request.urlopen(authorize + "?client_id=loco") as response:
            assert response.headers["Content-Type"] == "text/html"
            assert "<h1>Sign in</h1>" in response.read().decode()
        server.shutdown()
