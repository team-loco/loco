import argparse
import re
import struct
import zlib
from datetime import UTC, datetime, timedelta

from harness import (
    call,
    evidence,
    expect_rpc,
    load_state,
    open_page,
    repo,
    session,
    sql,
    wait_for_query,
)
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
DIGEST = "sha256:" + "a" * 64


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
    if any(env["slug"] == "staging" for env in environments):
        return
    call(
        state,
        "loco.environment.v1.EnvironmentService/CreateEnvironment",
        {"workspaceId": workspace, "name": "Staging EU West", "slug": "staging"},
    )
    service = {
        "routing": {"port": 8080},
        "regions": {
            "us-ca-1": {
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
            "spec": runtime(LONG_IMAGE, ["us-ca-1", "us-va-1"]),
        }
        for env in ("production", "staging")
    ]
    ids = evidence.seed_resources(
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
    production = next(e["id"] for e in environments if e["slug"] == "production")
    owner = state["users"]["owner"]["id"]
    sql(
        state,
        f"UPDATE resources SET description='Public API' WHERE id='{ids['acme/shared/api']}';",
    )
    sql(
        state,
        "INSERT INTO builds (method, origin_environment_id, platform, id, resource_id, status, source_type, source_key,"
        " source_size, dockerfile_path, context, image_repository, image_digest, message, created_by, created_at,"
        " started_at, finished_at) VALUES"
        + ",".join(
            f"('cli', '{production}', 'linux/amd64', gen_random_uuid(), '{ids['acme/shared/api']}', 'succeeded', 'upload',"
            f" 'api/{i}', 1024, 'Dockerfile', '.', 'registry.example.test/team/checkout-payments', '{DIGEST}', '', '{owner}',"
            f" NOW() - INTERVAL '{i * 10 + 5} minutes', NOW() - INTERVAL '{i * 10 + 4} minutes',"
            f" NOW() - INTERVAL '{i * 10 + 2} minutes')"
            for i in range(3)
        ),
    )
    long_id = ids[f"acme/shared/{LONG_NAME}"]
    sql(
        state,
        "INSERT INTO events(type, org_id, workspace_id, resource_id, environment_id, actor_type, actor_id, subject_type,"
        " request_id, data, created_at) VALUES"
        + ",".join(
            f"('resource.degraded', '{org}', '{workspace}', '{long_id}', '{production}', 'user', '{owner}', 'resource',"
            f" 'req-tables-{i}', jsonb_build_object('region', 'us-ca-1', 'environmentId', '{production}',"
            f" 'reason', 'restarting', 'message', 'Back-off restarting app ' || repeat('layer-', 30)),"
            f" NOW() - INTERVAL '{i + 1} minutes')"
            for i in range(3)
        ),
    )
    call(
        state,
        "loco.token.v1.TokenService/CreateToken",
        {
            "name": "ci-deploy-checkout-payments-reconciliation",
            "entityType": "ENTITY_TYPE_ORGANIZATION",
            "entityId": org,
            "expiresInSec": "3600",
            "scopes": [
                {
                    "entityType": "ENTITY_TYPE_ORGANIZATION",
                    "entityId": org,
                    "scope": "SCOPE_READ",
                }
            ],
        },
    )
    call(
        state,
        "loco.webhook.v1.WebhookService/CreateWebhook",
        {
            "workspaceId": workspace,
            "url": "https://hooks.invalid/checkout/payments/reconciliation",
            "eventTypes": ["resource.created", "deployment.succeeded"],
        },
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
  const first = table.querySelector('tbody tr:not([data-group])');
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
    """Drags the column's handle `dx` pixels toward widening it and returns the column's width before the pointer is released."""
    handle = table.get_by_role("separator", name=f"Resize {label}")
    handle.evaluate(
        "node => node.scrollIntoView({ block: 'nearest', inline: 'center', behavior: 'instant' })"
    )
    on_top(handle)
    # hover waits until the handle holds still and receives the pointer.
    handle.hover()
    box = handle.bounding_box()
    x, y = box["x"] + box["width"] / 2, box["y"] + box["height"] / 2
    head = table.get_by_role("columnheader", name=label, exact=True).bounding_box()
    if x < head["x"] + head["width"] / 2:
        # The last column's handle sits on its left edge, so dragging left widens it.
        dx = -dx
    start = head_width(table, label)
    page.mouse.move(x, y)
    page.mouse.down()
    page.mouse.move(x + dx, y, steps=4)
    # React renders the pointermove preview in a later task; wait for it before measuring.
    page.wait_for_function(
        "([node, start]) => node.getBoundingClientRect().width !== start",
        arg=[
            table.get_by_role("columnheader", name=label, exact=True).element_handle(),
            start,
        ],
        timeout=5000,
    )
    during = head_width(table, label)
    page.mouse.up()
    return during


def check_shrink(page, table, label):
    """Dragging the column that takes the spare width 120px left narrows it by 120px."""
    before = head_width(table, label)
    drag_handle(page, table, label, -120)
    after = head_width(table, label)
    assert abs(after - (before - 120)) <= 1, (before, after)
    check_layout(table)


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


def pixel(page, x, y, width=1, height=1):
    return page.screenshot(clip={"x": x, "y": y, "width": width, "height": height})


def rgb(page, x, y):
    """The colour of one screen pixel, read from the IDAT chunk of its 1x1 PNG."""
    png, data, i = pixel(page, x, y), b"", 8
    while i < len(png):
        size, kind = struct.unpack(">I4s", png[i : i + 8])
        if kind == b"IDAT":
            data += png[i + 8 : i + 8 + size]
        i += 12 + size
    return zlib.decompress(data)[1:4]


TRANSITIONS = "document.getAnimations().filter(a => a instanceof CSSTransition)"

# Counts the frames in which the node's box moved, on `node.moves`.
MOVES_JS = """node => { node.moves = 0; let last = '';
const tick = () => { const b = node.getBoundingClientRect(), now = `${b.x},${b.y}`;
if (now !== last) { node.moves++; last = now; } node.watch = requestAnimationFrame(tick); }; tick(); }"""


def check_row_states(page, row):
    """The pinned first cell takes the row's hover tint as it fades in and its keyboard focus ring, like the next cell."""

    # Late content such as an error panel can move the table for a frame, so a read counts only if the row held still through it.
    def steady(read):
        for _ in range(20):
            moves = row.evaluate("node => node.moves")
            first, second = (
                row.locator(":scope > td").nth(i).bounding_box() for i in range(2)
            )
            value = read(first, second)
            if row.evaluate("node => node.moves") == moves:
                return value
        raise AssertionError("the row kept moving")

    def bottoms(first, second):
        y = first["y"] + first["height"] - 3
        return rgb(page, first["x"] + 3, y), rgb(page, second["x"] + 3, y)

    def left_edge(first, _):
        return pixel(page, first["x"], first["y"] + first["height"] / 2, 3, 1)

    def tops(first, second):
        return (
            pixel(page, first["x"] + 20, first["y"], 1, 3),
            pixel(page, second["x"] + 20, second["y"], 1, 3),
        )

    row.evaluate(
        "node => node.scrollIntoView({ block: 'center', behavior: 'instant' })"
    )
    row.evaluate(MOVES_JS)
    page.mouse.move(0, 0)
    # In the first cell's corner, clear of any cell text that opens a tooltip.
    row.hover(position={"x": 3, "y": 3})
    page.evaluate(
        f"() => {TRANSITIONS}.forEach(a => {{ a.pause(); a.currentTime = 40; }})"
    )
    pinned, plain = steady(bottoms)
    assert max(abs(a - b) for a, b in zip(pinned, plain, strict=True)) <= 1, (
        "pinned cell misses the hover tint",
        pinned,
        plain,
    )
    page.evaluate(f"() => {TRANSITIONS}.forEach(a => a.finish())")
    page.mouse.move(0, 0)
    page.wait_for_function(f"() => {TRANSITIONS}.length === 0", timeout=5000)
    idle = steady(left_edge)
    page.keyboard.press("Shift")
    row.focus()
    assert row.evaluate("node => node.matches(':focus-visible')")
    assert steady(left_edge) != idle, "focus ring misses the row's left edge"
    pinned_top, plain_top = steady(tops)
    assert pinned_top == plain_top, "focus ring breaks at the pinned cell"
    row.blur()
    row.evaluate("node => cancelAnimationFrame(node.watch)")


def check_band(page, scroller, row):
    """While the table scrolls sideways, a hovered or selected row is one rounded band: its corners show the card at both pinned ends, and the selected outline runs unbroken across the row."""
    scroller.evaluate(
        "node => { node.scrollLeft = (node.scrollWidth - node.clientWidth) / 2; }"
    )
    # In the last cell's corner, clear of the row menu button whose tooltip covers the band's corner.
    row.get_by_role("cell").last.hover(position={"x": 3, "y": 3})
    page.evaluate(f"() => {TRANSITIONS}.forEach(a => a.finish())")
    cells = row.locator(":scope > td")
    first, last = cells.first.bounding_box(), cells.last.bounding_box()
    # The row's edges snap to whole pixels.
    top, bottom = round(first["y"]), round(first["y"] + first["height"]) - 1
    start, end = round(first["x"]), round(last["x"] + last["width"]) - 1
    card = rgb(page, start + 4, top - 1)

    def rounded(state):
        for x in (start, end):
            for y in (top, bottom):
                assert rgb(page, x, y) == card, (f"the {state} band is square", x, y)

    assert rgb(page, start + 4, top + 1) != card, "the hovered row shows no tint"
    rounded("hovered")
    middle = (first["x"] + first["width"] + last["x"]) / 2
    assert rgb(page, end - 5, top + 5) == rgb(page, middle, top + 5), (
        "the pinned action cell has a different hover surface"
    )
    page.mouse.move(0, 0)
    # Marks the row selected without opening its drawer.
    row.evaluate("node => node.setAttribute('data-state', 'selected')")
    page.evaluate(f"() => {TRANSITIONS}.forEach(a => a.finish())")
    rounded("selected")
    middle = (first["x"] + first["width"] + last["x"]) / 2
    for y, inside in ((top, top + 3), (bottom, bottom - 3)):
        line = rgb(page, middle, y)
        assert line != rgb(page, middle, inside), ("no outline in the middle", y)
        assert line == rgb(page, start + 10, y) == rgb(page, end - 10, y), (
            "the outline breaks at a pinned cell",
            y,
        )
    row.evaluate("node => node.removeAttribute('data-state')")
    scroller.evaluate("node => { node.scrollLeft = 0; }")


def production_id():
    return sql(
        state,
        f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND slug='production';",
    )


def resources_table(page):
    table = page.get_by_role("table", name="Resources")
    expect(table.get_by_role("row").filter(has_text=LONG_NAME)).to_be_visible()
    return table


def show_all_columns(page, table):
    button = table.get_by_role("button", name="Add a column", exact=True)
    created = page.get_by_role("menuitemcheckbox", name="Created")
    # Focus without scrolling and open with Enter, so the test itself never moves the table.
    button.evaluate("node => node.focus({ preventScroll: true })")
    page.keyboard.press("Enter")
    expect(created).to_be_visible()
    for item in page.get_by_role("menuitemcheckbox").all():
        if item.get_attribute("aria-checked") != "true":
            item.click()
    page.keyboard.press("Escape")


def resources(page):
    # The default sort is the first environment's column: its status, read on the server.
    with expect_rpc(
        page,
        "ListWorkspaceResources",
        lambda body: (
            body.get("orderBy") == "RESOURCE_ORDER_STATUS"
            and body.get("sortEnvironmentId") == production_id()
            and "environmentId" not in body
            and body.get("pageSize") == 25
        ),
    ):
        page.goto(base + "/resources?env=production")
    table = resources_table(page)
    expect(
        table.get_by_role("columnheader", name="Staging EU West", exact=True)
    ).to_be_visible()
    expect(table.get_by_role("checkbox")).to_have_count(0)
    expect(
        table.get_by_role("columnheader", name="Production", exact=True)
    ).to_have_attribute("aria-sort", "ascending")
    color = "node => getComputedStyle(node).color"
    assert table.locator("th[aria-sort]").evaluate(color) != table.get_by_role(
        "columnheader", name="Size", exact=True
    ).evaluate(color), "the sorted column looks like the others"
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
    check_row_states(page, row)
    check_arrow(page, table, row, row.get_by_role("button", name="Actions for api"))
    table = check_resize(page, table, "Resource", "Size", resources_table)

    show_all_columns(page, table)
    expect(table.get_by_role("columnheader", name="Image", exact=True)).to_be_visible()
    created = table.get_by_role("row").filter(has_text="api").first.get_by_role("cell")
    expect(created.get_by_text("Olivia Owner", exact=True)).to_be_visible()
    check_layout(table)
    scroller = scroller_of(page, table)
    assert scroller.evaluate("node => node.scrollWidth > node.clientWidth")
    assert scroller.evaluate("node => node.scrollLeft") == 0
    assert scroller.evaluate("node => node.offsetHeight - node.clientHeight") > 0
    check_scroll_end(table)
    columns = table.get_by_role("button", name="Add a column", exact=True)
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
    cost = page.get_by_role("menuitemcheckbox", name="Est. cost", exact=True)
    expect(cost).to_have_attribute("aria-checked", "true")
    tick = cost.locator(
        '[data-slot="dropdown-menu-checkbox-item-indicator"]'
    ).bounding_box()
    label = cost.get_by_text("Est. cost", exact=True).bounding_box()
    assert tick["x"] + tick["width"] <= label["x"], (tick, label)
    page.keyboard.press("Escape")
    expect(page.get_by_role("menu")).to_be_hidden()

    row.get_by_role("cell").nth(4).click()
    expect(page).to_have_url(re.compile(r"/resource/"))


def resources_map(page):
    # The map dims what the server's search leaves out.
    with expect_rpc(
        page,
        "ListWorkspaceResources",
        lambda body: body.get("query") == "api" and body.get("pageSize") == 200,
    ):
        page.goto(base + "/resources?env=production&view=map&q=api")
    expect(
        page.get_by_role("group", name="Filter by status", exact=True)
    ).to_be_visible()
    expect(page.get_by_role("textbox", name="Search resources")).to_have_value("api")
    api = page.get_by_role("button", name=re.compile(r"^api\b"))
    expect(page.get_by_role("button", name=re.compile(r"^web\b"))).to_have_css(
        "opacity", "0.3"
    )
    expect(api).to_have_css("opacity", "1")
    api.click()
    panel = page.get_by_role("dialog", name="api", exact=True)
    expect(panel.get_by_text("Public API", exact=True)).to_be_visible()
    page.keyboard.press("Escape")
    expect(panel).to_be_hidden()


def dashboard_table(page):
    table = page.get_by_role("table", name="Resources")
    expect(table.get_by_role("row").filter(has_text=LONG_NAME)).to_be_visible()
    return table


def dashboard(page):
    with expect_rpc(
        page,
        "ListWorkspaceResources",
        lambda body: (
            body.get("environmentScoped") is True
            and body.get("environmentId") == production_id()
            and body.get("orderBy") == "RESOURCE_ORDER_STATUS"
            and body.get("pageSize") == 25
        ),
    ):
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


def audit_table(page):
    table = page.get_by_role("table", name="Audit events")
    expect(table.locator("tbody tr[data-interactive]").first).to_be_visible()
    return table


def audit(page):
    page.goto(state["ui_url"] + f"/org/{org}/audit")
    table = audit_table(page)
    expect(table.locator("tbody tr[data-group]").first).to_have_text("Today")
    check_layout(table)
    fits_at_16px(page, table)
    row = table.locator("tbody tr[data-interactive]").first
    expect_pointer(page, row.get_by_role("cell").nth(2))
    expect(table.locator('[data-slot="nav-arrow"]')).to_have_count(0)
    table = check_resize(page, table, "Time", "Where", audit_table, "ArrowRight")
    table.locator("tbody tr[data-interactive]").first.get_by_role("cell").nth(1).click()
    expect(page.get_by_role("dialog")).to_be_visible()


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
                "cloud.region": "us-va-1",
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
        lambda route: route.fulfill(json={"entries": entries}),
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


def shows_whole(page, text):
    """At the default 16px text size the text is not truncated."""
    if page.evaluate("getComputedStyle(document.documentElement).fontSize") != "16px":
        return
    assert text.evaluate("node => node.scrollWidth <= node.clientWidth"), text


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
    check_row_states(page, row)
    check_band(page, scroller, row)
    table = check_resize(
        page, table, "Time", "Resource", logs_table, "ArrowRight", ("time", "resource")
    )
    check_shrink(page, table, "Message")
    region = table.get_by_role("columnheader", name="Region", exact=True)
    resource = table.get_by_role("columnheader", name="Resource", exact=True)
    expect(region).to_have_attribute("draggable", "true")
    expect(
        table.get_by_role("columnheader", name="Message", exact=True)
    ).to_have_attribute("draggable", "false")
    # Headless Chromium starts no native drag from synthetic mouse input, so the drag events are sent directly.
    transfer = page.evaluate_handle("() => new DataTransfer()")
    region.dispatch_event("dragstart", {"dataTransfer": transfer})
    resource.dispatch_event("dragover", {"dataTransfer": transfer})
    resource.dispatch_event("drop", {"dataTransfer": transfer})
    region.dispatch_event("dragend", {"dataTransfer": transfer})
    wait_for_query(
        page,
        lambda query: query.get("cols", [""])[0].startswith("level,region,resource"),
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
    check_row_states(page, row)
    # Closing the drawer opened by a click returns focus to the row without a focus ring.
    row.get_by_role("cell").nth(2).click()
    dialog = page.get_by_role("dialog")
    dialog.get_by_role("button", name="Close").click()
    expect(dialog).to_be_hidden()
    expect(row).to_be_focused()
    assert not row.evaluate("node => node.matches(':focus-visible')")
    table = check_resize(
        page, table, "Time", "Root span", traces_table, url_ids=("when", "span")
    )
    check_shrink(page, table, "Root span")
    table.get_by_role(
        "row", name="Open trace GET /api/checkout/payments/reconciliation/1"
    ).press("Enter")
    expect(page).to_have_url(re.compile(f"trace={1 + 1:032x}"))


def events_table(page):
    table = page.get_by_role("table", name="Events")
    expect(table.get_by_role("row", name="Degraded in us-ca-1")).to_have_count(3)
    return table


def events(page):
    page.goto(base + "/observability?env=production&view=events&live=0")
    table = events_table(page)
    check_layout(table)
    fits_at_16px(page, table)
    row = table.get_by_role("row", name="Degraded in us-ca-1").first
    expect_pointer(page, row.get_by_role("cell").nth(2))
    expect(table.locator('[data-slot="nav-arrow"]')).to_have_count(0)
    table = check_resize(page, table, "Time", "Resource", events_table, "ArrowRight")
    table.get_by_role("row", name="Degraded in us-ca-1").first.get_by_role("cell").nth(
        2
    ).click()
    expect(
        page.get_by_role("dialog", name="Degraded in us-ca-1", exact=True)
    ).to_be_visible()
    page.keyboard.press("Escape")


# Gateway traffic per bucket: 2 requests/s with 0.5 of them 5xx, and 120 ms at p95.
TRAFFIC_VALUES = {
    "cluster.upstream_rq_completed": 2,
    "cluster.upstream_rq_xx": 0.5,
    "cluster.upstream_rq_time": 120,
}


def mock_metrics(page):
    # Every metric shares bucket times, as the API's buckets do.
    now = datetime.now(UTC).replace(second=0, microsecond=0)

    def metrics(route):
        body = route.request.post_data_json
        value = TRAFFIC_VALUES.get(body.get("metricName"), 0.4)
        route.fulfill(
            json={
                "series": [
                    {
                        "resourceId": rid,
                        "points": [
                            {
                                "timestamp": (now - timedelta(minutes=m))
                                .isoformat()
                                .replace("+00:00", "Z"),
                                "value": value,
                            }
                            for m in (30, 10)
                        ],
                    }
                    for rid in body.get("resourceIds") or []
                ]
            }
        )

    page.route("**/loco.observability.v1.ObservabilityService/QueryMetrics", metrics)


def metrics_table(page):
    table = page.get_by_role("table", name="By resource and region")
    expect(table.get_by_role("row").filter(has_text=LONG_NAME)).to_have_count(2)
    return table


def metrics(page):
    mock_metrics(page)
    page.goto(base + "/observability?env=production&view=metrics&range=1h")
    table = metrics_table(page)
    check_layout(table)
    row = table.get_by_role("row").filter(has_text="api").first
    expect_pointer(page, row.get_by_role("cell").nth(1))
    expect(table.locator('[data-slot="nav-arrow"]')).to_have_count(0)
    traffic = page.get_by_role("region", name="Traffic", exact=True)
    for card, latest in (
        ("Requests", "2/s"),
        ("5xx responses", "25.00%"),
        ("Latency, p95", "120 ms"),
    ):
        section = traffic.get_by_role("region", name=card, exact=True)
        expect(section.get_by_label(f"{card} over time")).to_be_visible()
        expect(section.get_by_role("listitem").first).to_contain_text(latest)
    api = table.get_by_role("row").filter(has_text="api").first
    for latest in ("2/s", "25.00%", "120 ms"):
        expect(api.get_by_role("cell", name=latest, exact=True)).to_be_visible()
    for column in ("Requests", "5xx", "p95"):
        button = page.get_by_role("button", name=column, exact=True)
        button.click()
        expect(table.get_by_role("columnheader").filter(has=button)).to_have_attribute(
            "aria-sort", "descending"
        )
    table = check_resize(page, table, "CPU (latest)", "Region", metrics_table)
    table.get_by_role("row").filter(has_text="api").first.get_by_role("cell").nth(
        1
    ).click()
    expect(page).to_have_url(re.compile(r"resource(:|%3A)api"))


def named_table(name, ready):
    """Opens the table called `name` once a row containing `ready` shows."""

    def reopen(page):
        table = page.get_by_role("table", name=name, exact=True)
        expect(table.get_by_role("row").filter(has_text=ready).first).to_be_visible()
        return table

    return reopen


def check_table(page, reopen, keyboard, drag, key="ArrowRight", clickable=True):
    """Checks layout, the pointer and arrow-free rows for a table that opens details in place, then resizing."""
    table = reopen(page)
    check_layout(table)
    fits_at_16px(page, table)
    if clickable:
        expect_pointer(
            page,
            table.locator("tbody tr[data-interactive]")
            .first.get_by_role("cell")
            .nth(1),
        )
    else:
        expect(table.locator("tbody tr[data-interactive]")).to_have_count(0)
    expect(table.locator('[data-slot="nav-arrow"]')).to_have_count(0)
    return check_resize(page, table, keyboard, drag, reopen, key)


def recent_deployments(page):
    page.goto(base + "?env=production")
    reopen = named_table("Deployments", LONG_NAME)
    table = reopen(page)
    check_layout(table)
    fits_at_16px(page, table)
    row = table.get_by_role("row").filter(has_text="api").first
    expect_pointer(page, row.get_by_role("cell").nth(5))
    check_arrow(page, table, row, None)
    table = check_resize(page, table, "Deployed by", "Region", reopen, "ArrowLeft")
    table.get_by_role("row").filter(has_text="api").first.get_by_role("cell").nth(
        5
    ).click()
    expect(page).to_have_url(re.compile(r"/resource/"))


def history(page):
    resource = next(
        r["id"]
        for r in call(
            state,
            "loco.resource.v1.ResourceService/ListWorkspaceResources",
            {"workspaceId": workspace},
        )["resources"]
        if r["name"] == "api"
    )
    page.goto(base + f"/resource/{resource}?env=production")
    reopen = named_table("Deployments", "Running")
    table = check_table(page, reopen, "Deployed by", "Status", clickable=False)
    check_layout(table)
    page.get_by_role("tab", name=re.compile(r"^Builds")).click()
    reopen = named_table("Builds", "Olivia Owner")
    table = check_table(page, reopen, "Started by", "Status", "ArrowLeft")
    width = scroller_of(page, table).evaluate("node => node.scrollWidth")
    table.locator("tbody tr[data-interactive]").first.get_by_role("cell").nth(4).click()
    expect(table.get_by_role("button", name=re.compile("^Hide build"))).to_be_visible()
    check_layout(table)
    assert scroller_of(page, table).evaluate("node => node.scrollWidth") == width


def organizations(page):
    page.goto(state["ui_url"] + "/organizations")
    reopen = named_table("Organizations", "beta")
    table = reopen(page)
    check_layout(table)
    fits_at_16px(page, table)
    row = table.get_by_role("row", name="Open beta", exact=True)
    expect_pointer(page, row.get_by_role("cell").nth(1))
    check_arrow(page, table, row, row.get_by_role("button", name="Actions for beta"))
    table = check_resize(page, table, "Your role", "Created", reopen, "ArrowRight")
    table.get_by_role("row", name="Open beta", exact=True).get_by_role("cell").nth(
        1
    ).click()
    expect(page).not_to_have_url(re.compile(r"/organizations"))


def settings(page):
    page.goto(base + "/settings?scope=org")
    reopen = named_table("Workspaces", "sibling")
    table = reopen(page)
    check_layout(table)
    row = table.get_by_role("row").filter(has_text="sibling")
    expect_pointer(page, row.get_by_role("cell").nth(2))
    check_arrow(page, table, row, None)
    check_resize(page, table, "Resources", "Name", reopen, "ArrowRight")
    reopen = named_table("Limits", "Build images")
    table = reopen(page)
    expect(table.locator("tbody tr[data-group]")).to_have_text("History kept")
    check_layout(table)
    fits_at_16px(page, table)
    expect(table.locator("tbody tr[data-interactive]")).to_have_count(0)
    shows_whole(page, table.get_by_text("User, organization, workspace or resource"))
    before = head_width(table, "In use")
    handle = table.get_by_role("separator", name="Resize In use")
    handle.focus()
    handle.press("ArrowRight")
    handle.press("ArrowRight")
    assert abs(head_width(table, "In use") - (before + 32)) <= 1
    page.reload()
    assert abs(head_width(reopen(page), "In use") - (before + 32)) <= 1
    named_table("Workspaces", "sibling")(page).get_by_role("row").filter(
        has_text="sibling"
    ).get_by_role("cell").nth(2).click()
    expect(page).to_have_url(
        re.compile(f"/wks/{state['workspaces']['acme/sibling']}/settings")
    )

    page.goto(base + "/settings?scope=workspace")
    check_table(
        page,
        named_table("Environments", "Staging EU West"),
        "Description",
        "Name",
        clickable=False,
    )
    check_table(
        page,
        named_table("Data retention", "Traces"),
        "Organization limit",
        "Signal",
        clickable=False,
    )


def team(page):
    page.goto(state["ui_url"] + f"/org/{org}/team")
    check_table(
        page,
        named_table("People", "Max Member"),
        "Last active",
        "Role",
        clickable=False,
    )


def tokens(page):
    page.goto(state["ui_url"] + "/tokens?owner=org")
    table = check_table(
        page,
        named_table("Tokens", "ci-deploy"),
        "Last used",
        "Created by",
    )

    table.locator("tbody tr[data-interactive]").first.get_by_role("cell").nth(1).click()
    details = page.get_by_role("dialog", name="Token details", exact=True)
    expect(details.get_by_label("Name", exact=True)).to_have_attribute("readonly", "")
    page.keyboard.press("Escape")
    expect(details).to_be_hidden()


def webhooks(page):
    page.goto(base + "/webhooks")
    reopen = named_table("Webhooks", "hooks.invalid")
    shows_whole(
        page, reopen(page).get_by_text("/checkout/payments/reconciliation", exact=True)
    )
    table = check_table(page, reopen, "Events", "Created")
    table.locator("tbody tr[data-interactive]").first.get_by_role("cell").nth(2).click()
    expect(page.get_by_role("dialog", name="hooks.invalid", exact=True)).to_be_visible()


with session(
    repo / "tests/ui/out/tables", ignore_default_args=["--hide-scrollbars"]
) as browser:
    for root in ("16px", "20px"):
        context, page, errors = open_page(browser, state=state)
        context.add_init_script(
            f"document.addEventListener('DOMContentLoaded', () => {{ document.documentElement.style.fontSize = '{root}'; }})"
        )
        for check in (
            resources,
            resources_map,
            dashboard,
            recent_deployments,
            history,
            logs,
            traces,
            events,
            metrics,
            audit,
            organizations,
            settings,
            team,
            tokens,
            webhooks,
        ):
            check(page)
        assert not errors, errors
        context.close()

    # A link from before the tables paged by token opens the first page; the API's rejection logs a console error.
    context, page, _ = open_page(browser, state=state)
    for path in ("/resources?env=production&page=2", "?env=production&page=2"):
        page.goto(base + path)
        expect(
            page.get_by_role("table", name="Resources").get_by_role(
                "link", name="api", exact=True
            )
        ).to_be_visible()
        expect(page).not_to_have_url(re.compile(r"[?&]page="))
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
    mock_metrics(page)
    for path, reopen in (
        ("/observability?env=production&view=events&live=0", events_table),
        ("/observability?env=production&view=metrics&range=1h", metrics_table),
    ):
        page.goto(base + path)
        table = reopen(page)
        check_layout(table)
        check_fade(scroller_of(page, table))
    page.goto(state["ui_url"] + f"/org/{org}/audit")
    table = audit_table(page)
    check_layout(table)
    check_fade(scroller_of(page, table))
    for path, name, ready in (
        ("/organizations", "Organizations", "beta"),
        (f"/org/{org}/wks/{workspace}/settings?scope=org", "Workspaces", "sibling"),
        (
            f"/org/{org}/wks/{workspace}/settings?scope=workspace",
            "Environments",
            "Staging EU West",
        ),
        (f"/org/{org}/wks/{workspace}/webhooks", "Webhooks", "hooks.invalid"),
        (
            f"/org/{org}/wks/{workspace}/settings?scope=workspace",
            "Data retention",
            "Traces",
        ),
    ):
        page.goto(state["ui_url"] + path)
        table = named_table(name, ready)(page)
        assert scroller_of(page, table).evaluate(
            "node => node.scrollWidth <= node.clientWidth"
        ), f"{name} scrolls sideways at phone width"
    page.goto(state["ui_url"] + "/tokens?owner=org")
    table = named_table("Tokens", "ci-deploy")(page)
    for row in table.locator("tbody tr").all():
        chip = row.locator('[data-slot="chip"]').last.bounding_box()
        actions = row.locator('[data-pin="end"]').bounding_box()
        assert chip["x"] + chip["width"] <= actions["x"], (chip, actions)
    assert not errors, errors
    context.close()
print(
    "PASS: tables size columns to fit, resize by drag and keyboard, keep widths after reload, pin columns and scroll sideways"
)
