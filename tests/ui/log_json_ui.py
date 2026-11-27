import argparse
import json
import re
from datetime import UTC, datetime, timedelta
from pathlib import Path

from harness import copied_text, evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check parsed log JSON and its personal preference"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/log-json"))
args = parser.parse_args()
state = load_state(args, "log-json", "tests/ui/fixtures/system/scenario.json")
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
request = '{"entity":null,"path":"/ListOrgs","id":9007199254740993}'
now = datetime.now(UTC)
entries = [
    {
        "timestamp": (now - timedelta(seconds=i + 1)).isoformat(),
        "level": "LOG_LEVEL_INFO",
        "body": body,
        "resourceAttributes": {"service.name": "loco-api"},
        "logAttributes": {"request": value},
    }
    for i, (body, value) in enumerate(
        [
            ("handled request", request),
            ("plain text log", "GET /healthz"),
            ("malformed JSON log", '{"path":'),
        ]
    )
]


def open_log(page, body="handled request"):
    page.get_by_role("region", name="Log lines").get_by_role(
        "row", name=re.compile(body)
    ).click()
    detail = page.get_by_role("dialog")
    detail.get_by_role("button", name="JSON", exact=True).click()
    return detail


with session(out) as browser:
    for theme in ("light", "dark"):
        for narrow in (False, True):
            context, page, errors = open_page(
                browser,
                user="admin",
                state=state,
                narrow=narrow,
                storage={evidence.MODE_KEY: theme},
                color_scheme=theme,
            )
            context.route(
                "**/loco.observability.v1.ObservabilityService/QueryLogs",
                lambda route: route.fulfill(json={"entries": entries}),
            )
            context.route(
                "**/loco.observability.v1.ObservabilityService/QueryLogBuckets",
                lambda route: route.fulfill(json={"buckets": []}),
            )
            page.goto(state["ui_url"] + "/system/logs?live=0")
            detail = open_log(page)
            toggle = detail.get_by_role("button", name="Parsed JSON", exact=True)
            expect(toggle).to_have_attribute("aria-pressed", "false")
            original = detail.locator("[data-slot=code-block] pre").last.inner_text()
            toggle.click()
            expect(toggle).to_have_attribute("aria-pressed", "true")
            expect(detail.locator("[data-slot=code-block] pre").last).to_contain_text(
                '"id": 9007199254740993'
            )
            detail.get_by_role("button", name="Copy parsed JSON", exact=True).click()
            assert (
                json.loads(copied_text(page))["log_attributes"]["request"]["path"]
                == "/ListOrgs"
            )
            page.reload()
            detail = page.get_by_role("dialog")
            expect(toggle).to_have_attribute("aria-pressed", "true")
            settings = context.new_page()
            settings.goto(state["ui_url"] + "/profile")
            preference = settings.get_by_role("switch", name="Parsed JSON", exact=True)
            expect(preference).to_be_checked()
            preference.scroll_into_view_if_needed()
            settings.screenshot(
                path=str(out / f"settings-{theme}{'-narrow' if narrow else ''}.png")
            )
            preference.click()
            expect(toggle).to_have_attribute("aria-pressed", "false")
            expect(detail.locator("[data-slot=code-block] pre").last).to_have_text(
                original
            )
            detail.get_by_role("button", name="Copy JSON", exact=True).click()
            assert copied_text(page) == original
            settings.reload()
            expect(preference).not_to_be_checked()
            toggle.click()
            expect(preference).to_be_checked()
            page.screenshot(
                path=str(out / f"parsed-{theme}{'-narrow' if narrow else ''}.png")
            )
            page.keyboard.press("Escape")
            for body in ("plain text log", "malformed JSON log"):
                detail = open_log(page, body)
                expect(
                    detail.get_by_role("button", name="Parsed JSON", exact=True)
                ).to_have_count(0)
                page.keyboard.press("Escape")
            assert not errors, errors
            context.close()
            print(
                f"PASS: parsed JSON preference {theme} {'phone' if narrow else 'desktop'}",
                flush=True,
            )
