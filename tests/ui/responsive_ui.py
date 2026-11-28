import argparse
import re
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
    if env["slug"] == "production"
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
                    "age": "1 hour",
                    "placements": {"us-ca-1": {"ready_replicas": 1}},
                    "spec": {
                        "observability": {"injectOtel": True},
                        "build": {
                            "type": "image",
                            "image": "registry.example.com/app:1",
                        },
                        "port": 8080,
                        "regions": {
                            "us-ca-1": {
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
        "environmentId": production,
        "values": {"SESSION_KEY": "fixture-session"},
    },
)
base = state["ui_url"] + f"/org/{org}/wks/{workspace}"

TABLES_JS = """() => [...document.querySelectorAll('[data-slot=data-table]')].map(c => ({
  scrolls: c.scrollWidth > c.clientWidth + 1,
  overflowX: getComputedStyle(c).overflowX,
  scroll: c.getAttribute('data-scroll'),
  pinned: c.querySelector('td, th') ? getComputedStyle(c.querySelector('td, th')).position : null,
  endReachable: (() => {
    const last = c.querySelector('th:last-child, [data-slot=table-header] > :last-child');
    c.scrollLeft = c.scrollWidth;
    const reachable = !last || last.getBoundingClientRect().right <= c.getBoundingClientRect().right + 1;
    c.scrollLeft = 0;
    return reachable;
  })(),
}))"""
FIRST_CELL_JS = """() => {
  const c = [...document.querySelectorAll('[data-slot=data-table]')].find(x => x.querySelector('tbody tr'));
  const row = c.querySelector('tbody tr');
  const cell = row.querySelector('td');
  const content = cell.firstElementChild || cell;
  const left = (el) => el.getBoundingClientRect().left;
  let card = c.parentElement;
  while (card && getComputedStyle(card).backgroundColor === 'rgba(0, 0, 0, 0)') card = card.parentElement;
  return {
    rowOffset: left(content) - left(row),
    cellOffset: left(content) - left(cell),
    pinned: getComputedStyle(cell).position === 'sticky',
    cellBg: getComputedStyle(cell).backgroundColor,
    cardBg: card ? getComputedStyle(card).backgroundColor : null,
  };
}"""
HEADER_BORDERS_JS = """() => {
  const header = document.querySelector('[data-slot=table-header]');
  const chain = [header, ...header.querySelectorAll('tr, th')];
  return chain.map(el => {
    const s = getComputedStyle(el);
    return { style: s.borderBottomStyle, width: parseFloat(s.borderBottomWidth) };
  });
}"""
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
        samples = {
            "k8s.pod.cpu.usage": ((50, 0.03), (30, 0.05), (10, 0.04)),
            "k8s.pod.memory.working_set": (
                (50, 48 * 1024 * 1024),
                (30, 80 * 1024 * 1024),
                (10, 64 * 1024 * 1024),
            ),
        }
        points = [
            {
                "timestamp": (now - timedelta(minutes=minute))
                .isoformat()
                .replace("+00:00", "Z"),
                "value": value,
            }
            for minute, value in samples.get(body["metricName"], ())
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
            assert table["pinned"] in ("sticky", None), (label, table)
            assert table["endReachable"], (label, table)


with session(out) as browser:
    for mode in MODES:
        for label, (width, height) in VIEWPORTS.items():
            for name, url in PAGES.items():
                context, page, errors = open_at(browser, width, height, mode)
                page.goto(url)
                page.locator("main, [role=main]").first.wait_for()
                if name == "audit":
                    expect(
                        page.get_by_role("table", name="Audit events")
                    ).to_be_visible()
                expect(page.locator("[data-slot=data-table]").first).to_be_visible()
                tables_stay_inside(page, f"{mode} {label} {name}")
                if label == "390" and name in ("dashboard", "resource", "audit"):
                    table = page.locator("[data-slot=data-table]").first
                    table.scroll_into_view_if_needed()
                    table.screenshot(path=out / f"{mode}-{label}-{name}-start.png")
                    table.evaluate(
                        "el => el.scrollTo((el.scrollWidth - el.clientWidth) / 2, 0)"
                    )
                    expect(table).to_have_attribute("data-scroll", "middle")
                    table.screenshot(path=out / f"{mode}-{label}-{name}-middle.png")
                    table.evaluate("el => el.scrollTo(el.scrollWidth, 0)")
                    expect(table).to_have_attribute("data-scroll", "end")
                context.close()

    for mode, color in (("light", None), ("dark", None), ("light", "neobrutal")):
        context, page, errors = open_at(browser, 390, 844, mode)
        if color is not None:
            context.add_init_script(f"localStorage.setItem('loco:color:v1', '{color}')")
        page.goto(PAGES["dashboard"])
        row = page.locator(
            "[data-slot=data-table][data-scroll] tbody tr[data-interactive]"
        ).first
        row.scroll_into_view_if_needed()
        cell = row.locator("td").first
        idle = cell.evaluate("el => getComputedStyle(el).backgroundImage")
        row.hover()
        page.wait_for_function(
            "([el, idle]) => getComputedStyle(el).backgroundImage !== idle",
            arg=[cell.element_handle(), idle],
            timeout=2000,
        )
        page.locator("[data-slot=data-table][data-scroll]").first.screenshot(
            path=out / f"{color or mode}-{mode}-390-row-hover.png"
        )
        context.close()

    for color in ("loco", "catppuccin", "rose-pine", "solarized", "neobrutal", "paper"):
        for mode in MODES:
            for label, (width, height) in (
                ("390", VIEWPORTS["390"]),
                ("1280", (1280, 800)),
            ):
                for name in ("dashboard", "resources"):
                    context, page, errors = open_at(browser, width, height, mode)
                    context.add_init_script(
                        f"localStorage.setItem('loco:color:v1', '{color}')"
                    )
                    page.goto(PAGES[name])
                    page.locator("[data-slot=data-table] tbody tr").first.wait_for()
                    where = (color, mode, label, name)
                    cell = page.evaluate(FIRST_CELL_JS)
                    assert cell["rowOffset"] >= 10, (where, cell)
                    if cell["pinned"]:
                        assert cell["cellBg"] == cell["cardBg"], (where, cell)
                    context.close()

    for mode in MODES:
        for name in ("dashboard", "resources", "audit"):
            context, page, errors = open_at(browser, 1280, 800, mode)
            context.add_init_script(
                "localStorage.setItem('loco:color:v1', 'solarized')"
            )
            page.goto(PAGES[name])
            page.locator("[data-slot=table-header]").first.wait_for()
            borders = page.evaluate(HEADER_BORDERS_JS)
            drawn = [b for b in borders if b["style"] != "none" and b["width"] > 0]
            assert len(drawn) == 1 and drawn[0]["style"] == "solid", (
                mode,
                name,
                borders,
            )
            context.close()

    context, page, errors = open_at(browser, 390, 844)
    page.goto(PAGES["resources"])
    cell = page.locator("[data-slot=data-table]").get_by_text(NAME, exact=True).first
    cell.hover()
    expect(page.locator("[data-slot=tooltip-content]")).to_have_text(NAME)
    context.close()

    for mode in MODES:
        context, page, errors = open_at(browser, 390, 844, mode)
        page.goto(base + f"/resource/{resource}?env=production&tab=settings")
        variables = page.locator("#set-env")
        variables.get_by_role("button", name="Add", exact=True).click()
        fits = variables.evaluate(
            """section => [section, ...section.querySelectorAll("input")].every(el => {
                const box = el.getBoundingClientRect();
                return box.left >= 0 && box.right <= innerWidth && el.scrollWidth <= el.clientWidth + 1;
            })"""
        )
        assert fits, mode
        page.add_style_tag(content=".savebar { visibility: hidden }")
        variables.screenshot(path=out / f"{mode}-390-variables.png")
        context.close()

    for mode in MODES:
        context, page, errors = open_at(browser, 390, 844, mode, has_touch=True)
        mock_traffic(context)
        page.goto(PAGES["resource"])
        chart = page.locator('[aria-label="CPU: average"]')
        expect(page.get_by_text("0.04 vCPU", exact=True)).to_be_visible()
        expect(chart).to_be_visible()
        chart.scroll_into_view_if_needed()
        page.wait_for_timeout(500)
        box = chart.bounding_box()
        page.touchscreen.tap(box["x"] + box["width"] / 2, box["y"] + box["height"] / 2)
        tooltip = page.locator(".recharts-tooltip-wrapper").first
        expect(tooltip).to_have_css("visibility", "visible")
        expect(tooltip.get_by_text("us-ca-1 0.05 vCPU", exact=True)).to_be_visible()
        page.screenshot(path=out / f"{mode}-390-chart-tooltip.png")
        page.evaluate("window.scrollBy(0, 400)")
        expect(tooltip).to_have_css("visibility", "hidden")
        context.close()

    for mode in MODES:
        context, page, errors = open_at(browser, 1280, 800, mode, **{})
        context.add_init_script("localStorage.setItem('loco:color:v1', 'neobrutal')")
        page.goto(PAGES["resource"])
        page.get_by_role("button", name="More actions", exact=True).click()
        page.get_by_role("menuitem", name="Restart", exact=True).click()
        page.get_by_role("dialog").wait_for()
        page.screenshot(path=out / f"{mode}-neobrutal-dialog.png")
        page.keyboard.press("Escape")
        page.get_by_role("button", name="Events", exact=True).click()
        page.locator("[data-slot=sheet-content]").wait_for()
        page.screenshot(path=out / f"{mode}-neobrutal-sheet.png")
        page.keyboard.press("Escape")
        page.get_by_role("button", name=re.compile(r"^Workspace activity, ")).click()
        close = page.get_by_role(
            "dialog", name="Workspace activity", exact=True
        ).get_by_role("button", name="Close workspace activity", exact=True)
        close.hover()
        tooltip = page.locator("[data-slot=tooltip-content]")
        expect(tooltip).to_be_visible()
        page.screenshot(path=out / f"{mode}-neobrutal-tooltip.png")
        context.close()
print("PASS: responsive")
