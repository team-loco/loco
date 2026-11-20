import argparse
import re
from datetime import UTC, datetime, timedelta

from harness import call, evidence, load_state, open_page, repo, session
from playwright.sync_api import TimeoutError as PlaywrightTimeout, expect

parser = argparse.ArgumentParser(
    description="Check column sizing, resizing, pinning and scrolling of every table"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "tables", "tests/ui/fixtures/settings-labels/scenario.json")
org = state["orgs"]["acme"]["id"]
workspace = state["workspaces"]["acme/shared"]
base = state["ui_url"] + f"/org/{org}/wks/{workspace}"

TOOLTIP = '[data-slot="tooltip-content"]'
LONG_NAME = "checkout-payments-reconciliation-worker-eu"
LONG_IMAGE = "registry.example.test/team/checkout-payments:2026.11.19-build-4812"


def runtime(image, regions):
    return {
        "observability": {"injectOtel": True},
        "build": {"type": "image", "image": image},
        "port": 8080,
        "regions": {
            region: {
                "cpu": "1500m",
                "memory": "3Gi",
                "minReplicas": 2,
                "maxReplicas": 6,
            }
            for region in regions
        },
    }


def seed():
    environments = call(
        state,
        "loco.environment.v1.EnvironmentService/ListEnvironments",
        {"workspaceId": workspace},
    )["environments"]
    if any(env["name"] == "staging" for env in environments):
        return
    staging = call(
        state,
        "loco.environment.v1.EnvironmentService/CreateEnvironment",
        {"workspaceId": workspace, "name": "staging"},
    )["environmentId"]
    call(
        state,
        "loco.environment.v1.EnvironmentService/UpdateEnvironment",
        {"environmentId": staging, "displayName": "Staging EU West"},
    )
    service = {
        "routing": {"port": 8080},
        "regions": {
            "us-east-1": {
                "enabled": True,
                "cpu": "1500m",
                "memory": "3Gi",
                "minReplicas": 2,
                "maxReplicas": 6,
            }
        },
    }
    deployments = [
        {
            "environment": env,
            "status": "running",
            "spec": runtime(LONG_IMAGE, ["us-east-1", "eu-west-1"]),
        }
        for env in ("production", "staging")
    ]
    evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": name,
                "service": service,
                "deployments": deployments,
            }
            for name in (LONG_NAME, "api", "web")
        ],
    )


seed()

# Each cell holds its content or truncates it, unpinned cells never overlap, the pinned end column meets the right edge,
# every head lines up with its cells and its label sits on the same edge as their content, and no fit column
# truncates below its maxSize while the grow column shows all of its text.
LAYOUT_JS = """table => {
  const problems = [];
  const heads = [...table.querySelectorAll('thead th')];
  const scroller = table.closest('[data-slot="data-table"]').getBoundingClientRect();
  for (const end of table.querySelectorAll('[data-pin="end"]'))
    if (Math.abs(end.getBoundingClientRect().right - scroller.right) > 1) problems.push('pinned end column leaves a gap at the edge');
  for (const row of table.querySelectorAll('tbody tr')) {
    const cells = [...row.children];
    if (cells.length !== heads.length) continue;
    cells.forEach((cell, i) => {
      const box = cell.getBoundingClientRect();
      const head = heads[i].getBoundingClientRect();
      if (cell.scrollWidth > cell.clientWidth + 1) problems.push(`cell ${i} overflows: ${cell.textContent}`);
      if (Math.abs(box.left - head.left) > 1 || Math.abs(box.width - head.width) > 1)
        problems.push(`cell ${i} misaligned with its head`);
      const next = cells[i + 1];
      if (next && !cell.dataset.pin && !next.dataset.pin && box.right > next.getBoundingClientRect().left + 1)
        problems.push(`cell ${i} overlaps cell ${i + 1}`);
    });
  }
  const cutIn = (i) => [...table.querySelectorAll(`tbody tr > :nth-child(${i + 1}) .truncate`)];
  const cut = (e) => e.scrollWidth > e.clientWidth + 1;
  const g = heads.findIndex(h => h.dataset.grow !== undefined);
  const growRoom = g >= 0 && cutIn(g).length > 0 && !cutIn(g).some(cut);
  heads.forEach((head, i) => {
    const max = parseFloat(head.style.maxWidth);
    if (i === g || Number.isNaN(max) || head.getBoundingClientRect().width >= max - 1 || !growRoom) return;
    if (cutIn(i).some(cut)) problems.push(`fit column ${head.textContent} truncates while the grow column has room`);
  });
  const first = table.querySelector('tbody tr');
  heads.forEach((head, i) => {
    const label = head.querySelector('button > span, :scope > span');
    const content = first?.children[i]?.firstElementChild;
    if (!label || !content || !label.textContent.trim()) return;
    const a = label.getBoundingClientRect(), b = content.getBoundingClientRect();
    if (Math.abs(head.dataset.align === 'end' ? a.right - b.right : a.left - b.left) > 2)
      problems.push(`head ${label.textContent} and its cells start at different edges: ${a.left}-${a.right} vs ${b.left}-${b.right}`);
  });
  return problems;
}"""


def check_layout(table):
    problems = table.evaluate(LAYOUT_JS)
    assert not problems, problems


def head_width(table, label):
    return table.get_by_role("columnheader", name=label, exact=True).evaluate(
        "node => node.getBoundingClientRect().width"
    )


POINTER_JS = """cell => { cell.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }); const b = cell.getBoundingClientRect();
return getComputedStyle(document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2)).cursor; }"""


def expect_pointer(page, cell):
    """The cell shows the pointer cursor once the table has stopped moving."""
    cursor = None
    for _ in range(50):
        cursor = cell.evaluate(POINTER_JS)
        if cursor == "pointer":
            return
        page.wait_for_timeout(100)
    assert cursor == "pointer", cursor


# The element is on top at its own centre, so a click there reaches it without scrolling.
ON_TOP_JS = """node => { const b = node.getBoundingClientRect();
const c = node.closest('[data-slot="data-table"]').getBoundingClientRect();
const hit = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2);
return b.left >= c.left && b.right <= c.right && node.contains(hit)
  || `${b.left}-${b.right} in ${c.left}-${c.right}, hit ${hit?.outerHTML.slice(0, 120)}`; }"""


def on_top(node):
    """Waits until `node` is on top, then reports where it is if it never gets there."""
    node.evaluate(
        "node => node.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' })"
    )
    try:
        node.page.wait_for_function(
            f"node => ({ON_TOP_JS})(node) === true",
            arg=node.element_handle(),
            timeout=5000,
        )
    except PlaywrightTimeout:
        raise AssertionError(node.evaluate(ON_TOP_JS)) from None


# Scrolled fully right, the last scrolling column ends where the pinned end column starts and every resize handle
# in view, clear of the pinned first column, is the element under its own centre.
SCROLL_END_JS = """table => {
  const scroller = table.closest('[data-slot="data-table"]');
  scroller.scrollLeft = scroller.scrollWidth;
  const problems = [];
  const view = scroller.getBoundingClientRect();
  const start = table.querySelector('thead [data-pin="start"]')?.getBoundingClientRect().right ?? view.left;
  const end = table.querySelector('thead [data-pin="end"]');
  if (end) {
    const before = end.previousElementSibling.getBoundingClientRect();
    if (before.right > end.getBoundingClientRect().left + 1) problems.push(`${end.previousElementSibling.textContent} ends under the pinned column`);
  }
  for (const handle of table.querySelectorAll('[role="separator"]')) {
    const b = handle.getBoundingClientRect();
    const x = b.left + b.width / 2, y = b.top + b.height / 2;
    if (x < start || x > view.right) continue;
    if (document.elementFromPoint(x, y) !== handle) problems.push(`${handle.getAttribute('aria-label')} is covered`);
  }
  return problems;
}"""


def check_scroll_end(table):
    table.evaluate(
        "table => table.scrollIntoView({ block: 'center', behavior: 'instant' })"
    )
    problems = table.evaluate(SCROLL_END_JS)
    assert not problems, problems
    table.evaluate(
        "table => { table.closest('[data-slot=\"data-table\"]').scrollLeft = 0; }"
    )


def check_arrow(page, table, row, menu):
    """Every clickable row has one arrow, hidden until the row is hovered, then uncovered and clear of the row menu."""
    arrows = table.locator('[data-slot="nav-arrow"]')
    expect(arrows).to_have_count(table.locator("tbody tr[data-interactive]").count())
    arrow = row.locator('[data-slot="nav-arrow"]')
    page.mouse.move(0, 0)
    expect(arrow).to_have_css("opacity", "0")
    row.get_by_role("cell").last.hover()
    expect(arrow).to_have_css("opacity", "1")
    assert arrow.evaluate(
        """node => { const b = node.getBoundingClientRect();
        return document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2) === node; }"""
    ), "the arrow is covered"
    if menu is not None:
        a, m = arrow.bounding_box(), menu.bounding_box()
        assert a["x"] + a["width"] <= m["x"], (a, m)
    page.mouse.move(0, 0)


def check_fade(scroller):
    """A table that overflows its card at rest says so with a fade on its right edge."""
    expect(scroller).to_have_attribute("data-scroll", "start")
    assert scroller.evaluate("node => getComputedStyle(node).maskImage") != "none"


def scroller_of(page, table):
    return page.locator('[data-slot="data-table"]').filter(has=table)


def drag_handle(page, table, label, dx):
    """Drags the column's handle `dx` pixels and returns the column's width before the pointer is released."""
    handle = table.get_by_role("separator", name=f"Resize {label}")
    handle.evaluate(
        "node => node.scrollIntoView({ block: 'nearest', inline: 'center', behavior: 'instant' })"
    )
    on_top(handle)
    box = handle.bounding_box()
    x, y = box["x"] + box["width"] / 2, box["y"] + box["height"] / 2
    page.mouse.move(x, y)
    page.mouse.down()
    page.mouse.move(x + dx, y, steps=4)
    during = head_width(table, label)
    page.mouse.up()
    return during


def check_resize(page, table, keyboard, drag, reopen, key="ArrowLeft", url_ids=()):
    """Moves `keyboard` 32px with two `key` presses and widens `drag` by a 40px drag, then checks both widths after `reopen`."""
    before = head_width(table, keyboard)
    handle = table.get_by_role("separator", name=f"Resize {keyboard}")
    handle.focus()
    handle.press(key)
    handle.press(key)
    keyed = head_width(table, keyboard)
    step = 32 if key == "ArrowRight" else -32
    assert abs(keyed - (before + step)) <= 1, (before, keyed)
    before = head_width(table, drag)
    during = drag_handle(page, table, drag, 40)
    assert abs(during - (before + 40)) <= 1, ("mid-drag", before, during)
    dragged = head_width(table, drag)
    assert abs(dragged - (before + 40)) <= 1, (before, dragged)
    check_layout(table)
    for column, width in zip(url_ids, (keyed, dragged), strict=False):
        expect(page).to_have_url(
            re.compile(rf"widths=[^&]*{column}(:|%3A){round(width)}\b"), timeout=1000
        )
    page.reload()
    table = reopen(page)
    reloaded = head_width(table, keyboard), head_width(table, drag)
    assert abs(reloaded[0] - keyed) <= 1 and abs(reloaded[1] - dragged) <= 1, (
        (keyed, dragged),
        reloaded,
        page.url,
    )
    return table


def resources_table(page):
    table = page.get_by_role("table", name="Resources")
    expect(table.get_by_role("row").filter(has_text=LONG_NAME)).to_be_visible()
    return table


def show_all_columns(page, table):
    # A pointer click where the button is, so the test itself never scrolls the table.
    box = table.get_by_role("button", name="Columns", exact=True).bounding_box()
    page.mouse.click(box["x"] + box["width"] / 2, box["y"] + box["height"] / 2)
    expect(page.get_by_role("menuitemcheckbox", name="Created")).to_be_visible()
    for item in page.get_by_role("menuitemcheckbox").all():
        if item.get_attribute("aria-checked") != "true":
            item.click()
    page.keyboard.press("Escape")


def resources(page):
    page.goto(base + "/resources?env=production")
    table = resources_table(page)
    expect(
        table.get_by_role("columnheader", name="Staging EU West", exact=True)
    ).to_be_visible()
    expect(table.get_by_role("checkbox")).to_have_count(0)
    check_layout(table)
    name_width = head_width(table, "Resource")
    assert 140 <= name_width <= 360, name_width
    long_link = table.get_by_role("link", name=LONG_NAME, exact=True)
    if long_link.evaluate("node => node.scrollWidth > node.clientWidth"):
        long_link.hover()
        expect(page.locator(TOOLTIP)).to_have_text(LONG_NAME)
    table.get_by_role("link", name="api", exact=True).hover()
    expect(page.locator(TOOLTIP)).to_have_count(0)

    row = table.get_by_role("row").filter(has_text="api").first
    expect_pointer(page, row.get_by_role("cell").nth(3))
    check_arrow(page, table, row, row.get_by_role("button", name="Actions for api"))
    table = check_resize(page, table, "Resource", "Size", resources_table)

    show_all_columns(page, table)
    expect(table.get_by_role("columnheader", name="Image", exact=True)).to_be_visible()
    check_layout(table)
    scroller = scroller_of(page, table)
    assert scroller.evaluate("node => node.scrollWidth > node.clientWidth")
    assert scroller.evaluate("node => node.scrollLeft") == 0
    assert scroller.evaluate("node => node.offsetHeight - node.clientHeight") > 0
    check_scroll_end(table)
    columns = table.get_by_role("button", name="Columns", exact=True)
    row = table.get_by_role("row").filter(has_text="api").first
    actions = row.get_by_role("button", name="Actions for api", exact=True)
    on_top(columns)
    on_top(actions)

    cell_box = row.get_by_role("cell").nth(2).bounding_box()
    page.mouse.move(cell_box["x"] + 10, cell_box["y"] + 10)
    page.mouse.wheel(300, 0)
    page.wait_for_function("node => node.scrollLeft > 0", arg=scroller.element_handle())
    check_layout(table)
    on_top(columns)
    on_top(actions)
    page.mouse.move(0, 0)
    columns.click()
    expect(page.get_by_role("menuitemcheckbox", name="Image")).to_be_visible()
    page.keyboard.press("Escape")
    expect(page.get_by_role("menu")).to_be_hidden()

    row.get_by_role("cell").nth(4).click()
    expect(page).to_have_url(re.compile(r"/resource/"))


def dashboard_table(page):
    table = page.get_by_role("table", name="Resources")
    expect(table.get_by_role("row").filter(has_text=LONG_NAME)).to_be_visible()
    return table


def dashboard(page):
    page.goto(base + "?env=production")
    table = dashboard_table(page)
    check_layout(table)
    version = table.get_by_text("2026.11.19-build-4812", exact=True).first
    assert version.evaluate("node => node.scrollWidth <= node.clientWidth")
    row = table.get_by_role("row").filter(has_text="api").first
    expect_pointer(page, row.get_by_role("cell").nth(2))
    check_arrow(
        page, table, row, row.get_by_role("button", name=re.compile("^Actions for"))
    )
    check_resize(
        page, table, "Traffic, last hour", "Region", dashboard_table, "ArrowRight"
    )
    table = dashboard_table(page)
    table.get_by_role("row").filter(has_text="api").first.get_by_role("cell").nth(
        3
    ).click()
    expect(page).to_have_url(re.compile(r"/resource/"))


# Playwright hides scrollbars by default; these checks need the table's own.
def stamp(delta):
    return (datetime.now(UTC) - delta).isoformat().replace("+00:00", "Z")


def mock_telemetry(page):
    entries = [
        {
            "timestamp": stamp(timedelta(seconds=i * 7 + 1)),
            "level": "LOG_LEVEL_INFO",
            "body": f"Served checkout request {i} " + "with a long message " * 6,
            "resourceAttributes": {
                "service.name": LONG_NAME,
                "cloud.region": "eu-west-1",
            },
            "logAttributes": {
                "http.request.method": "GET",
                "url.path": "/api/checkout/payments/reconciliation/" + "x" * 40,
                "http.response.status_code": "200",
                "duration_ms": "12",
            },
        }
        for i in range(6)
    ]
    page.route(
        "**/loco.observability.v1.ObservabilityService/QueryLogs",
        lambda route: route.fulfill(
            json={"entries": entries, "totalMatched": str(len(entries))}
        ),
    )
    summaries = [
        {
            "traceId": f"{i + 1:032x}",
            "rootSpanName": f"GET /api/checkout/payments/reconciliation/{i}",
            "serviceName": LONG_NAME,
            "startTime": stamp(timedelta(seconds=i * 30 + 5)),
            "durationNanos": "427600000",
            "status": "TRACE_STATUS_OK",
            "spanCount": "12",
        }
        for i in range(6)
    ]
    page.route(
        "**/loco.observability.v1.ObservabilityService/ListTraces",
        lambda route: route.fulfill(
            json={"totalCount": str(len(summaries)), "summaries": summaries}
        ),
    )


LOG_COLUMNS = "level,resource,replica,region,method,path,status,duration,trace"


def logs_table(page):
    table = page.get_by_role("table", name="Log lines")
    expect(
        table.get_by_role("row", name=re.compile("Served checkout request 0"))
    ).to_be_visible()
    return table


def fits_at_16px(page, table):
    """At the default 16px text size the table needs no sideways scroll."""
    if page.evaluate("getComputedStyle(document.documentElement).fontSize") != "16px":
        return
    assert scroller_of(page, table).evaluate(
        "node => node.scrollWidth <= node.clientWidth"
    )


def logs(page):
    mock_telemetry(page)
    page.goto(base + "/observability?env=production&view=logs")
    fits_at_16px(page, logs_table(page))
    page.goto(base + f"/observability?env=production&view=logs&cols={LOG_COLUMNS}")
    table = logs_table(page)
    check_layout(table)
    scroller = scroller_of(page, table)
    assert scroller.evaluate("node => node.scrollWidth > node.clientWidth")
    on_top(table.get_by_role("button", name="Add a column", exact=True))
    check_scroll_end(table)
    row = table.get_by_role("row", name=re.compile("Served checkout request 1"))
    expect_pointer(page, row.get_by_role("cell").nth(1))
    expect(table.locator('[data-slot="nav-arrow"]')).to_have_count(0)
    table = check_resize(
        page, table, "Time", "Resource", logs_table, "ArrowRight", ("time", "resource")
    )
    cell = table.get_by_role("cell").nth(3)
    cell.evaluate(
        "node => node.scrollIntoView({ block: 'center', behavior: 'instant' })"
    )
    box = cell.bounding_box()
    point = [box["x"] + 5, box["y"] + 5]
    # The wheel goes to whatever is on top, so wait until the table is, past any loading overlay.
    scroller.evaluate(
        """(node, [x, y]) => new Promise(resolve => {
        const check = () => node.contains(document.elementFromPoint(x, y)) ? resolve() : requestAnimationFrame(check);
        check(); })""",
        point,
    )
    page.mouse.move(*point)
    page.mouse.wheel(300, 0)
    try:
        page.wait_for_function(
            "node => node.scrollLeft > 0", arg=scroller.element_handle(), timeout=5000
        )
    except PlaywrightTimeout:
        raise AssertionError(
            scroller.evaluate(
                """(node, [x, y]) => [node.scrollLeft, node.scrollWidth, node.clientWidth,
                document.elementFromPoint(x, y)?.outerHTML.slice(0, 120)]""",
                point,
            )
        ) from None
    check_layout(table)
    table.get_by_role("row", name=re.compile("Served checkout request 1")).get_by_role(
        "cell"
    ).nth(2).click()
    expect(page.get_by_role("dialog")).to_contain_text("Served checkout request 1")


def traces_table(page):
    table = page.get_by_role("table", name="Traces")
    expect(table.get_by_role("row", name=re.compile("^Open trace "))).to_have_count(6)
    return table


def traces(page):
    mock_telemetry(page)
    page.goto(base + "/observability?env=production&view=traces")
    table = traces_table(page)
    check_layout(table)
    fits_at_16px(page, table)
    row = table.get_by_role(
        "row", name="Open trace GET /api/checkout/payments/reconciliation/1"
    )
    expect_pointer(page, row.get_by_role("cell").nth(4))
    expect(table.locator('[data-slot="nav-arrow"]')).to_have_count(0)
    table = check_resize(
        page, table, "Time", "Root span", traces_table, url_ids=("when", "span")
    )
    table.get_by_role(
        "row", name="Open trace GET /api/checkout/payments/reconciliation/1"
    ).press("Enter")
    expect(page).to_have_url(re.compile(f"trace={1 + 1:032x}"))


with session(
    repo / "tests/ui/out/tables", ignore_default_args=["--hide-scrollbars"]
) as browser:
    for root in ("16px", "20px"):
        context, page, errors = open_page(browser, state=state)
        context.add_init_script(
            f"document.addEventListener('DOMContentLoaded', () => {{ document.documentElement.style.fontSize = '{root}'; }})"
        )
        for check in (resources, dashboard, logs, traces):
            check(page)
        assert not errors, errors
        context.close()

    context, page, errors = open_page(browser, narrow=True, state=state)
    page.goto(base + "/resources?env=production")
    table = resources_table(page)
    check_layout(table)
    scroller = scroller_of(page, table)
    name_cell = (
        table.get_by_role("row").filter(has_text="api").first.get_by_role("cell")
    )
    check_fade(scroller)
    half = scroller.evaluate("node => node.clientWidth / 2")
    assert head_width(table, "Resource") <= half + 1, half
    left = name_cell.first.evaluate("node => node.getBoundingClientRect().left")
    scroller.evaluate("node => { node.scrollLeft = 200; }")
    page.wait_for_function("node => node.scrollLeft > 0", arg=scroller.element_handle())
    moved = name_cell.first.evaluate("node => node.getBoundingClientRect().left")
    assert abs(moved - left) <= 1, (left, moved)
    for path, reopen in (
        (f"/observability?env=production&view=logs&cols={LOG_COLUMNS}", logs_table),
        ("/observability?env=production&view=traces", traces_table),
    ):
        mock_telemetry(page)
        page.goto(base + path)
        table = reopen(page)
        check_layout(table)
        check_fade(scroller_of(page, table))
    assert not errors, errors
    context.close()
print(
    "PASS: tables size columns to fit, resize by drag and keyboard, keep widths after reload, pin columns and scroll sideways"
)
