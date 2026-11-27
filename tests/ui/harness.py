import atexit
import contextlib
import json
import os
import shutil
import time
from pathlib import Path
from urllib.parse import parse_qs, urlparse

import evidence
from playwright.sync_api import sync_playwright

repo = Path(__file__).resolve().parents[2]
# The port and log file of every UI server this suite started.
UI_SERVERS = []
TCP_TABLES = ("/proc/net/tcp", "/proc/net/tcp6")

# A clipboard per page; every suite shares one Chromium and its one system clipboard.
CLIPBOARD_JS = """Object.defineProperty(navigator, "clipboard", {
  value: { writeText: async text => { window.__copied = String(text); } },
});"""


def load_state(args, name, scenario, *, issuer=None):
    os.chdir(repo)
    if args.state is None:
        args.state = evidence.start_state(
            ref="HEAD", name=name, scenario=repo / scenario, issuer=issuer
        )
        print("Preview state:", args.state, flush=True)
        if not args.keep:
            atexit.register(
                lambda: evidence.stop_state(evidence.load_state(args.state))
            )
    state = evidence.load_state(args.state)
    UI_SERVERS.append((state["ports"]["ui"], Path(state["root"]) / "ui.log"))
    return state


TCP_STATES = [
    "",
    "ESTABLISHED",
    "SYN_SENT",
    "SYN_RECV",
    "FIN_WAIT1",
    "FIN_WAIT2",
    "TIME_WAIT",
    "CLOSE",
    "CLOSE_WAIT",
    "LAST_ACK",
    "LISTEN",
    "CLOSING",
]


def tcp_sockets(port):
    """Linux's TCP sockets on `port` with their queued bytes; a LISTEN socket's receive queue counts unaccepted connections."""
    sockets = []
    for table in TCP_TABLES:
        with contextlib.suppress(OSError):
            for line in Path(table).read_text().splitlines()[1:]:
                _, local, remote, state, queues = line.split()[:5]
                ports = [
                    int(address.rsplit(":", 1)[1], 16) for address in (local, remote)
                ]
                if port in ports:
                    send, receive = (int(queue, 16) for queue in queues.split(":"))
                    sockets.append(
                        {
                            "local": ports[0],
                            "remote": ports[1],
                            "state": TCP_STATES[int(state, 16)],
                            "send_queue": send,
                            "receive_queue": receive,
                        }
                    )
    return sockets


def probe(context, page):
    """The page's readyState and a fetch of its URL outside the renderer."""
    try:
        document = page.wait_for_function(
            "document.readyState", timeout=3000
        ).json_value()
    except Exception as err:
        document = {"error": str(err).splitlines()[0]}
    try:
        response = context.request.get(page.url, timeout=3000)
        fetched = {"status": response.status, "bytes": len(response.body())}
    except Exception as err:
        fetched = {"error": str(err).splitlines()[0]}
    return {"document": document, "fetched": fetched}


@contextlib.contextmanager
def session(out, **launch_options):
    with (
        sync_playwright() as playwright,
        evidence.shared_browser(playwright, **launch_options) as browser,
    ):
        try:
            yield browser
        except BaseException:
            sockets = [tcp_sockets(port) for port, _ in UI_SERVERS]
            # test:ui labels a suite's repeat runs name#2, name#3; each keeps its own directory.
            _, repeat, number = os.environ.get("UI_RUN_LABEL", "").partition("#")
            if repeat:
                out = out.with_name(f"{out.name}#{number}")
            out.mkdir(parents=True, exist_ok=True)
            for number, (_, ui_log) in enumerate(UI_SERVERS):
                evidence.write_json(out / f"ui-{number}-sockets.json", sockets[number])
                with contextlib.suppress(Exception):
                    shutil.copy(ui_log, out / f"ui-{number}.log")
            for index, context in enumerate(browser.contexts):
                with contextlib.suppress(Exception):
                    context.tracing.stop(path=out / f"failure-{index}.zip")
                for number, page in enumerate(context.pages):
                    with contextlib.suppress(Exception):
                        evidence.write_json(
                            out / f"failure-{index}-{number}.json",
                            {
                                "url": page.url,
                                "probe": probe(context, page),
                                **getattr(page, "diagnostics", {}),
                            },
                        )
                        page.screenshot(
                            path=out / f"failure-{index}-{number}.png",
                            animations="disabled",
                        )
            raise


def open_page(
    browser,
    user="owner",
    narrow=False,
    *,
    state,
    storage=None,
    now=None,
    **context_options,
):
    options = {
        "viewport": evidence.NARROW if narrow else evidence.DESKTOP,
        "color_scheme": "light",
        **context_options,
    }
    context = browser.new_context(**options)
    context.tracing.start(screenshots=True, snapshots=True, sources=True)
    if now is not None:
        context.clock.install(time=now)
    if user is not None:
        evidence.authenticate(context, state, state["users"][user]["token"])
    values = {evidence.MODE_KEY: "light", **(storage or {})}
    context.add_init_script(
        f"for (const [key, value] of Object.entries({json.dumps(values)})) localStorage.setItem(key, value);"
    )
    context.add_init_script(CLIPBOARD_JS)
    page = context.new_page()
    errors = []
    failed_requests = []
    page.diagnostics = {"page_errors": errors, "failed_requests": failed_requests}
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.on(
        "requestfailed",
        lambda request: failed_requests.append(
            {"url": request.url, "failure": request.failure}
        ),
    )
    page.on(
        "response",
        lambda response: (
            failed_requests.append({"url": response.url, "status": response.status})
            if response.status >= 400
            else None
        ),
    )
    return context, page, errors


@contextlib.contextmanager
def expect_rpc(page, method, match):
    """Waits for the response to the `method` request whose JSON body satisfies `match`."""
    with page.expect_response(
        lambda response: (
            response.url.endswith("/" + method)
            and match(response.request.post_data_json or {})
        )
    ) as info:
        yield info


@contextlib.contextmanager
def expect_json(page, path, match):
    """Waits for the response from `path` whose JSON body satisfies `match`."""
    with page.expect_response(
        lambda response: response.url.endswith(path) and match(response.json())
    ) as info:
        yield info


def call(state, method, body, user="owner"):
    return evidence.rpc(state["api_url"], state["users"][user]["token"], method, body)


def sql(state, query):
    return evidence.psql(state, query)


def copied_text(page, timeout=5):
    """Waits for the page's next clipboard write and returns its text."""
    deadline = time.monotonic() + timeout
    while (
        text := page.evaluate(
            "() => { const text = window.__copied; delete window.__copied; return text; }"
        )
    ) is None:
        assert time.monotonic() < deadline, "the page copied nothing"
        page.wait_for_timeout(50)
    return text


def wait_for_query(page, check):
    page.wait_for_url(lambda url: check(parse_qs(urlparse(url).query)))


LINE_STARTS_JS = """node => {
  const lines = [];
  const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT);
  const range = document.createRange();
  while (walker.nextNode()) {
    const text = walker.currentNode;
    for (let i = 0; i < text.length; i++) {
      if (/\\s/.test(text.data[i])) continue;
      range.setStart(text, i);
      range.setEnd(text, i + 1);
      const box = range.getBoundingClientRect();
      const middle = box.top + box.height / 2;
      const line = lines.find(l => Math.abs(l.middle - middle) < 6);
      if (line === undefined) lines.push({ middle, left: box.left, char: text.data[i] });
      else if (box.left < line.left) Object.assign(line, { left: box.left, char: text.data[i] });
    }
  }
  return lines.map(line => line.char);
}"""


def line_starts(locator):
    """The first visible character of every rendered line of the element."""
    return locator.evaluate(LINE_STARTS_JS)


def observe_event_batches(page):
    """Observe complete Connect JSON event messages, forwarding original chunks unchanged."""
    page.add_init_script("""
window.__eventStreamBatches = 0;
window.__eventStreamCursorById = {};
const originalFetch = window.fetch;
window.fetch = async (...args) => {
  const response = await originalFetch(...args);
  const url = args[0] instanceof Request ? args[0].url : String(args[0]);
  if (!url.endsWith('/StreamEvents') || !response.ok || !response.body) return response;
  let pending = new Uint8Array();
  const body = response.body.pipeThrough(new TransformStream({
    transform(chunk, controller) {
      const joined = new Uint8Array(pending.length + chunk.length);
      joined.set(pending);
      joined.set(chunk, pending.length);
      pending = joined;
      while (pending.length >= 5) {
        const size = new DataView(pending.buffer, pending.byteOffset).getUint32(1);
        if (pending.length < 5 + size) break;
        const flags = pending[0];
        if (flags & 1) throw new Error('Expected uncompressed Connect JSON test messages');
        if (!(flags & 2)) {
          const message = JSON.parse(new TextDecoder().decode(pending.subarray(5, 5 + size)));
          window.__eventStreamBatches++;
          for (const event of message.events ?? []) {
            window.__eventStreamCursorById[event.id] = message.cursor;
          }
        }
        pending = pending.subarray(5 + size);
      }
      controller.enqueue(chunk);
    },
  }));
  return new Response(body, {
    status: response.status, statusText: response.statusText, headers: response.headers,
  });
};
""")


def event_stream_input(request):
    """Read the unary request envelope used by the real Connect JSON stream."""
    body = request.post_data_buffer
    assert request.headers["content-type"].startswith("application/connect+json")
    assert body[0] == 0
    assert int.from_bytes(body[1:5], "big") == len(body) - 5
    return json.loads(body[5:])
