import argparse
from datetime import UTC, datetime, timedelta
from pathlib import Path

from harness import call, evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check tables, variable rows, chart tooltips and truncated text at narrow widths"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/responsive"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "responsive", "tests/ui/fixtures/deployment-history/scenario.json"
)
workspace = state["workspaces"]["acme/shared"]
org = state["orgs"]["acme"]["id"]
production = next(
    env["id"]
    for env in call(
        state,
        "loco.environment.v1.EnvironmentService/ListEnvironments",
        {"workspaceId": workspace},
    )["environments"]
    if env["name"] == "production"
)
NAME = "responsive-resource-with-a-deliberately-long-name"
resource = evidence.seed_resources(
    state,
    [
        {
            "workspace": "acme/shared",
            "name": NAME,
            "deployments": [
                {
                    "environment": "production",
                    "status": "running",
                    "placements": {"us-east-1": {"ready_replicas": 1}},
                    "spec": {
                        "observability": {"injectOtel": True},
                        "build": {
                            "type": "image",
                            "image": "registry.example.com/app:1",
                        },
                        "port": 8080,
                        "regions": {
                            "us-east-1": {
                                "cpu": "100m",
                                "memory": "128Mi",
                                "minReplicas": 1,
                                "maxReplicas": 1,
                            }
                        },
                    },
                }
            ],
        }
    ],
)[f"acme/shared/{NAME}"]
call(
    state,
    "loco.secret.v1.SecretService/SetSecrets",
    {
        "resourceId": resource,
        "environmentId": production,
        "values": {"SESSION_KEY": "fixture-session"},
    },
)
base = state["ui_url"] + f"/org/{org}/wks/{workspace}"

TABLES_JS = """() => [...document.querySelectorAll('[data-slot=table-container]')].map(c => ({
  scrolls: c.scrollWidth > c.clientWidth + 1,
  overflowX: getComputedStyle(c).overflowX,
  scroll: c.getAttribute('data-scroll'),
  pinned: getComputedStyle(c.querySelector('td, th')).position,
}))"""
PAGES = {
    "dashboard": base + "?env=production",
    "resources": base + "/resources?env=production",
    "resource": base + f"/resource/{resource}?env=production",
    "audit": f"{state['ui_url']}/org/{org}/audit",
}
VIEWPORTS = {"360": (360, 800), "390": (390, 844), "zoom150": (853, 533)}
MODES = ("light", "dark")


def mock_traffic(context):
    now = datetime.now(UTC)

    def query_metrics(route):
        body = route.request.post_data_json
        points = [
            {
                "timestamp": (now - timedelta(minutes=minute))
                .isoformat()
                .replace("+00:00", "Z"),
                "value": value,
            }
            for minute, value in ((50, 3.0), (30, 5.0), (10, 4.0))
        ]
        route.fulfill(
            json={
                "series": [
                    {"resourceId": rid, "points": points}
                    for rid in body.get("resourceIds") or [resource]
                ]
            }
        )

    context.route(
        "**/loco.observability.v1.ObservabilityService/QueryMetrics", query_metrics
    )


def open_at(browser, width, height, mode="light", **options):
    context, page, errors = open_page(
        browser,
        state=state,
        viewport={"width": width, "height": height},
        color_scheme=mode,
        storage={evidence.MODE_KEY: mode},
        **options,
    )
    return context, page, errors


def tables_stay_inside(page, label):
    assert page.evaluate("document.documentElement.scrollWidth") <= page.evaluate(
        "window.innerWidth"
    ), f"{label}: page scrolls sideways"
    for table in page.evaluate(TABLES_JS):
        assert table["overflowX"] == "auto", (label, table)
        if table["scrolls"]:
            assert table["scroll"] == "start", (label, table)
            assert table["pinned"] == "sticky", (label, table)


with session(out) as browser:
    for mode in MODES:
        for label, (width, height) in VIEWPORTS.items():
            for name, url in PAGES.items():
                context, page, errors = open_at(browser, width, height, mode)
                page.goto(url)
                page.locator("main, [role=main]").first.wait_for()
                if name != "audit":
                    expect(
                        page.locator("[data-slot=table-container]").first
                    ).to_be_visible()
                tables_stay_inside(page, f"{mode} {label} {name}")
                if label == "390" and name in ("dashboard", "resource"):
                    table = page.locator("[data-slot=table-container]").first
                    table.scroll_into_view_if_needed()
                    table.screenshot(path=out / f"{mode}-{label}-{name}-start.png")
                    table.evaluate("el => el.scrollTo(el.scrollWidth / 2, 0)")
                    expect(table).to_have_attribute("data-scroll", "middle")
                    table.screenshot(path=out / f"{mode}-{label}-{name}-middle.png")
                    table.evaluate("el => el.scrollTo(el.scrollWidth, 0)")
                    expect(table).to_have_attribute("data-scroll", "end")
                context.close()

    context, page, errors = open_at(browser, 390, 844)
    page.goto(PAGES["resources"])
    cell = (
        page.locator("[data-slot=table-container]").get_by_text(NAME, exact=True).first
    )
    cell.hover()
    expect(
        page.locator(f'[data-slot=table-container] [title="{NAME}"]').first
    ).to_be_attached()
    context.close()

    for mode in MODES:
        context, page, errors = open_at(browser, 390, 844, mode)
        page.goto(base + f"/resource/{resource}?env=production&tab=settings")
        variables = page.locator("#set-env")
        reveal = variables.get_by_role("button", name="Show SESSION_KEY for 30 seconds")
        value = variables.get_by_label("New value for SESSION_KEY", exact=True)
        value.fill("typed")
        expect(reveal).to_be_enabled()
        reveal_box, value_box = reveal.bounding_box(), value.bounding_box()
        assert reveal_box["y"] < value_box["y"] + value_box["height"], (
            reveal_box,
            value_box,
        )
        assert reveal_box["x"] > value_box["x"] + value_box["width"], (
            reveal_box,
            value_box,
        )
        page.add_style_tag(content=".savebar { visibility: hidden }")
        value.locator("..").screenshot(path=out / f"{mode}-390-variables.png")
        context.close()

    for mode in MODES:
        context, page, errors = open_at(browser, 390, 844, mode, has_touch=True)
        mock_traffic(context)
        page.goto(PAGES["resource"])
        chart = page.locator("[data-slot=chart]").first
        chart.scroll_into_view_if_needed()
        box = chart.bounding_box()
        page.touchscreen.tap(box["x"] + box["width"] / 2, box["y"] + box["height"] / 2)
        tooltip = page.locator(".recharts-tooltip-wrapper").first
        expect(tooltip).to_have_css("visibility", "visible")
        page.screenshot(path=out / f"{mode}-390-chart-tooltip.png")
        page.evaluate("window.scrollBy(0, 400)")
        expect(tooltip).to_have_css("visibility", "hidden")
        context.close()

    for mode in MODES:
        context, page, errors = open_at(browser, 1280, 800, mode, **{})
        context.add_init_script("localStorage.setItem('loco:color:v1', 'neobrutal')")
        page.goto(PAGES["resource"])
        page.get_by_role("button", name="Redeploy", exact=True).click()
        page.get_by_role("dialog").wait_for()
        page.screenshot(path=out / f"{mode}-neobrutal-dialog.png")
        page.keyboard.press("Escape")
        page.get_by_role("button", name="Events", exact=True).click()
        page.locator("[data-slot=sheet-content]").wait_for()
        page.screenshot(path=out / f"{mode}-neobrutal-sheet.png")
        page.keyboard.press("Escape")
        copy = page.locator("[data-slot=tooltip-trigger]").first
        copy.hover()
        tooltip = page.locator("[data-slot=tooltip-content]")
        expect(tooltip).to_be_visible()
        assert tooltip.evaluate("el => getComputedStyle(el).borderTopWidth") == "2px"
        page.screenshot(path=out / f"{mode}-neobrutal-tooltip.png")
        context.close()
print("PASS: responsive")
