import argparse
import json
from pathlib import Path

from playwright.sync_api import expect
from harness import evidence, load_state, open_page, repo, session

parser = argparse.ArgumentParser(
    description="Check install-wide history retention in organization settings"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-retention"))
args = parser.parse_args()
state = load_state(
    args, "settings-retention", "tests/ui/fixtures/settings-labels/scenario.json"
)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
config = evidence.rpc(
    state["api_url"], None, "loco.config.v1.ConfigService/GetConfig", {}
)
# The evidence platform disables image cleanup and uses the default event retention.
assert config["historyRetention"].get("buildImagesPerResource", 0) == 0
assert config["historyRetention"]["events"] == "7776000s"
orgs = evidence.rpc(
    state["api_url"],
    state["users"]["owner"]["token"],
    "loco.org.v1.OrgService/ListUserOrgs",
    {"userId": state["users"]["owner"]["id"]},
)
org_id = next(org["id"] for org in orgs["orgs"] if org["name"] == "acme")
workspace_id = state["workspaces"]["acme/shared"]
path = f"{state['ui_url']}/org/{org_id}/wks/{workspace_id}/settings?scope=org"

with session(out) as browser:
    for mode in ("light", "dark"):
        for narrow in (False, True):
            context, page, errors = open_page(
                browser, state=state, narrow=narrow, storage={evidence.MODE_KEY: mode}
            )
            page.goto(path)
            limits = page.get_by_role("region", name="Limits", exact=True)
            images = limits.get_by_role("row").filter(
                has=page.get_by_role("cell", name="Build images", exact=True)
            )
            events = limits.get_by_role("row").filter(
                has=page.get_by_role(
                    "cell", name="Events and webhook deliveries", exact=True
                )
            )
            expect(images.get_by_role("cell").last).to_have_text("Cleanup disabled")
            expect(events.get_by_role("cell").last).to_have_text("90 days")
            expect(images.get_by_text("Raised", exact=True)).to_have_count(0)
            limits.screenshot(
                path=out / f"retention-after-{mode}{'-narrow' if narrow else ''}.png",
                animations="disabled",
            )
            if narrow:
                scroller = limits.locator('[data-slot="table-container"]')
                events.get_by_role("cell").last.scroll_into_view_if_needed()
                assert scroller.evaluate("node => node.scrollLeft > 0")
                expect(events.get_by_role("cell").last).to_be_in_viewport()
            assert not errors, errors
            context.close()

    # A custom API response exercises fractional days and a non-default image count.
    context, page, errors = open_page(browser, state=state)
    config["historyRetention"] = {"buildImagesPerResource": 7, "events": "129600s"}
    page.route(
        "**/loco.config.v1.ConfigService/GetConfig",
        lambda route: route.fulfill(json=config),
    )
    page.goto(path)
    limits = page.get_by_role("region", name="Limits", exact=True)
    expect(
        limits.get_by_role("row")
        .filter(has=page.get_by_role("cell", name="Build images", exact=True))
        .get_by_role("cell")
        .last
    ).to_have_text("7")
    expect(
        limits.get_by_role("row")
        .filter(
            has=page.get_by_role(
                "cell", name="Events and webhook deliveries", exact=True
            )
        )
        .get_by_role("cell")
        .last
    ).to_have_text("1.5 days")
    assert not errors, errors
    context.close()

    # An older server has no install retention fields; avoid claiming cleanup is disabled.
    context, page, errors = open_page(browser, state=state)
    del config["historyRetention"]
    page.route(
        "**/loco.config.v1.ConfigService/GetConfig",
        lambda route: route.fulfill(json=config),
    )
    page.goto(path)
    limits = page.get_by_role("region", name="Limits", exact=True)
    for label in ("Build images", "Events and webhook deliveries"):
        expect(
            limits.get_by_role("row")
            .filter(has=page.get_by_role("cell", name=label, exact=True))
            .get_by_role("cell")
            .last
        ).to_have_text("—")
    assert not errors, errors
    context.close()
print("Install retention API and dashboard checks passed")
