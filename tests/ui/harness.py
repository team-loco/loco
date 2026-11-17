import argparse
import atexit
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import sync_playwright

repo = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)


def load_state(args, name, scenario):
    os.chdir(repo)
    if args.state is None:
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            evidence.cmd_up(argparse.Namespace(ref="HEAD", name=name, scenario=str(repo / scenario)))
        args.state = output.getvalue().strip().splitlines()[-1]
        print("Preview state:", args.state, flush=True)
        if not getattr(args, "keep", False):
            atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
    return evidence.load_state(args.state)


def authenticate(context, token, storage=None):
    values = {evidence.MODE_KEY: "light", **(storage or {})}
    if token is not None:
        values[evidence.AUTH_KEY] = json.dumps({
            "idToken": token, "refreshToken": None, "expiresAt": evidence.SESSION_EXPIRY_MS,
        })
    context.add_init_script(
        f"for (const [key, value] of Object.entries({json.dumps(values)})) localStorage.setItem(key, value);"
    )


@contextlib.contextmanager
def session(out):
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch()
        try:
            yield browser
        except BaseException:
            out.mkdir(parents=True, exist_ok=True)
            for index, context in enumerate(browser.contexts):
                with contextlib.suppress(Exception):
                    context.tracing.stop(path=out / f"failure-{index}.zip")
                for number, page in enumerate(context.pages):
                    with contextlib.suppress(Exception):
                        evidence.write_json(out / f"failure-{index}-{number}.json", {"url": page.url, **getattr(page, "diagnostics", {})})
                        page.screenshot(path=out / f"failure-{index}-{number}.png", animations="disabled")
            raise


def open_page(browser, user="owner", narrow=False, *, state, storage=None, **context_options):
    options = {"viewport": evidence.NARROW if narrow else evidence.DESKTOP, "color_scheme": "light", **context_options}
    context = browser.new_context(**options)
    context.tracing.start(screenshots=True, snapshots=True, sources=True)
    authenticate(context, None if user is None else state["users"][user]["token"], storage)
    page = context.new_page()
    errors = []
    failed_requests = []
    page.diagnostics = {"page_errors": errors, "failed_requests": failed_requests}
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.on("requestfailed", lambda request: failed_requests.append({"url": request.url, "failure": request.failure}))
    page.on("response", lambda response: failed_requests.append({"url": response.url, "status": response.status}) if response.status >= 400 else None)
    return context, page, errors


def rpc(state, service, method, body):
    return evidence.rpc(
        state["api_url"], state["users"]["owner"]["token"],
        f"loco.{service}.v1.{service.title()}Service/{method}", body,
    )


def call(state, method, body):
    return evidence.rpc(state["api_url"], state["users"]["owner"]["token"], method, body)


def wait_for_query(page, check):
    page.wait_for_url(lambda url: check(parse_qs(urlparse(url).query)))
