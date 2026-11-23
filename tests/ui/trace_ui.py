import argparse
import base64
import calendar
import json
import re
import time
import urllib.parse
from pathlib import Path

from harness import (
    call,
    copied_text,
    expect_rpc,
    line_starts,
    load_state,
    open_page,
    repo,
    session,
    wait_for_query,
)
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Exercise the production trace viewer with fixture trace responses"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/trace-ui"))
args = parser.parse_args()
state = load_state(args, "trace_ui", "tests/ui/fixtures/trace.json")
org_id = state["orgs"]["acme"]["id"]
workspace_id = state["workspaces"]["acme/shared"]
base = f"/org/{org_id}/wks/{workspace_id}/observability?view=traces"
trace = "4bf92f3577b34da6a3ce929d0e0e4736"
# Yesterday's local noon: the browser clock and every seeded trace share one local day.
today = time.localtime()
now = int(
    time.mktime((today.tm_year, today.tm_mon, today.tm_mday - 1, 12, 0, 0, 0, 0, -1))
)
start = now - 600
rows = [
    ("0001", "", "frontend-proxy", "POST /api/checkout", 0, 427.6),
    ("0002", "0001", "frontend", "POST /api/checkout", 1.2, 424.1),
    ("0003", "0002", "checkout", "oteldemo.CheckoutService/PlaceOrder", 5.3, 412.0),
    ("0004", "0003", "cart", "GetCart", 8.4, 24.5),
    ("0005", "0004", "valkey", "GET cart:session", 10.1, 7.8),
    ("0006", "0003", "product-catalog", "GetProduct · OLJCESPC7Z", 34.1, 18.9),
    ("0007", "0003", "currency", "Convert · USD → EUR", 58.7, 12.4),
    ("0008", "0003", "shipping", "GetQuote", 76.3, 86.2),
    ("0009", "0008", "quote", "POST /quote", 79.5, 71.4),
    ("000a", "0003", "payment", "Charge · attempt 1", 166.8, 96.1),
    ("000b", "000a", "payment", "POST /payments/authorize", 168.2, 91.7),
    ("000c", "0003", "payment", "Charge · attempt 2", 275.2, 78.6),
    ("000d", "0003", "email", "SendOrderConfirmation", 357.4, 45.2),
    ("000e", "0003", "cart", "EmptyCart", 405.1, 8.6),
]


def stamp(offset):
    return f"{time.strftime('%Y-%m-%dT%H:%M:%S', time.gmtime(start))}.{int(offset * 1000000):09d}Z"


def span(row):
    sid, parent, service, name, offset, duration = row
    return {
        "traceId": trace,
        "spanId": sid.zfill(16),
        "parentSpanId": parent.zfill(16) if parent else "",
        "name": name,
        "kind": "Server",
        "serviceName": service,
        "startTime": stamp(offset),
        "durationNanos": str(int(duration * 1000000)),
        "statusCode": "Error"
        if sid in ("000a", "000b")
        else "Unset"
        if sid == "000e"
        else "Ok",
        "statusMessage": "Payment authorization timed out after 90 ms"
        if sid == "000b"
        else "",
        "spanAttributes": {
            "http.request.method": "POST",
            "http.response.status_code": "504" if sid == "000b" else "200",
            "server.address": "payments.internal",
            "payment.attempt": "1",
        }
        if sid == "000b"
        else {
            "rpc.system": "grpc",
            "rpc.service": f"oteldemo.{service.title()}Service",
        },
        "resourceAttributes": {
            "service.name": service,
            "service.version": "2.1.0",
            "deployment.environment.name": "production",
            "cloud.region": "us-ca-1",
        },
        "scopeName": "@opentelemetry/instrumentation-http",
        "scopeVersion": "0.210.0",
    }


spans = [span(row) for row in rows]
spans[0]["startTime"] = stamp(0.000123)
concurrent_spans = [
    span(row)
    for row in [
        ("1000", "", "frontend", "GET /recommendations", 0, 10),
        ("1001", "1000", "product-catalog", "Product lookup", 0.5, 8),
        (
            "1002",
            "1001",
            "product-catalog",
            "grpc.oteldemo.ProductCatalogService/GetProduct",
            1,
            2.62,
        ),
        (
            "1003",
            "1001",
            "product-catalog",
            "grpc.oteldemo.ProductCatalogService/GetProduct",
            2,
            3.98,
        ),
        ("1004", "1002", "cache", "Read product cache", 2, 4),
        (
            "1005",
            "1001",
            "product-catalog",
            "grpc.oteldemo.ProductCatalogService/GetProduct",
            4,
            3,
        ),
    ]
]

spans[10]["events"] = [
    {
        "name": "retry.scheduled",
        "timestamp": stamp(168.2 + 12.4),
        "attributes": {"attempt": "1"},
    },
    {
        "name": "exception",
        "timestamp": stamp(168.2 + 90),
        "attributes": {
            "exception.type": "TimeoutError",
            "exception.message": "Authorization timed out",
            "exception.stacktrace": "TimeoutError: Authorization timed out\n    at authorize (payments.ts:42:7)\n    at checkout (checkout.ts:18:3)",
        },
    },
]
linked_trace = "1" * 32
linked_span = "2" * 16
spans[11]["links"] = [
    {
        "traceId": trace,
        "spanId": spans[5]["spanId"],
        "attributes": {"link.reason": "retry"},
    },
    {"traceId": linked_trace, "spanId": linked_span, "attributes": {}},
]
recorded = []
mode = ["normal"]


def iso_seconds(value):
    return calendar.timegm(time.strptime(value[:19], "%Y-%m-%dT%H:%M:%S"))


def trace_buckets(body):
    width = body["intervalSeconds"]
    first = int(iso_seconds(body["startTime"])) // width * width
    last = int(iso_seconds(body["endTime"])) // width * width
    buckets = [
        {"startTime": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(at))}
        for at in range(first, last + 1, width)
    ]
    for index, bucket in enumerate(buckets[-4:]):
        bucket.update(
            traceCount=str(index + 2),
            errorCount=str(index % 2),
            p50DurationNanos=str((index + 1) * 20000000),
            p95DurationNanos=str((index + 1) * 90000000),
        )
    if mode[0] == "outlier":
        buckets[-5].update(
            traceCount="1",
            errorCount="0",
            p50DurationNanos="400000000000",
            p95DurationNanos="500000000000",
        )
    # The list's totals: one error trace among 7, and 60 more traces when paged.
    traces = "67" if mode[0] == "paged" else "7"
    return {
        "buckets": buckets,
        "total": {
            "traceCount": "1" if "@status:error" in body.get("query", "") else traces,
            "errorCount": "1",
            "p50DurationNanos": "42000000",
            "p95DurationNanos": "360000000",
        },
    }


UNIT_MS = {"s": 1000, "ms": 1, "µs": 0.001, "ns": 0.000001}


def milliseconds(text):
    match = re.fullmatch(r"([0-9.]+)(s|ms|µs|ns)", text)
    return float(match.group(1)) * UNIT_MS[match.group(2)]


def last_page_request():
    return next(
        row
        for row in reversed(recorded)
        if "startTime" in row and row.get("limit") in (25, 50, 100)
    )


def page_request(**fields):
    return lambda body: (
        body.get("limit") in (25, 50, 100)
        and all(body.get(key) == value for key, value in fields.items())
    )


def message(field, content):
    data = content if isinstance(content, bytes) else content.encode()
    return bytes([field << 3 | 2, len(data)]) + data


def query_syntax_error(description):
    violation = message(1, message(1, "query") + message(2, description))
    info = (
        message(1, "QUERY_SYNTAX")
        + message(2, "loco")
        + message(3, message(1, "position") + message(2, "1"))
    )

    def detail(kind, payload):
        return {"type": kind, "value": base64.b64encode(payload).decode()}

    return {
        "code": "invalid_argument",
        "message": description,
        "details": [
            detail("google.rpc.BadRequest", violation),
            detail("google.rpc.ErrorInfo", info),
        ],
    }


def handle(route):
    body = route.request.post_data_json
    recorded.append(body)
    if mode[0] == "query-error" and not route.request.url.endswith("/GetTrace"):
        route.fulfill(
            status=400,
            content_type="application/json",
            body=json.dumps(query_syntax_error("unclosed (")),
        )
        return
    if mode[0] == "drawer-error" and route.request.url.endswith("/GetTrace"):
        route.fulfill(
            status=500,
            content_type="application/json",
            body=json.dumps({"code": "internal", "message": "Trace read failed"}),
        )
        return
    if mode[0] == "zero" and route.request.url.endswith("/GetTrace"):
        route.fulfill(json={"spans": [dict(spans[0], durationNanos="0")]})
        return
    if mode[0] == "drawer-empty" and route.request.url.endswith("/GetTrace"):
        route.fulfill(json={"spans": []})
        return
    if mode[0] == "error":
        route.fulfill(
            status=503,
            content_type="application/json",
            body=json.dumps(
                {
                    "code": "unavailable",
                    "message": "Trace storage is temporarily unavailable",
                }
            ),
        )
        return
    if mode[0] == "empty":
        route.fulfill(json={"spans": []})
        return
    if route.request.url.endswith("/QueryTraceBuckets"):
        route.fulfill(json=trace_buckets(body))
        return
    if mode[0] == "concurrent" and route.request.url.endswith("/GetTrace"):
        route.fulfill(json={"spans": concurrent_spans})
        return
    if mode[0] == "large" and route.request.url.endswith("/GetTrace"):
        route.fulfill(
            json={
                "spans": [spans[0]]
                + [
                    dict(
                        spans[5],
                        spanId=f"{index + 256:016x}",
                        parentSpanId=spans[0]["spanId"],
                    )
                    for index in range(3000)
                ]
            }
        )
        return
    if mode[0] == "many" and not route.request.url.endswith("/ListTraces"):
        route.fulfill(
            json={
                "spans": spans
                + [
                    dict(spans[5], spanId=f"{index + 256:016x}", name=f"Lookup {index}")
                    for index in range(100)
                ]
            }
        )
        return
    if route.request.url.endswith("/ListTraces"):
        others = [
            dict(
                spans[index],
                traceId=f"{index + 100:032x}",
                parentSpanId="",
                name=name,
                durationNanos=str(duration * 1000000),
            )
            for index, (name, duration) in enumerate(
                [
                    ("GET /api/products", 18),
                    ("GetRecommendations", 132),
                    ("GetProduct", 9),
                    ("GET /cart", 42),
                    ("Convert · USD → EUR", 12),
                    ("GetQuote", 86),
                ]
            )
        ]
        summaries = [
            {
                "traceId": trace,
                "rootSpanName": spans[0]["name"],
                "serviceName": spans[0]["serviceName"],
                "startTime": spans[0]["startTime"],
                "durationNanos": "427600000",
                "status": "TRACE_STATUS_ERROR",
                "spanCount": str(len(spans)),
                "httpStatusCode": 200,
            }
        ]
        summaries += [
            {
                "traceId": row["traceId"],
                "rootSpanName": row["name"],
                "serviceName": row["serviceName"],
                "startTime": row["startTime"],
                "durationNanos": row["durationNanos"],
                "status": "TRACE_STATUS_OK",
                "spanCount": "1",
            }
            for row in others
        ]
        if mode[0] == "dated":
            summaries[-1]["startTime"] = time.strftime(
                "%Y-%m-%dT%H:%M:%S.000000000Z", time.gmtime(start - 3 * 86400)
            )
        if mode[0] == "paged":
            summaries += [
                dict(
                    summaries[1],
                    traceId=f"{index + 1000:032x}",
                    rootSpanName=f"Paged trace {index}",
                )
                for index in range(60)
            ]
        offset = body.get("offset", 0)
        route.fulfill(
            json={
                "totalCount": str(len(summaries)),
                "summaries": summaries[offset : offset + body["limit"]],
            }
        )
        return
    if body.get("traceId") == linked_trace:
        route.fulfill(
            json={
                "spans": [
                    dict(
                        spans[5],
                        traceId=linked_trace,
                        spanId=linked_span,
                        parentSpanId="",
                        name="Linked product lookup",
                    )
                ]
            }
        )
        return
    route.fulfill(json={"spans": spans})


out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with session(out) as browser:
    root_name = "POST /api/checkout"
    failing = re.compile(r"^payment POST \/payments\/authorize, .*, error$")
    context, page, errors = open_page(
        browser,
        state=state,
        now=now,
        device_scale_factor=2,
        reduced_motion="reduce",
    )
    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)

    def six_hour_config(route):
        response = route.fetch()
        body = response.json()
        body["observabilityMaxTimeRange"] = "21600s"
        route.fulfill(response=response, json=body)

    context.route("**/loco.config.v1.ConfigService/GetConfig*", six_hour_config)
    page.goto(state["ui_url"] + f"/org/{org_id}/wks/{workspace_id}/resources")
    expect(page).to_have_url(re.compile(r"[?&]env="))
    page.get_by_role("link", name="Traces", exact=True).click()
    open_trace = page.get_by_role("region", name="Trace list").get_by_role(
        "row", name="Open trace " + root_name, exact=True
    )
    open_trace.wait_for()
    expect(page.get_by_role("heading", level=1)).to_have_text("Traces")
    expect(page.get_by_role("columnheader", name="Time", exact=True)).to_have_attribute(
        "aria-sort", "descending"
    )
    wait_for_query(
        page,
        lambda query: (
            query.get("env") == ["production"] and query.get("view") == ["traces"]
        ),
    )
    expect(page.get_by_role("button", name=re.compile(r"^All\s*7$"))).to_be_visible()
    expect(page.get_by_role("button", name=re.compile(r"^Errors\s*1$"))).to_be_visible()
    expect(open_trace).to_contain_text("Error")
    expect(open_trace).to_have_attribute("aria-selected", "false")
    open_trace.click(position={"x": 20, "y": 10})
    drawer = page.get_by_role("dialog", name=root_name)
    expect(page).to_have_url(re.compile("trace=" + trace))
    assert drawer.evaluate("(element) => element.contains(document.activeElement)")
    drawer.get_by_role("button", name="Close", exact=True).focus()
    page.keyboard.press("ArrowDown")
    expect(page).not_to_have_url(re.compile("trace=" + trace))
    page.keyboard.press("ArrowUp")
    expect(page).to_have_url(re.compile("trace=" + trace))
    before = drawer.bounding_box()["width"]
    handle = drawer.get_by_role("separator", name="Resize drawer")
    box = handle.bounding_box()
    x, y = box["x"] + box["width"] / 2, box["y"] + box["height"] / 2
    page.mouse.move(x, y)
    page.mouse.down()
    page.mouse.move(x + 200, y, steps=5)
    page.mouse.up()
    narrow = drawer.bounding_box()["width"]
    assert narrow < before - 150, (before, narrow)
    handle.dispatch_event("dblclick")
    page.wait_for_function(
        "([el, w]) => el.getBoundingClientRect().width > w",
        arg=[drawer.element_handle(), narrow + 150],
    )
    flame = drawer.get_by_role("region", name="Flame graph")
    expect(flame.get_by_role("button")).to_have_count(len(spans))
    bottom = drawer.get_by_role("region", name="Trace bottom panel", exact=True)
    flame_box = flame.bounding_box()
    assert bottom.bounding_box()["y"] - flame_box["y"] - flame_box["height"] <= 20
    details = drawer.get_by_role("region", name="Span details")
    expect(drawer.get_by_role("region", name="Selected span")).to_have_count(0)
    expect(details.get_by_role("heading", name="Details", exact=True)).to_be_visible()
    assert (
        details.locator("dl").evaluate("dl => getComputedStyle(dl).borderTopWidth")
        == "1px"
    )
    flame.get_by_role("button", name=failing).click()
    expect(flame.get_by_role("button", name=failing)).to_have_attribute(
        "aria-pressed", "true"
    )
    expect(page).to_have_url(re.compile("span=" + spans[10]["spanId"]))
    expect(
        drawer.get_by_role("region", name="Selected span").get_by_role(
            "heading", name="POST /payments/authorize", exact=True
        )
    ).to_be_visible()
    expect(
        drawer.get_by_text("Payment authorization timed out after 90 ms", exact=True)
    ).to_be_visible()
    stack = spans[10]["events"][1]["attributes"]["exception.stacktrace"]
    expect(drawer.locator("pre").filter(has_text="TimeoutError")).to_have_text(stack)
    drawer.get_by_role("button", name="Copy stack trace", exact=True).click()
    assert copied_text(page) == stack
    attributes = drawer.get_by_role("region", name="Span attributes")
    expect(details.get_by_text(spans[10]["spanId"], exact=True)).to_be_visible()

    def fact(label):
        return details.locator("div").filter(
            has=page.locator("dt").filter(has_text=re.compile("^" + label + "$"))
        )

    expect(fact("Duration")).to_contain_text("21% of trace")
    expect(fact("Starts at")).to_contain_text("+168ms")
    expect(fact("Parent")).to_contain_text("Charge · attempt 1")
    expect(fact("Resource")).to_contain_text("payment")
    expect(attributes.get_by_role("heading", level=5)).to_have_text(
        ["HTTP", "Server", "Other"]
    )
    expect(
        attributes.get_by_text("http.response.status_code", exact=True)
    ).to_be_visible()
    attributes.get_by_role("tab", name="Telemetry", exact=True).click()
    expect(attributes.get_by_role("tablist", name="Span detail tabs")).to_be_visible()
    attributes.get_by_role("tab", name="Telemetry", exact=True).press("ArrowLeft")
    expect(attributes.get_by_role("tab", name="Kubernetes", exact=True)).to_be_focused()
    attributes.get_by_role("tab", name="Kubernetes", exact=True).press("Enter")
    expect(
        attributes.get_by_role("tab", name="Kubernetes", exact=True)
    ).to_have_attribute("aria-selected", "true")
    expect(
        attributes.get_by_role("tabpanel", name="Kubernetes", exact=True)
    ).to_be_visible()
    attributes.get_by_role("tab", name="Telemetry", exact=True).click()
    expect(attributes.get_by_text("service.version", exact=True)).to_be_visible()
    expect(
        attributes.get_by_text("@opentelemetry/instrumentation-http", exact=True)
    ).to_be_visible()
    attributes.get_by_role("tab", name="Attributes", exact=True).click()
    expect(
        attributes.get_by_role(
            "button",
            name="Only traces with this value: http.response.status_code",
            exact=True,
        )
    ).to_be_enabled()
    expect(
        attributes.get_by_role(
            "button",
            name="Hide traces with this value: http.response.status_code",
            exact=True,
        )
    ).to_be_enabled()
    attributes.get_by_role("tab", name="Telemetry", exact=True).click()
    expect(
        attributes.get_by_role(
            "button", name="Only traces with this value: scope.name", exact=True
        )
    ).to_be_disabled()
    attributes.get_by_role("tab", name="Attributes", exact=True).click()
    for name, status in (("cart EmptyCart", "Unset"), ("checkout ", "OK")):
        flame.get_by_role("button", name=re.compile("^" + name)).first.click()
        expect(
            drawer.get_by_role("region", name="Selected span").locator(
                '[data-slot="badge"]'
            )
        ).to_have_text(status)
        expect(
            drawer.get_by_role("region", name="Span details")
            .get_by_role("definition")
            .filter(has_text=re.compile("^(OK|Unset|Error)$"))
        ).to_have_text(status)
    flame.get_by_role("button", name=failing).click()
    page.mouse.move(0, 0)
    expect(page.locator('[data-slot="tooltip-content"]')).to_have_count(0)
    trace_chip = drawer.get_by_role("button", name="Copy trace ID " + trace, exact=True)
    expect(trace_chip).to_have_text("Trace" + trace[:8])
    trace_chip.hover()
    expect(page.locator('[data-slot="tooltip-content"]')).to_have_text(trace)
    trace_chip.click()
    assert copied_text(page) == trace
    drawer.get_by_role("button", name="More for this trace", exact=True).click()
    expect(
        page.get_by_role("menuitem", name="Copy trace ID", exact=True)
    ).to_have_count(0)
    expect(page.get_by_role("menuitem")).to_have_text(
        [
            "Logs for this traceFilter logs by @trace",
            f"Copy as JSON{len(spans)} spans, OTLP shape",
            f"Download JSONtrace-{trace[:8]}….json",
            "Copy linkThis trace and span",
        ]
    )
    page.get_by_role("menuitem", name=re.compile("^Copy as JSON")).click()
    exported = json.loads(copied_text(page))
    exported_spans = [
        span
        for resource in exported["resourceSpans"]
        for scope in resource["scopeSpans"]
        for span in scope["spans"]
    ]
    assert len(exported_spans) == len(spans)
    assert next(
        span for span in exported_spans if span["spanId"] == spans[10]["spanId"]
    )["events"][1]["attributes"]
    drawer.get_by_role("button", name="More for this trace", exact=True).click()
    page.get_by_role("menuitem", name=re.compile("^Copy link")).click()
    assert copied_text(page) == page.url
    drawer.get_by_role("button", name="More for this trace", exact=True).click()
    with page.expect_download() as download_info:
        page.get_by_role("menuitem", name=re.compile("^Download JSON")).click()
    assert download_info.value.suggested_filename == f"trace-{trace}.json"
    expect(
        page.get_by_text(f"Downloaded trace-{trace[:8]}.json", exact=True)
    ).to_be_visible()
    ranking = drawer.get_by_role("group", name="Time spent per resource")
    expect(ranking.get_by_role("button", name="+7 more", exact=True)).to_be_visible()
    expect(ranking.get_by_role("button").filter(has_text="payment")).to_contain_text(
        re.compile(r"ms \(<?[0-9]+%\)")
    )
    expect(ranking.locator("button i")).to_have_count(4)
    ranking.get_by_role("button", name="+7 more", exact=True).click()
    expect(ranking.locator("button i")).to_have_count(11)
    colors = ranking.locator("button i").evaluate_all(
        "(dots) => dots.map(dot => getComputedStyle(dot).backgroundColor)"
    )
    assert len(set(colors)) == 11, colors
    ranking.get_by_role("button", name="Show fewer", exact=True).click()
    expect(ranking.locator("button i")).to_have_count(4)
    payment = ranking.get_by_role("button").filter(has_text="payment")
    payment.click()
    expect(payment).to_have_attribute("aria-pressed", "true")
    drawer.get_by_role("button", name="Show all resources", exact=True).click()
    expect(payment).to_have_attribute("aria-pressed", "false")
    overview = drawer.get_by_role("slider", name="Trace overview")
    expect(overview).to_have_count(0)
    axis = flame.locator("[data-trace-axis]")
    axis_box = axis.bounding_box()
    page.mouse.move(axis_box["x"] + axis_box["width"] * 0.2, axis_box["y"] + 8)
    page.mouse.down()
    page.mouse.move(axis_box["x"] + axis_box["width"] * 0.6, axis_box["y"] + 8, steps=6)
    page.mouse.up()
    expect(overview).to_have_attribute("aria-valuetext", "20–60% of trace")
    overview_box = overview.bounding_box()
    page.mouse.move(
        overview_box["x"] + overview_box["width"] * 0.3, overview_box["y"] + 12
    )
    page.mouse.down()
    page.mouse.move(
        overview_box["x"] + overview_box["width"] * 0.4, overview_box["y"] + 12, steps=4
    )
    page.mouse.up()
    expect(overview).to_have_attribute("aria-valuetext", "30–70% of trace")
    overview.click(position={"x": overview_box["width"] * 0.85, "y": 12})
    expect(overview).to_have_attribute("aria-valuetext", "60–100% of trace")
    page.mouse.move(
        overview_box["x"] + overview_box["width"] * 0.1, overview_box["y"] + 12
    )
    page.mouse.down()
    page.mouse.move(
        overview_box["x"] + overview_box["width"] * 0.4, overview_box["y"] + 12, steps=4
    )
    page.mouse.up()
    expect(overview).to_have_attribute("aria-valuetext", "10–40% of trace")
    drawer.get_by_role("button", name="Reset zoom", exact=True).click()
    expect(overview).to_have_count(0)
    flame.get_by_role("button", name=failing).dblclick()
    overview = drawer.get_by_role("slider", name="Trace overview")
    before_window = overview.get_attribute("aria-valuetext")
    overview.press("ArrowLeft")
    expect(overview).not_to_have_attribute("aria-valuetext", before_window)
    drawer.get_by_role("button", name="Reset zoom", exact=True).click()
    plot = drawer.locator("[data-trace-plot]")
    plot.hover(position={"x": 150, "y": 50})
    page.mouse.wheel(0, -250)
    expect(overview).to_be_visible()
    wheel_window = overview.get_attribute("aria-valuetext")
    page.mouse.wheel(200, 0)
    expect(overview).not_to_have_attribute("aria-valuetext", wheel_window)
    drawer.get_by_role("button", name="Reset zoom", exact=True).click()
    expect(overview).to_have_count(0)
    plot.hover(position={"x": 150, "y": 50})
    page.keyboard.down("Control")
    page.mouse.wheel(0, -250)
    page.keyboard.up("Control")
    expect(drawer.get_by_role("button", name="Reset zoom", exact=True)).to_be_visible()
    window = overview.get_attribute("aria-valuetext")
    plot.hover(position={"x": 160, "y": 50})
    page.mouse.down()
    page.mouse.move(
        plot.bounding_box()["x"] + 100, plot.bounding_box()["y"] + 50, steps=4
    )
    page.mouse.up()
    expect(overview).not_to_have_attribute("aria-valuetext", window)
    plot.hover(position={"x": 160, "y": 50})
    page.mouse.down()
    page.mouse.move(
        plot.bounding_box()["x"] + 140, plot.bounding_box()["y"] + 50, steps=2
    )
    plot.dispatch_event("pointercancel", {"pointerId": 1})
    # Drag zoom applies on the next frame; read the window once the last move has landed.
    page.evaluate(
        "() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))"
    )
    canceled_window = overview.get_attribute("aria-valuetext")
    page.mouse.move(
        plot.bounding_box()["x"] + 100, plot.bounding_box()["y"] + 50, steps=2
    )
    assert overview.get_attribute("aria-valuetext") == canceled_window
    page.mouse.up()
    drawer.get_by_role("button", name="Reset zoom", exact=True).click()
    drawer.get_by_role("button", name="Waterfall", exact=True).click()
    waterfall = drawer.get_by_role("region", name="Waterfall")
    axis_box = waterfall.locator("[data-trace-axis]").bounding_box()
    page.mouse.move(axis_box["x"] + axis_box["width"] * 0.1, axis_box["y"] + 8)
    page.mouse.down()
    page.mouse.move(axis_box["x"] + axis_box["width"] * 0.5, axis_box["y"] + 8, steps=6)
    page.mouse.up()
    expect(overview).to_have_attribute("aria-valuetext", "10–50% of trace")
    drawer.get_by_role("button", name="Reset zoom", exact=True).click()

    expect(waterfall.locator("[data-span]")).to_have_count(len(spans))
    waterfall.get_by_role(
        "button", name="Collapse POST /api/checkout", exact=True
    ).first.click()
    expect(waterfall.locator("[data-span]")).to_have_count(1)
    expect(waterfall.locator("[data-span]").first).to_contain_text(f"+{len(spans) - 1}")
    waterfall.get_by_role(
        "button", name="Expand POST /api/checkout", exact=True
    ).click()
    expect(waterfall.locator("[data-span]")).to_have_count(len(spans))
    first_name = waterfall.locator("[data-span]").first
    first_name.press("ArrowDown")
    expect(waterfall.locator("[data-span]").nth(1)).to_be_focused()
    page.keyboard.press("j")
    expect(waterfall.locator("[data-span]").nth(2)).to_be_focused()
    page.keyboard.press("k")
    expect(waterfall.locator("[data-span]").nth(1)).to_be_focused()
    # Selecting a leaf dims the rows outside its ancestors and descendants.
    for _ in range(3):
        page.keyboard.press("j")
    expect(waterfall.locator("[data-span]").nth(4)).to_be_focused()
    expect(waterfall.locator(f'[data-span-id="{spans[3]["spanId"]}"]')).to_have_css(
        "opacity", "1"
    )
    expect(waterfall.locator(f'[data-span-id="{spans[5]["spanId"]}"]')).to_have_css(
        "opacity", "0.45"
    )
    waterfall.get_by_role("separator", name="Resize span column").press("ArrowRight")
    expect(
        waterfall.get_by_role("separator", name="Resize span column")
    ).to_have_attribute("aria-valuenow", "356")
    hint = drawer.get_by_text(
        "Drag the time axis or double-click a span to zoom", exact=True
    )
    expect(hint).to_have_count(0)
    expect(
        drawer.get_by_role("group", name="Trace view").get_by_role("button")
    ).to_have_text(["Flame graph", "Waterfall"])
    drawer.get_by_role("button", name="Flame graph", exact=True).click()
    expect(hint).to_be_visible()
    flame.get_by_role("button", name=failing).click()
    page.get_by_text("Copied trace ID", exact=True).wait_for(state="hidden")
    linking_span = flame.get_by_role(
        "button", name=re.compile(r"^payment Charge · attempt 2,")
    )
    attributes = drawer.get_by_role("region", name="Span attributes")
    expect(attributes.get_by_role("tab", name="Links", exact=True)).to_have_count(0)
    linking_span.click()
    attributes.get_by_role("tab", name="Links", exact=True).click()
    links = drawer.get_by_role("region", name="Span links")
    expect(links.get_by_role("listitem")).to_have_count(2)
    expect(links.get_by_role("link")).to_have_count(2)
    expect(links).to_contain_text("link.reason")
    expect(links).to_contain_text(linked_trace)
    links.get_by_role("link").first.click()
    expect(page).to_have_url(re.compile("span=" + spans[5]["spanId"]))
    expect(drawer.get_by_role("region", name="Selected span")).to_contain_text(
        "GetProduct · OLJCESPC7Z"
    )
    linking_span.click()
    flame.get_by_role(
        "button", name=re.compile(r"^payment Charge · attempt 2,")
    ).dblclick()
    expect(drawer.get_by_role("button", name="Reset zoom", exact=True)).to_be_visible()
    attributes.get_by_role("tab", name="Links", exact=True).click()
    links.get_by_role("link").nth(1).click()
    wait_for_query(
        page,
        lambda query: (
            query.get("trace") == [linked_trace] and query.get("span") == [linked_span]
        ),
    )
    linked_drawer = page.get_by_role("dialog", name="Linked product lookup", exact=True)
    linked_drawer.wait_for()
    expect(
        linked_drawer.get_by_role("button", name="Reset zoom", exact=True)
    ).to_have_count(0)
    page.go_back()
    drawer.wait_for()
    drawer.get_by_role("button", name="2 linked spans", exact=True).click()
    expect(page).to_have_url(re.compile("span=" + spans[11]["spanId"]))
    expect(attributes.get_by_role("tab", name="Links", exact=True)).to_have_attribute(
        "aria-selected", "true"
    )
    expect(links.get_by_role("listitem")).to_have_count(2)
    flame.get_by_role("button", name=failing).click()
    expect(page).to_have_url(re.compile("span=" + spans[10]["spanId"]))
    assert not errors, errors
    previous_url = page.url
    previous_request = dict(
        next(row for row in reversed(recorded) if "startTime" in row)
    )
    page.keyboard.press("Escape")
    expect(drawer).to_have_count(0)
    expect(page).not_to_have_url(re.compile(r"[?&]trace="))
    spans.append(dict(spans[5], spanId="0000000000000f01", name="Late exported span"))
    open_trace.click()
    expect(flame.get_by_role("button")).to_have_count(len(spans))
    expect(
        flame.get_by_role("button", name=re.compile("Late exported span"))
    ).to_have_count(1)
    drawer.get_by_role("button", name="Close", exact=True).click()
    expect(drawer).to_have_count(0)
    spans.pop()
    page.get_by_role("button", name=re.compile(r"^Past 15 minutes ")).click()
    expect(page.get_by_text("Ranges up to 6h.", exact=True)).to_be_visible()
    expect(
        page.get_by_role("group", name="Presets").get_by_role(
            "button", name=re.compile("Past 12 hours")
        )
    ).to_have_count(0)
    page.get_by_role("group", name="Presets").get_by_role(
        "button", name="Past hour", exact=False
    ).click()
    expect(page).to_have_url(re.compile(r"[?&]range=1h"))
    page.go_back()
    expect(page).not_to_have_url(re.compile(r"[?&]range=1h"))
    page.goto(previous_url)
    expect(flame.get_by_role("button", name=failing)).to_have_attribute(
        "aria-pressed", "true"
    )
    drawer.get_by_role("button", name="Close", exact=True).click()
    expect(drawer).to_have_count(0)
    page.get_by_role("button", name="Refresh", exact=True).click()
    assert (
        next(row for row in reversed(recorded) if "startTime" in row)["startTime"]
        != previous_request["startTime"]
    )
    with expect_rpc(
        page,
        "ListTraces",
        lambda body: body.get("query") == "@trace:invalid" and body.get("limit") == 25,
    ) as filtered:
        page.goto(state["ui_url"] + base + "&q=" + urllib.parse.quote("@trace:invalid"))
    assert filtered.value.request.post_data_json["query"] == "@trace:invalid"
    page.goto(state["ui_url"] + base)
    expect(open_trace).to_be_visible()
    page.goto(
        state["ui_url"]
        + base
        + "&q=checkout&trace="
        + trace
        + "&span="
        + spans[10]["spanId"]
    )
    with expect_rpc(
        page,
        "ListTraces",
        lambda body: body.get("query") == "-@http.response.status_code:504 checkout",
    ):
        drawer.get_by_role("region", name="Span attributes").get_by_role(
            "button",
            name="Hide traces with this value: http.response.status_code",
            exact=True,
        ).click()
    expect(drawer).to_have_count(0)
    page.goto(state["ui_url"] + base)
    expect(open_trace).to_be_visible()
    page.goto(state["ui_url"] + base + "&trace=" + trace + "&span=ffffffffffffffff")
    drawer.get_by_text("The selected span is unavailable.", exact=True).wait_for()
    drawer.get_by_role("button", name="Close", exact=True).click()
    expect(drawer).to_have_count(0)
    mode[0] = "drawer-error"
    page.goto(state["ui_url"] + base + "&trace=" + trace)
    failed_drawer = page.get_by_role("dialog", name="Trace", exact=True)
    expect(
        failed_drawer.get_by_role("heading", name="Couldn’t load this trace")
    ).to_be_visible()
    expect(
        failed_drawer.get_by_text("ObservabilityService/GetTrace", exact=True)
    ).to_be_visible()
    expect(
        failed_drawer.get_by_role("button", name="Copy trace ID " + trace, exact=True)
    ).to_have_text("Trace" + trace[:8])
    failed_drawer.get_by_role(
        "button", name="Copy trace ID " + trace, exact=True
    ).click()
    assert copied_text(page) == trace
    mode[0] = "normal"
    failed_drawer.get_by_role("button", name="Retry", exact=True).click()
    expect(drawer).to_be_visible()
    drawer.get_by_role("button", name="Close", exact=True).click()
    mode[0] = "drawer-empty"
    page.goto(state["ui_url"] + base + "&trace=" + trace)
    gone = page.get_by_role("dialog")
    expect(
        gone.get_by_role("heading", name="This trace is no longer available")
    ).to_be_visible()
    expect(gone.get_by_role("button", name="More for this trace")).to_have_count(0)
    page.goto(state["ui_url"] + base + "&trace=4bf92f35")
    malformed = page.get_by_role("dialog")
    expect(malformed.get_by_role("heading", name="Trace not found")).to_be_visible()
    expect(malformed).to_contain_text("The trace ID looks invalid")
    expect(malformed).not_to_contain_text("no longer available")
    page.goto(state["ui_url"] + base + "&trace=" + trace)
    call(
        state,
        "loco.workspace.v1.WorkspaceService/UpdateWorkspace",
        {"workspaceId": workspace_id, "traceRetentionDays": 9},
    )
    page.reload()
    expect(
        gone.get_by_text(
            "Traces are kept for 9 days. It may also have been dropped before it was stored.",
            exact=True,
        )
    ).to_be_visible()
    gone.get_by_role("button", name="Back to traces", exact=True).click()
    expect(gone).to_have_count(0)
    mode[0] = "empty"
    page.get_by_role("button", name="Refresh", exact=True).click()
    page.get_by_text("Send your first trace", exact=True).wait_for()
    expect(page.get_by_role("combobox", name="Search and filter")).to_have_count(0)
    expect(
        page.get_by_text(
            "Loco configures the collector connection by default. Add instrumentation to your app, or configure its SDK endpoint yourself.",
            exact=True,
        )
    ).to_be_visible()
    page.get_by_role("tab", name="Python", exact=True).click()
    expect(
        page.get_by_text("opentelemetry-instrument python app.py", exact=False)
    ).to_be_visible()
    mode[0] = "error"
    page.get_by_role("button", name="Refresh", exact=True).click()
    page.get_by_text("Trace storage is temporarily unavailable", exact=False).wait_for(
        timeout=20000
    )
    expect(
        page.get_by_role("heading", name="Couldn\u2019t load traces", exact=True)
    ).to_be_visible()
    expect(
        page.get_by_text("ObservabilityService/ListTraces", exact=True)
    ).to_be_visible()
    mode[0] = "query-error"
    page.goto(state["ui_url"] + base + "&q=(((")
    expect(page.get_by_role("alert")).to_contain_text("unclosed (")
    expect(page.get_by_role("region", name="Trace list", exact=True)).to_be_visible()
    expect(page.get_by_role("region", name="Overview", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", name="Couldn\u2019t load traces")).to_have_count(
        0
    )
    mode[0] = "normal"
    page.goto(state["ui_url"] + base)
    open_trace.wait_for()
    with expect_rpc(
        page,
        "ListTraces",
        lambda body: body.get("query") == "@resource:not-a-resource",
    ) as filtered:
        page.goto(state["ui_url"] + base + "&r=not-a-resource")
    assert filtered.value.request.post_data_json["query"] == "@resource:not-a-resource"
    page.goto(state["ui_url"] + base)
    open_trace.wait_for()
    context.close()
    mode[0] = "paged"
    context, page, _ = open_page(browser, state=state, now=now)
    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
    with expect_rpc(page, "ListTraces", page_request(limit=25)):
        page.goto(state["ui_url"] + base)
    assert last_page_request().get("limit") == 25
    expect(page.get_by_text("1–25 of 67", exact=True)).to_be_visible()
    expect(
        page.get_by_role("region", name="Trace list").locator(
            'tr[aria-label^="Open trace "]'
        )
    ).to_have_count(25)
    root_span = page.get_by_role("columnheader", name="Root span", exact=True)
    width = root_span.evaluate("node => node.getBoundingClientRect().width")
    page.get_by_role("separator", name="Resize Root span").press("ArrowRight")
    expect(page).to_have_url(re.compile(r"widths=span(:|%3A)\d+"))
    page.wait_for_function(
        "([node, width]) => Math.abs(node.getBoundingClientRect().width - width) <= 1",
        arg=[root_span.element_handle(), width + 16],
    )
    with expect_rpc(page, "ListTraces", page_request(offset=25)):
        page.get_by_role("button", name="Next page", exact=True).click()
    assert last_page_request()["offset"] == 25
    expect(page.get_by_text("26–50 of 67", exact=True)).to_be_visible()
    with expect_rpc(page, "ListTraces", page_request(offset=50)):
        page.get_by_role("button", name="Last page", exact=True).click()
    assert last_page_request()["offset"] == 50
    expect(
        page.get_by_role("region", name="Trace list").locator(
            'tr[aria-label^="Open trace "]'
        )
    ).to_have_count(17)
    expect(page.get_by_text("51–67 of 67", exact=True)).to_be_visible()
    expect(page.get_by_role("button", name=re.compile(r"^All\s*67$"))).to_be_visible()
    with expect_rpc(
        page,
        "ListTraces",
        page_request(sort="TRACE_SUMMARY_SORT_DURATION", ascending=None, offset=None),
    ):
        page.get_by_role("button", name="Duration", exact=True).click()
    assert last_page_request().get("offset", 0) == 0
    assert last_page_request()["sort"] == "TRACE_SUMMARY_SORT_DURATION"
    assert not last_page_request().get("ascending")
    with expect_rpc(
        page,
        "ListTraces",
        page_request(sort="TRACE_SUMMARY_SORT_DURATION", ascending=True),
    ):
        page.get_by_role("button", name="Duration", exact=True).click()
    assert last_page_request()["ascending"] is True
    for label, key in [
        ("Root span", "OPERATION"),
        ("Resource", "RESOURCE"),
        ("Status", "STATUS"),
        ("Spans", "SPAN_COUNT"),
    ]:
        with expect_rpc(
            page,
            "ListTraces",
            page_request(sort="TRACE_SUMMARY_SORT_" + key, ascending=None),
        ):
            page.get_by_role("button", name=label, exact=True).click()
        assert last_page_request()["sort"] == "TRACE_SUMMARY_SORT_" + key
        assert not last_page_request().get("ascending")
        with expect_rpc(
            page,
            "ListTraces",
            page_request(sort="TRACE_SUMMARY_SORT_" + key, ascending=True),
        ):
            page.get_by_role("button", name=label, exact=True).click()
        assert last_page_request()["ascending"] is True
    sent = len(recorded)
    with (
        expect_rpc(page, "ListTraces", page_request(query="@status:error")),
        expect_rpc(
            page,
            "QueryTraceBuckets",
            lambda body: body.get("query") == "@status:error",
        ),
    ):
        page.get_by_role("button", name=re.compile(r"^Errors")).click()
    assert last_page_request()["query"] == "@status:error"
    expect(page.get_by_role("button", name=re.compile(r"^All\s*67$"))).to_be_visible()
    lists = [row for row in recorded[sent:] if "limit" in row]
    assert len(lists) == 1, lists
    # The All count comes from the unfiltered histogram the All tab already loaded.
    histograms = [row for row in recorded[sent:] if "intervalSeconds" in row]
    assert len(histograms) == 1, histograms
    wait_for_query(page, lambda query: query.get("q") == ["@status:error"])
    with expect_rpc(
        page,
        "ListTraces",
        lambda body: body.get("query") == "@duration:>100" and body.get("limit") == 25,
    ):
        page.goto(state["ui_url"] + base + "&q=" + urllib.parse.quote("@duration:>100"))
    wait_for_query(page, lambda query: query.get("q") == ["@duration:>100"])
    token_remove = page.get_by_role("button", name="Remove duration >100", exact=True)
    expect(token_remove).to_be_visible()
    expect(page.get_by_role("heading", level=1)).to_have_text("Traces")
    page.goto(state["ui_url"] + base)
    chart = page.get_by_role(
        "img",
        name=re.compile(r"^Traces per \d+-second bucket, 67 in range with 1 errors"),
    )
    expect(chart).to_be_visible()
    expect(page.get_by_text("1.5% errors", exact=True)).to_be_visible()
    expect(page.get_by_text("360ms", exact=True)).to_be_visible()
    box = chart.bounding_box()
    chart.hover(position={"x": box["width"] - 2, "y": box["height"] / 2})
    clock = r"\d\d:\d\d(:\d\d)?–\d\d:\d\d(:\d\d)?"
    tooltips = page.get_by_role("tooltip")
    expect(tooltips).to_have_count(2)
    for tooltip in (tooltips.nth(0), tooltips.nth(1)):
        expect(tooltip).to_have_text(
            re.compile(f"^{clock}Traces5Errors1p5080msp95360ms$")
        )
    chart.hover(position={"x": 2, "y": 2})
    page.mouse.down()
    page.mouse.move(
        box["x"] + box["width"] - 2, box["y"] + box["height"] * 0.8, steps=5
    )
    with page.expect_request(
        lambda request: request.url.endswith("/QueryTraceBuckets")
    ) as zoom_request:
        page.mouse.up()
    assert "@duration" not in zoom_request.value.post_data_json.get("query", ""), (
        zoom_request.value.post_data_json
    )
    expect(page).to_have_url(re.compile(r"[?&]from="))
    zoomed = urllib.parse.parse_qs(urllib.parse.urlparse(page.url).query)
    assert "from" in zoomed and "to" in zoomed, page.url
    assert "@duration" not in zoomed.get("q", [""])[0], zoomed
    page.get_by_role("button", name="Reset zoom", exact=True).click()
    expect(page).not_to_have_url(re.compile(r"(?:%40|@)duration|[?&]from="))
    assert "@duration" not in urllib.parse.unquote(page.url)
    assert "from=" not in page.url
    expect(page.get_by_role("button", name="Reset zoom", exact=True)).to_have_count(0)
    with expect_rpc(
        page,
        "ListTraces",
        lambda body: body.get("query") == "products",
    ) as filtered:
        page.goto(state["ui_url"] + base + "&q=products")
    assert filtered.value.request.post_data_json["query"] == "products"
    expect(
        page.get_by_role("row", name="Open trace GET /api/products", exact=True)
    ).to_be_visible()
    context.close()
    mode[0] = "outlier"
    context, page, errors = open_page(browser, state=state, now=now)
    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
    page.goto(state["ui_url"] + base)
    latency = page.get_by_role("img", name=re.compile(r"^p50 and p95 trace duration"))
    expect(latency).to_be_visible()
    p50_lines = latency.locator("path.stroke-info")
    expect(p50_lines.first).to_have_attribute("d", re.compile(r"\d"))
    p50_points = [
        point
        for path in p50_lines.all()
        for point in re.findall(r"[\d.]+,[\d.]+", path.get_attribute("d"))
    ]
    p50_heights = [100 - float(point.split(",")[1]) for point in p50_points]
    assert min(p50_heights) > 10, p50_heights
    assert not errors, errors
    context.close()
    mode[0] = "dated"
    context, page, errors = open_page(browser, state=state, now=now)
    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
    page.goto(state["ui_url"] + base)
    rows = page.get_by_role("region", name="Trace list").get_by_role(
        "row", name=re.compile("^Open trace ")
    )
    expect(rows).to_have_count(7)
    expect(rows.last).to_contain_text(re.compile(r"^[A-Z][a-z]{2} \d+, \d\d:\d\d"))
    expect(rows.first).not_to_contain_text(re.compile(r"^[A-Z][a-z]{2} \d+, "))
    for index in range(7):
        expect(rows.nth(index)).to_contain_text(re.compile(r"(OK|Error)\d"))
    # Columns keep their widths and the row ends in an empty tail.
    assert rows.first.evaluate(
        """row => Math.round(row.children[2].getBoundingClientRect().width) === 260
        && row.getBoundingClientRect().right - row.lastElementChild.getBoundingClientRect().right > 20"""
    )
    context.close()
    mode[0] = "normal"
    environments = call(
        state,
        "loco.environment.v1.EnvironmentService/ListEnvironments",
        {"workspaceId": workspace_id},
    )["environments"]
    staging = next(
        (
            environment["id"]
            for environment in environments
            if environment["name"] == "staging"
        ),
        None,
    )
    if staging is None:
        staging = call(
            state,
            "loco.environment.v1.EnvironmentService/CreateEnvironment",
            {
                "workspaceId": workspace_id,
                "name": "staging",
            },
        )["environmentId"]
    context, page, _ = open_page(
        browser,
        state=state,
        now=now,
        storage={"loco:env:v1:" + workspace_id: "staging"},
    )
    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
    with expect_rpc(
        page,
        "GetTrace",
        lambda body: body.get("scope", {}).get("environmentId") == staging,
    ):
        page.goto(state["ui_url"] + base + "&trace=" + trace)
    wait_for_query(
        page,
        lambda query: query.get("env") == ["staging"] and query.get("trace") == [trace],
    )
    shared_url = page.url
    assert "env=staging" in shared_url
    assert recorded[-1]["scope"]["environmentId"] == staging
    context.close()
    context, page, _ = open_page(browser, state=state, now=now)
    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
    with expect_rpc(
        page,
        "GetTrace",
        lambda body: body.get("scope", {}).get("environmentId") == staging,
    ):
        page.goto(shared_url)
    assert recorded[-1]["scope"]["environmentId"] == staging
    context.close()
    raw_service = "resource-" + workspace_id
    spans[0]["serviceName"] = raw_service
    spans[0]["resourceAttributes"]["service.name"] = raw_service
    context, page, _ = open_page(browser, narrow=True, state=state, now=now)
    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
    page.goto(
        state["ui_url"] + base + "&trace=" + trace + "&span=" + spans[0]["spanId"]
    )
    page.get_by_role("dialog", name=root_name).get_by_role(
        "region", name="Span attributes"
    ).get_by_role("tab", name="Telemetry", exact=True).click()
    page.get_by_role("dialog", name=root_name).get_by_role(
        "region", name="Span attributes"
    ).get_by_text(raw_service, exact=True).first.wait_for()
    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
    meta = page.get_by_role("dialog", name=root_name).locator("[data-meta-line]")
    assert len(line_starts(meta)) > 1, "the trace meta line fits on one line"
    assert "·" not in line_starts(meta), line_starts(meta)
    context.close()
    mode[0] = "normal"
    context, page, _ = open_page(browser, state=state, now=now)
    original_spans = json.loads(json.dumps(spans))
    unmapped_failing = failing
    failing = re.compile(
        r"^checkout \(payment\) POST \/payments\/authorize, .*, error$"
    )
    for row in spans:
        row["resourceId"] = workspace_id
        row["resourceAttributes"].update(
            {
                "k8s.pod.name": "checkout-abc-123",
                "k8s.node.name": "worker-1",
                "k8s.pod.start_time": time.strftime(
                    "%Y-%m-%dT%H:%M:%SZ", time.gmtime(start - 3600)
                ),
                "loco.region": "us-ca-1",
            }
        )
    spans[6]["resourceAttributes"].pop("k8s.pod.name")
    spans[5]["resourceAttributes"]["k8s.pod.name"] = "catalog-def-456"
    context.route(
        "**/loco.resource.v1.ResourceService/ListWorkspaceResources",
        lambda route: route.fulfill(
            json={
                "resources": [
                    {
                        "id": workspace_id,
                        "workspaceId": workspace_id,
                        "name": "checkout",
                        "slug": "checkout",
                        "type": "RESOURCE_TYPE_SERVICE",
                        "spec": {
                            "service": {
                                "regions": {
                                    "us-ca-1": {
                                        "enabled": True,
                                        "cpu": "500m",
                                        "memory": "512Mi",
                                    }
                                }
                            }
                        },
                    }
                ]
            }
        ),
    )

    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
    log_entries = [
        {
            "timestamp": stamp(0.000456),
            "level": "LOG_LEVEL_INFO",
            "body": "Trace started",
            "resourceId": workspace_id,
            "traceId": trace,
            "spanId": spans[0]["spanId"],
        },
        {
            "timestamp": stamp(172),
            "level": "LOG_LEVEL_ERROR",
            "body": "Payment request failed",
            "resourceId": workspace_id,
            "traceId": trace,
            "spanId": spans[10]["spanId"],
        },
        {
            "timestamp": stamp(35),
            "level": "LOG_LEVEL_INFO",
            "body": "Product lookup complete",
            "resourceId": workspace_id,
            "traceId": trace,
            "spanId": spans[5]["spanId"],
        },
    ]
    log_requests = []

    def trace_logs(route):
        log_requests.append(route.request.post_data_json)
        route.fulfill(
            json={"entries": log_entries, "totalMatched": str(len(log_entries))}
        )

    context.route("**/loco.observability.v1.ObservabilityService/QueryLogs", trace_logs)
    metric_requests = []
    metric_mode = ["normal"]

    def metrics(route):
        body = route.request.post_data_json
        metric_requests.append(body)
        if metric_mode[0] == "error":
            route.fulfill(
                status=500,
                json={"code": "internal", "message": "Replica metrics unavailable"},
            )
            return
        if metric_mode[0] == "empty":
            route.fulfill(json={"series": []})
            return
        cpu_share = [0.3, 0.92, 0.45]
        memory_share = [0.4, 0.6, 0.5]
        values = (
            [share * 0.5 for share in cpu_share]
            if body["metricName"] == "k8s.pod.cpu.usage"
            else cpu_share
            if body["metricName"] == "k8s.pod.cpu_limit_utilization"
            else [share * 512 * 1024 * 1024 for share in memory_share]
            if body["metricName"] == "k8s.pod.memory.working_set"
            else memory_share
            if body["metricName"] == "k8s.pod.memory.working_set_limit_utilization"
            else [1024, 2048, 512]
            if body["attributes"].get("direction") == "receive"
            else [512, 1024, 256]
        )

        def points(scale):
            return [
                {
                    "timestamp": time.strftime(
                        "%Y-%m-%dT%H:%M:%SZ", time.gmtime(start + offset)
                    ),
                    "value": value * scale,
                }
                # The API buckets from before the window, so the first point lies outside it.
                for offset, value in zip(
                    (-900, -60, 60, 120), [values[0], *values], strict=True
                )
            ]

        route.fulfill(
            json={
                "series": [
                    {
                        "resourceId": workspace_id,
                        "labels": {"k8s.pod.name": pod},
                        "points": points(scale),
                    }
                    for pod, scale in (
                        ("other-pod-999", 0.1),
                        ("checkout-abc-123", 1),
                        ("catalog-def-456", 0.5),
                    )
                ]
            }
        )

    context.route("**/loco.observability.v1.ObservabilityService/QueryMetrics", metrics)

    event_requests = []

    def events(route):
        body = route.request.post_data_json
        event_requests.append(body)

        def event(seq, kind, offset, data, title):
            return {
                "seq": str(seq),
                "id": f"00000000-0000-4000-8000-00000000000{seq}",
                "type": kind,
                "title": title,
                "severity": "normal",
                "region": data.get("region", ""),
                # The Events view lists the whole workspace; the trace drawer names its resources.
                "resourceId": (body.get("resourceIds") or [""])[0],
                "environmentId": body.get("environmentId", ""),
                "createdAt": time.strftime(
                    "%Y-%m-%dT%H:%M:%SZ", time.gmtime(start + offset)
                ),
                "data": data,
            }

        window = (
            iso_seconds(body.get("startTime", "1970-01-01T00:00:00Z")),
            iso_seconds(body.get("endTime", "9999-01-01T00:00:00Z")),
        )
        route.fulfill(
            json={
                "events": [
                    item
                    for item in [
                        event(
                            2,
                            "resource.restarted",
                            -120,
                            {
                                "region": "us-ca-1",
                                "message": "Container exited with code 137",
                            },
                            "Restarted in us-ca-1",
                        ),
                        event(
                            1,
                            "deployment.ready",
                            -1800,
                            {"region": "us-ca-1"},
                            "Deploy ready in us-ca-1",
                        ),
                    ]
                    if window[0] <= iso_seconds(item["createdAt"]) < window[1]
                ]
            }
        )

    context.route("**/loco.event.v1.EventService/ListEvents", events)
    page.mouse.move(0, 0)
    page.goto(state["ui_url"] + base + "&trace=" + trace)
    drawer = page.get_by_role("dialog", name=root_name)
    expect(drawer.locator("[data-meta-line]").get_by_role("link")).to_have_text(
        "checkout"
    )
    log_rows = drawer.get_by_role("region", name="Logs in this trace")
    expect(
        log_rows.get_by_role("button", name="Open log: Payment request failed")
    ).to_be_visible()
    expect(
        drawer.get_by_role("button", name=re.compile("^Log at .*Trace started$"))
    ).to_be_visible()
    expect(
        log_rows.get_by_role("button", name="Open log: Trace started")
    ).to_contain_text("+333ns")
    failing_button = drawer.get_by_role("region", name="Flame graph").get_by_role(
        "button", name=failing
    )
    assert len(log_requests) == 1, log_requests
    assert log_requests[0]["query"] == "@trace:" + trace, log_requests
    payment_log = log_rows.get_by_role(
        "button", name="Open log: Payment request failed"
    )
    expect(failing_button).not_to_have_attribute("data-linked", "true")
    payment_log.hover()
    expect(failing_button).to_have_attribute("data-linked", "true")
    assert payment_log.evaluate(
        "async row => { await Promise.all(row.getAnimations().map(a => a.finished)); return getComputedStyle(row).boxShadow.split(/,(?![^(]*\\))/).every(part => {const x = part.replace(/[a-z]+\\([^)]*\\)/g, '').match(/-?[0-9.]+px/); return x === null || parseFloat(x[0]) === 0}) }"
    ), payment_log.evaluate("row => getComputedStyle(row).boxShadow")
    # Clear enter-driven state while keeping the pointer inside the row. The next
    # native movement must restore linking without another mouseenter event.
    payment_log.dispatch_event("mouseout")
    expect(failing_button).not_to_have_attribute("data-linked", "true")
    payment_log.hover(position={"x": 20, "y": 10})
    expect(failing_button).to_have_attribute("data-linked", "true")
    marker = drawer.get_by_role(
        "button", name=re.compile("^Log at .*Payment request failed$")
    )
    marker.click()
    expect(page).to_have_url(re.compile("span=" + spans[10]["spanId"]))
    expect(drawer.get_by_role("complementary", name="Log line")).to_be_visible()
    expect(
        drawer.get_by_role("button", name=re.compile("^Add .* as a column$"))
    ).to_have_count(0)
    drawer.get_by_role("complementary", name="Log line").get_by_role(
        "button", name="Close", exact=True
    ).click()
    scope_box = drawer.get_by_role("group", name="Which logs").bounding_box()
    tabs_box = drawer.get_by_role("tablist", name="Trace bottom panel").bounding_box()
    collapse_box = drawer.get_by_role(
        "button", name="Collapse bottom panel"
    ).bounding_box()
    assert scope_box["x"] - tabs_box["x"] - tabs_box["width"] > 100
    assert collapse_box["x"] - scope_box["x"] - scope_box["width"] < 140
    drawer.get_by_role("button", name="This span", exact=True).click()
    expect(log_rows.get_by_role("button")).to_have_count(1)
    drawer.get_by_role("region", name="Flame graph").get_by_role(
        "button", name=re.compile(r"^checkout \(currency\) Convert")
    ).click()
    expect(
        log_rows.get_by_text("No logs from this span or its children.", exact=True)
    ).to_be_visible()
    resize = drawer.get_by_role("separator", name="Resize bottom panel")
    height = int(resize.get_attribute("aria-valuenow"))
    resize.press("ArrowUp")
    expect(resize).not_to_have_attribute("aria-valuenow", str(height))
    assert int(resize.get_attribute("aria-valuenow")) > height
    drawer.get_by_role("button", name="Collapse bottom panel").click()
    expect(drawer.get_by_role("button", name="Expand bottom panel")).to_be_visible()
    assert page.evaluate(
        "JSON.parse(localStorage.getItem('loco-layout'))['trace.bottomCollapsed']"
    )
    drawer.get_by_role("button", name="Expand bottom panel").click()
    drawer.get_by_role("tab", name="Infrastructure", exact=True).click()
    expect(drawer.get_by_role("tablist", name="Trace bottom panel")).to_be_visible()
    expect(
        drawer.get_by_role("tab", name="Infrastructure", exact=True)
    ).to_have_attribute("aria-selected", "true")
    events_tab = drawer.get_by_role("tab", name=re.compile("^Events"))
    drawer.get_by_role("tab", name="Infrastructure", exact=True).press("ArrowLeft")
    expect(events_tab).to_be_focused()
    events_tab.press("ArrowLeft")
    expect(drawer.get_by_role("tab", name=re.compile("^Logs "))).to_be_focused()
    drawer.get_by_role("tab", name=re.compile("^Logs ")).press("Enter")
    expect(drawer.get_by_role("tab", name=re.compile("^Logs "))).to_have_attribute(
        "aria-selected", "true"
    )
    drawer.get_by_role("tab", name=re.compile("^Logs ")).press("ArrowRight")
    expect(events_tab).to_be_focused()
    events_tab.press("ArrowRight")
    expect(drawer.get_by_role("tab", name="Infrastructure", exact=True)).to_be_focused()
    drawer.get_by_role("tab", name="Infrastructure", exact=True).press("Enter")
    expect(
        drawer.get_by_role("tabpanel", name="Infrastructure", exact=True)
    ).to_be_visible()
    infrastructure = drawer.get_by_role("region", name="Trace infrastructure")
    expect(
        infrastructure.get_by_text(
            "No replica identity was recorded for this span.", exact=True
        )
    ).to_be_visible()
    drawer.get_by_role("region", name="Flame graph").get_by_role(
        "button", name=failing
    ).click()
    expect(
        infrastructure.get_by_text(re.compile("Up .* when this ran"))
    ).to_be_visible()
    cpu = infrastructure.get_by_role(
        "region", name="CPU for checkout-abc-123", exact=True
    )
    memory = infrastructure.get_by_role(
        "region", name="Memory for checkout-abc-123", exact=True
    )
    network = infrastructure.get_by_role(
        "region", name="Network for checkout-abc-123", exact=True
    )
    for chart, title in ((cpu, "CPU"), (memory, "Memory"), (network, "Network")):
        expect(
            chart.get_by_role("group", name=title + " usage", exact=True)
        ).to_be_visible()
    expect(cpu.get_by_label("CPU at trace start")).to_have_text("0.15 vCPU")
    expect(cpu.get_by_text("0.46 vCPU", exact=True)).to_be_visible()
    expect(cpu).to_contain_text("near the limit")
    expect(memory.get_by_label("Memory at trace start")).to_have_text("205 Mi")
    expect(memory.get_by_text("307 Mi", exact=True)).to_be_visible()
    expect(memory).not_to_contain_text("near the limit")
    expect(cpu.get_by_text("limit 0.5 vCPU", exact=True)).to_be_visible()
    expect(memory.get_by_text("limit 512 Mi", exact=True)).to_be_visible()
    expect(network.get_by_text(re.compile("^limit "))).to_have_count(0)
    for chart, title in ((cpu, "CPU"), (memory, "Memory"), (network, "Network")):
        marks = chart.get_by_role("group", name="Events on " + title, exact=True)
        expect(marks.get_by_role("button")).to_have_count(1)
        expect(
            marks.get_by_role("button", name=re.compile("^Restarted in us-ca-1, "))
        ).to_be_visible()
    grid = cpu.locator(".recharts-cartesian-grid").bounding_box()
    band = cpu.locator(".recharts-reference-area").bounding_box()
    mark = cpu.get_by_role(
        "button", name=re.compile("^Restarted in us-ca-1, ")
    ).bounding_box()
    # The restart is 120s before the trace in a ±10m window; the sub-second trace adds under a pixel.
    expected = band["x"] - grid["width"] * 120 / 1200
    assert abs(mark["x"] + mark["width"] / 2 - expected) < 3, (mark, band, grid)
    assert len(metric_requests) == 6, metric_requests
    assert all(
        request["resourceIds"] == [workspace_id] and request["byPod"]
        for request in metric_requests
    )
    assert {request["metricName"] for request in metric_requests} == {
        "k8s.pod.cpu.usage",
        "k8s.pod.cpu_limit_utilization",
        "k8s.pod.memory.working_set",
        "k8s.pod.memory.working_set_limit_utilization",
        "k8s.pod.network.io",
    }
    network_requests = [
        request
        for request in metric_requests
        if request["metricName"] == "k8s.pod.network.io"
    ]
    assert {request["attributes"]["direction"] for request in network_requests} == {
        "receive",
        "transmit",
    }
    assert all(request["aggregation"] == "rate" for request in network_requests)
    grid = cpu.locator(".recharts-cartesian-grid").bounding_box()
    page.mouse.move(
        grid["x"] + grid["width"] * 0.55, grid["y"] + grid["height"] / 2, steps=3
    )
    expect(cpu.get_by_label("CPU at cursor")).to_contain_text("0.46 vCPU at ")
    expect(cpu.locator("output")).to_have_count(1)
    expect(memory.locator("output")).to_have_count(1)
    expect(cpu.get_by_label("CPU at trace start")).to_have_count(0)
    assert network.locator(".recharts-yAxis-tick-labels text").all_text_contents()
    assert all(
        label.endswith("B/s")
        for label in network.locator(
            ".recharts-yAxis-tick-labels text"
        ).all_text_contents()
    )
    expect(memory.get_by_label("Memory at cursor")).to_contain_text("307 Mi at ")
    expect(network.get_by_label("Network at cursor")).to_contain_text(" B/s at ")
    cursor_times = [
        chart.get_by_label(title + " at cursor").inner_text().split(" at ")[-1]
        for chart, title in ((cpu, "CPU"), (memory, "Memory"), (network, "Network"))
    ]
    assert len(set(cursor_times)) == 1, cursor_times
    page.mouse.move(0, 0)
    expect(cpu.get_by_label("CPU at trace start")).to_have_text("0.15 vCPU")
    expect(memory.get_by_label("Memory at trace start")).to_have_text("205 Mi")
    window = infrastructure.get_by_role("group", name="Infrastructure window")
    expect(window.get_by_role("button", name="±10m", exact=True)).to_have_attribute(
        "aria-pressed", "true"
    )
    window.get_by_role("button", name="±1h", exact=True).click()
    expect(
        network.get_by_role("group", name="Network usage", exact=True)
    ).to_be_visible()
    assert len(metric_requests) == 12, metric_requests
    assert all(
        7200
        <= iso_seconds(request["endTime"]) - iso_seconds(request["startTime"])
        <= 7201
        for request in metric_requests[6:]
    )
    expect(
        cpu.get_by_role("group", name="Events on CPU", exact=True).get_by_role("button")
    ).to_have_count(2)
    drawer.get_by_role("region", name="Flame graph").get_by_role(
        "button", name=re.compile(r"^checkout \(product-catalog\) GetProduct")
    ).click()
    expect(window.get_by_role("button", name="±1h", exact=True)).to_have_attribute(
        "aria-pressed", "true"
    )
    expect(
        infrastructure.get_by_role("group", name="Network usage", exact=True)
    ).to_be_visible()
    assert all(request["byPod"] for request in metric_requests), metric_requests
    catalog_cpu = infrastructure.get_by_role(
        "region", name="CPU for catalog-def-456", exact=True
    )
    expect(catalog_cpu.get_by_label("CPU at trace start")).to_have_text("0.075 vCPU")
    expect(catalog_cpu.get_by_text("0.23 vCPU", exact=True)).to_be_visible()
    expect(catalog_cpu).not_to_contain_text("near the limit")
    page.set_viewport_size({"width": 390, "height": 844})
    infrastructure.get_by_role(
        "region", name="Network for catalog-def-456", exact=True
    ).scroll_into_view_if_needed()
    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
    page.set_viewport_size({"width": 1280, "height": 800})
    metric_mode[0] = "empty"
    window.get_by_role("button", name="±5m", exact=True).click()
    expect(
        infrastructure.get_by_text("No metrics in this range", exact=True)
    ).to_have_count(3)
    metric_mode[0] = "error"
    window.get_by_role("button", name="±30m", exact=True).click()
    expect(infrastructure.get_by_role("alert").first).to_contain_text(
        "Replica metrics unavailable", timeout=20000
    )
    metric_mode[0] = "normal"
    window.get_by_role("button", name="±10m", exact=True).click()
    expect(
        infrastructure.get_by_role("group", name="Network usage", exact=True)
    ).to_be_visible()
    expect(events_tab).to_have_text(re.compile(r"^Events\s*1$"))
    assert any(
        "endTime" in request
        and iso_seconds(request["endTime"]) - iso_seconds(request["startTime"]) >= 1200
        for request in event_requests
    ), event_requests
    events_tab.click()
    trace_events = drawer.get_by_role("region", name="Trace events")
    restarted = trace_events.get_by_role(
        "button", name="Open Restarted in us-ca-1 in Events", exact=True
    )
    expect(restarted).to_contain_text("Container exited with code 137")
    expect(restarted).to_contain_text("checkout")
    expect(restarted).to_contain_text("2m before")
    expect(trace_events.get_by_role("listitem")).to_have_count(1)
    trace_events.get_by_role("group", name="Events window").get_by_role(
        "button", name="±1h", exact=True
    ).click()
    expect(trace_events.get_by_role("listitem")).to_have_count(2)
    expect(events_tab).to_have_text(re.compile(r"^Events\s*2$"))
    drawer.get_by_role("tab", name="Infrastructure", exact=True).click()
    expect(window.get_by_role("button", name="±1h", exact=True)).to_have_attribute(
        "aria-pressed", "true"
    )
    drawer.get_by_role("tab", name=re.compile("^Logs ")).click()
    assert len(log_requests) == 1, log_requests
    marker.click()
    drawer.get_by_role("complementary", name="Log line").get_by_role(
        "button", name="More for this line"
    ).click()
    page.get_by_role("menuitem", name=re.compile("^Logs around this line")).click()
    wait_for_query(
        page,
        lambda query: (
            query.get("view") == ["logs"] and query.get("q") == ["@trace:" + trace]
        ),
    )
    page.go_back()
    marker.click()
    drawer.get_by_role("complementary", name="Log line").get_by_role(
        "button", name="Only level error", exact=True
    ).click()
    wait_for_query(
        page,
        lambda query: (
            query.get("view") == ["logs"]
            and "@trace:" + trace in query.get("q", [""])[0]
            and "@level:error" in query.get("q", [""])[0]
        ),
    )
    page.goto(state["ui_url"] + base + "&trace=" + trace)
    drawer = page.get_by_role("dialog", name=root_name)
    drawer.get_by_role("tab", name=re.compile("^Events")).click()
    drawer.get_by_role("region", name="Trace events").get_by_role(
        "button", name="Open Restarted in us-ca-1 in Events", exact=True
    ).click()
    wait_for_query(
        page,
        lambda query: (
            query.get("view") == ["events"] and query.get("q") == ["@resource:checkout"]
        ),
    )
    page.set_viewport_size({"width": 390, "height": 844})
    page.goto(state["ui_url"] + base + "&trace=" + trace)
    drawer = page.get_by_role("dialog", name=root_name)
    narrow_flame = drawer.get_by_role("region", name="Flame graph")
    expect(narrow_flame.get_by_role("button", name=failing)).to_be_visible()
    assert narrow_flame.bounding_box()["height"] > 100
    narrow_flame.hover(position={"x": 30, "y": 30})
    ancestors = """el => {const values=[]; for(let p=el.parentElement;p;p=p.parentElement) values.push(p.scrollTop); return values;}"""
    before_hover = narrow_flame.evaluate(ancestors)
    box = narrow_flame.bounding_box()
    page.mouse.move(box["x"] + 200, box["y"] + 40, steps=20)
    assert narrow_flame.evaluate(ancestors) == before_hover

    message = (
        drawer.get_by_role("button", name="Open log: Payment request failed")
        .locator(":scope > span")
        .last
    )
    assert message.bounding_box()["width"] > 200
    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
    context.close()
    spans[:] = original_spans
    failing = unmapped_failing
    mode[0] = "zero"
    context, page, errors = open_page(browser, state=state, now=now)
    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
    page.goto(state["ui_url"] + base + "&trace=" + trace)
    zero = (
        page.get_by_role("dialog", name=root_name)
        .get_by_role("region", name="Flame graph")
        .get_by_role("button")
        .first
    )
    expect(zero).to_be_visible()
    assert zero.bounding_box()["width"] > 0
    zero.click()
    expect(page).to_have_url(re.compile("span=" + spans[0]["spanId"]))
    assert not errors, errors
    context.close()
    mode[0] = "large"
    context, page, errors = open_page(browser, state=state, now=now)
    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
    page.goto(state["ui_url"] + base + "&trace=" + trace)
    drawer = page.get_by_role("dialog", name=root_name)
    large_flame = drawer.get_by_role("region", name="Flame graph")
    expect(large_flame.get_by_role("button")).to_have_count(3001)
    large_flame.get_by_role("button").first.hover()
    page.mouse.wheel(0, -400)
    expect(drawer.get_by_role("button", name="Reset zoom", exact=True)).to_be_visible()
    assert not errors, errors
    context.close()
    mode[0] = "many"
    context, page, _ = open_page(browser, state=state, now=now)
    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
    page.goto(state["ui_url"] + base + "&trace=" + trace)
    flame = page.get_by_role("dialog", name=root_name).get_by_role(
        "region", name="Flame graph"
    )
    expect(flame.get_by_role("button")).to_have_count(114)
    many_drawer = page.get_by_role("dialog", name=root_name)
    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
    for width, height in ((1280, 600), (390, 844)):
        page.set_viewport_size({"width": width, "height": height})
        many_drawer.get_by_role("button", name="Waterfall", exact=True).click()
        waterfall = many_drawer.get_by_role("region", name="Waterfall")
        expect(waterfall.locator("[data-span]").first).to_be_visible()
        waterfall.hover(position={"x": 150, "y": 120})
        for _ in range(8):
            page.mouse.wheel(0, 500)
            page.wait_for_timeout(50)
        for label in ("Trace view", "Waterfall"):
            top = (
                many_drawer.get_by_role("group", name=label)
                if label == "Trace view"
                else waterfall.get_by_text("Span", exact=True).first
            )
            top_box = top.bounding_box()
            drawer_box = many_drawer.bounding_box()
            assert top_box["y"] >= drawer_box["y"], (width, label, top_box)
            assert (
                top_box["y"] + top_box["height"]
                <= drawer_box["y"] + drawer_box["height"]
            )
        header_box = many_drawer.locator("[data-slot=trace-header]").bounding_box()
        assert header_box["y"] >= many_drawer.bounding_box()["y"]
        span_head = waterfall.get_by_text("Span", exact=True).first.bounding_box()
        row_box = waterfall.locator("[data-span]").first.bounding_box()
        assert row_box["y"] < span_head["y"], (width, row_box, span_head)
        view_tabs = many_drawer.get_by_role("group", name="Trace view")
        assert (
            view_tabs.bounding_box()["y"] < header_box["y"] + header_box["height"] + 80
        )
    context.close()
    mode[0] = "normal"
    context, page, _ = open_page(browser, state=state, now=now)
    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
    recent_save_ms = 1500
    search = page.get_by_role("combobox", name="Search and filter")
    suggestions = page.get_by_role("listbox", name="Suggestions")
    recent = suggestions.get_by_role("option").filter(has_text="checkout")
    page.goto(state["ui_url"] + base + "&q=checkout")
    expect(search).to_have_value("checkout")
    page.clock.run_for(recent_save_ms)
    page.goto(state["ui_url"] + base + "&q=quick")
    page.get_by_role("button", name="Clear", exact=True).click()
    page.clock.run_for(recent_save_ms)
    search.click()
    expect(recent).to_have_count(1)
    expect(suggestions.get_by_role("option").filter(has_text="quick")).to_have_count(0)
    page.goto(state["ui_url"] + base.replace("view=traces", "view=logs"))
    search.click()
    expect(suggestions.get_by_role("option").filter(has_text="checkout")).to_have_count(
        0
    )
    page.goto(state["ui_url"] + base)
    search.click()
    expect(recent).to_have_count(1)
    page.get_by_role("button", name="Clear recent searches", exact=True).click()
    expect(recent).to_have_count(0)
    page.reload()
    search.click()
    expect(recent).to_have_count(0)
    search.press_sequentially("chec")
    expect(
        suggestions.get_by_role("option", name="Search root span names for “chec”")
    ).to_be_visible()
    expect(page.locator('[data-slot="combobox-content"]')).to_contain_text(
        "@duration:>500 compare"
    )
    expect(page.get_by_role("button", name="Pin this search")).to_have_count(0)
    search.fill("")
    page.keyboard.press("Escape")
    expect(page.get_by_role("button", name="Pin this search")).to_have_count(0)
    page.goto(state["ui_url"] + base + "&q=checkout")
    expect(search).to_have_value("checkout")
    page.clock.run_for(recent_save_ms)
    page.get_by_role("button", name="Pin this search").click()
    expect(
        page.locator("[data-sonner-toast]").filter(has_text="Pinned")
    ).to_be_visible()
    page.goto(state["ui_url"] + base)
    search.click()
    expect(recent).to_have_count(1)
    suggestions.get_by_role("button", name="Unpin checkout").click()
    expect(suggestions.get_by_role("button", name="Remove checkout")).to_be_visible()
    suggestions.get_by_role("button", name="Remove checkout").click()
    expect(recent).to_have_count(0)
    context.close()
    mode[0] = "concurrent"
    for narrow in (False, True):
        context, page, errors = open_page(browser, narrow=narrow, state=state, now=now)
        context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
        page.goto(state["ui_url"] + base + "&trace=" + trace)
        drawer = page.get_by_role("dialog", name="GET /recommendations")
        flame = drawer.get_by_role("region", name="Flame graph")
        expect(flame.get_by_role("button")).to_have_count(len(concurrent_spans))
        ranking = drawer.get_by_role("group", name="Time spent per resource")
        more = ranking.get_by_role("button", name=re.compile(r"^\+\d+ more$"))
        if more.count():
            more.click()
        shares = ranking.get_by_role("button").filter(has_text=re.compile(r"\(.*%\)"))
        texts = shares.all_inner_texts()
        spent = sum(
            milliseconds(re.search(r"([0-9.]+(?:ms|µs|ns|s)) \(", t).group(1))
            for t in texts
        )
        percent = sum(int(re.search(r"\((<?)(\d+)%\)", t).group(2)) for t in texts)
        assert spent <= 10.001 and percent <= 101, (texts, spent, percent)
        boxes = flame.get_by_role("button").evaluate_all(
            "(buttons) => buttons.map(b => {const r=b.getBoundingClientRect(); return {x:r.x,y:r.y,right:r.right,bottom:r.bottom}})"
        )
        for i, a in enumerate(boxes):
            for b in boxes[i + 1 :]:
                assert (
                    a["bottom"] <= b["y"]
                    or b["bottom"] <= a["y"]
                    or a["right"] <= b["x"] + 0.1
                    or b["right"] <= a["x"] + 0.1
                ), (a, b)
        assert flame.get_by_role("button").evaluate_all(
            "buttons => buttons.every(b => [...b.querySelectorAll('span')].every(s => {const r=s.getBoundingClientRect(),p=b.getBoundingClientRect();return r.width===0 || (r.x>=p.x-1 && r.right<=p.right+1 && r.y>=p.y-1 && r.bottom<=p.bottom+1)}))"
        )
        drawer.get_by_role("button", name="Waterfall", exact=True).click()
        waterfall = drawer.get_by_role("region", name="Waterfall")
        expect(waterfall.locator("[data-span]")).to_have_count(len(concurrent_spans))
        assert waterfall.locator("[data-span]").evaluate_all(
            "buttons => buttons.every(b => b.clientHeight === b.scrollHeight)"
        )
        assert waterfall.locator("button > span.absolute").evaluate_all(
            "bars => bars.every(b => b.clientHeight >= b.scrollHeight)"
        )
        assert not errors, errors
        context.close()
    mode[0] = "normal"
    marker_logs = [
        {
            "timestamp": stamp(offset),
            "level": "LOG_LEVEL_INFO",
            "body": body,
            "resourceId": workspace_id,
            "traceId": trace,
            "spanId": spans[index]["spanId"],
        }
        for offset, index, body in (
            (0.5, 0, "Proxy accepted"),
            (35, 5, "Product found"),
            (172, 10, "Payment declined"),
            (300, 12, "Access log " + "GET /api/products/OLJCESPC7Z 200 " * 12),
            *((0.5, 0, f"Proxy filler {n}") for n in range(30)),
        )
    ]
    for font in ("16px", "20px"):
        context, page, errors = open_page(browser, state=state, now=now)
        context.add_init_script("localStorage.setItem('loco-clock', '12')")
        context.add_init_script(
            "document.addEventListener('DOMContentLoaded', () => {"
            f"document.documentElement.style.fontSize = '{font}';"
            "window.transitions = [];"
            "document.addEventListener('transitionrun', (e) => window.transitions.push("
            "[e.target.dataset.slot || String(e.target.className), e.propertyName]));});"
        )
        context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
        context.route(
            "**/loco.observability.v1.ObservabilityService/QueryLogs",
            lambda route: route.fulfill(
                json={"entries": marker_logs, "totalMatched": str(len(marker_logs))}
            ),
        )
        page.goto(state["ui_url"] + base)
        page.get_by_role("row", name="Open trace " + root_name, exact=True).click()
        drawer = page.get_by_role("dialog", name=root_name)
        flame = drawer.get_by_role("region", name="Flame graph")
        expect(flame.locator("[data-span-id]")).to_have_count(len(spans))
        page.wait_for_function(
            "() => transitions.some(([c, p]) => c === 'sheet-content' && ['translate', 'transform'].includes(p))"
        )
        expect(page.locator('[data-slot="tooltip-content"]')).to_have_count(0)
        overview = drawer.get_by_role("slider", name="Trace overview")
        expect(overview).to_have_count(0)
        lane = flame.locator("[data-span-id]").first.evaluate(
            "b => [b.getBoundingClientRect().width, b.parentElement.getBoundingClientRect().width]"
        )
        assert abs(lane[0] - lane[1]) < 1, (font, lane)
        boxes = flame.locator("[data-span-id]").evaluate_all(
            "(bars) => bars.map(b => {const r=b.getBoundingClientRect(); return [r.x, r.y, r.right, r.bottom]})"
        )
        for i, a in enumerate(boxes):
            for b in boxes[i + 1 :]:
                assert (
                    a[3] <= b[1]
                    or b[3] <= a[1]
                    or a[2] <= b[0] + 0.1
                    or b[2] <= a[0] + 0.1
                ), (font, a, b)
        ranking = drawer.get_by_role("group", name="Time spent per resource")
        ranking.get_by_role("button", name=re.compile(r"^\+\d+ more$")).click()
        for service, body in (
            ("product-catalog", "Product found"),
            ("payment", "Payment declined"),
        ):
            marker = drawer.get_by_role(
                "button", name=re.compile(f"^Log at .*: {body}$")
            )
            width, background = marker.evaluate(
                "m => [m.getBoundingClientRect().width, getComputedStyle(m).backgroundColor]"
            )
            dot = (
                ranking.get_by_role("button")
                .filter(has_text=service)
                .first.locator("i")
                .evaluate("i => getComputedStyle(i).backgroundColor")
            )
            assert width <= 4 and background == dot, (
                font,
                service,
                width,
                background,
                dot,
            )
        product = drawer.get_by_role(
            "button", name=re.compile("^Log at .*: Product found$")
        )
        other = drawer.get_by_role(
            "button", name=re.compile("^Log at .*: Payment declined$")
        )
        flame.locator(f'[data-span-id="{spans[5]["spanId"]}"]').hover()
        expect(product).to_have_css("height", "12px")
        assert product.evaluate("m => getComputedStyle(m).boxShadow") == "none", font
        assert product.evaluate("m => getComputedStyle(m).outlineStyle") == "none", font
        assert float(product.evaluate("m => getComputedStyle(m).opacity")) == 1, font
        assert float(other.evaluate("m => getComputedStyle(m).opacity")) < 0.5, font
        log_list = drawer.locator("[data-trace-log-scroll]")
        log_list.evaluate(
            "l => {l.scrollTop = l.scrollHeight; l.querySelectorAll('[data-log-key]').forEach(r => {r.mounted = true})}"
        )
        times = log_list.locator("[data-log-key] > span:first-child")
        assert times.first.inner_text().endswith(("AM", "PM")), font
        assert times.evaluate_all(
            "ts => ts.every(t => t.getBoundingClientRect().height < 20 && t.scrollWidth <= t.clientWidth)"
        ), font
        scrolled = log_list.evaluate("l => l.scrollTop")
        assert scrolled > 0, font
        for span in (spans[10], spans[5], spans[0]):
            flame.locator(f'[data-span-id="{span["spanId"]}"]').hover()
            page.wait_for_timeout(100)
            assert log_list.evaluate("l => l.scrollTop") == scrolled, font
            flame.locator(f'[data-span-id="{span["spanId"]}"]').click()
            page.wait_for_timeout(100)
            assert log_list.evaluate("l => l.scrollTop") == scrolled, font
            assert log_list.evaluate(
                "l => [...l.querySelectorAll('[data-log-key]')].every(r => r.mounted)"
            ), font
        bar = flame.locator(f'[data-span-id="{spans[5]["spanId"]}"]')
        bar.hover()
        tooltip = page.get_by_role("tooltip")
        expect(tooltip).to_contain_text(spans[5]["name"])
        title = tooltip.locator("span").first
        meta = tooltip.locator("span").nth(1)
        expect(meta).to_contain_text("self ")
        expect(tooltip.locator('[data-slot="span-tip-dot"]')).to_have_count(1)
        weights = [
            e.evaluate(
                "e => [getComputedStyle(e).fontWeight, getComputedStyle(e).fontSize]"
            )
            for e in (title, meta)
        ]
        assert weights[0] != weights[1], weights
        assert int(weights[0][0]) > int(weights[1][0]), weights
        assert float(weights[0][1][:-2]) > float(weights[1][1][:-2]), weights
        expect(tooltip).not_to_contain_text("Double-click")
        box = bar.bounding_box()
        assert page.evaluate(
            "([x, y]) => document.elementsFromPoint(x, y).every(e => !e.hasAttribute('title'))",
            [box["x"] + box["width"] / 2, box["y"] + box["height"] / 2],
        ), font
        marker = drawer.get_by_role(
            "button", name=re.compile("^Log at .*: Product found$")
        )
        marker.hover()
        expect(page.get_by_role("tooltip")).to_contain_text("Product found")
        assert marker.get_attribute("title") is None, font
        drawer.get_by_role("button", name=re.compile("^Log at .*: Access log ")).hover()
        tip = page.get_by_role("tooltip")
        expect(tip).to_contain_text("Access log")
        clamped = tip.locator("span").first.evaluate(
            "t => [t.getBoundingClientRect().height / parseFloat(getComputedStyle(t).lineHeight), t.scrollHeight > t.clientHeight]"
        )
        assert clamped[0] <= 2.05 and clamped[1], (font, clamped)
        expect(tip.locator("span").nth(1)).to_be_visible()
        resizer = drawer.get_by_role("separator", name="Resize bottom panel")
        panel_edges = (
            "d => [d.querySelector('[data-slot=sheet-body]'), d.querySelector('[aria-label=\"Trace bottom panel\"]'), d.querySelector('[data-trace-log-scroll]')]"
            ".map(e => e.getBoundingClientRect()).map(r => [r.top, r.bottom])"
        )
        heights = []
        for dy in (-900, 100, 900):
            handle_box = resizer.bounding_box()
            x = handle_box["x"] + 20
            y = handle_box["y"] + handle_box["height"] / 2
            page.mouse.move(x, y)
            page.mouse.down()
            page.mouse.move(x, y + dy, steps=5)
            page.mouse.up()
            body, panel, rows_box = drawer.evaluate(panel_edges)
            assert abs(panel[1] - body[1]) < 1 and abs(rows_box[1] - panel[1]) < 1, (
                font,
                dy,
                body,
                panel,
                rows_box,
            )
            heights.append(
                (panel[1] - panel[0], resizer.get_attribute("aria-valuenow"))
            )
        assert abs(int(heights[1][1]) - (heights[0][0] - 100)) <= 1, (font, heights)
        assert resizer.get_attribute("aria-valuenow") == "192", font
        log_list.evaluate("l => {l.scrollTop = 0}")
        assert log_list.evaluate(
            "l => {const top = l.getBoundingClientRect().top, bottom = l.getBoundingClientRect().bottom, rows = [...l.querySelectorAll('[data-log-key]')].map(r => r.getBoundingClientRect()); return rows[0].top >= top - 0.5 && rows.filter(r => r.bottom <= bottom + 0.5).length >= 3}"
        ), font
        flame.locator("[data-span-id]").first.hover()
        page.evaluate("transitions.length = 0")
        page.mouse.wheel(0, -250)
        expect(overview).to_be_visible()
        page.wait_for_function(
            "() => transitions.some(([c, p]) => c.includes('tv-range-wrap') && p === 'grid-template-rows')"
        )
        drawer.get_by_role("button", name="Reset zoom", exact=True).click()
        expect(overview).to_have_count(0)
        drawer.get_by_role("button", name="Waterfall", exact=True).click()
        waterfall = drawer.get_by_role("region", name="Waterfall")
        expect(waterfall.locator("[data-span]")).to_have_count(len(spans))
        view_box = drawer.get_by_role("group", name="Trace view").bounding_box()
        page.mouse.move(
            view_box["x"] + view_box["width"] + 300,
            view_box["y"] + view_box["height"] / 2,
        )
        for _ in range(6):
            page.mouse.wheel(0, 300)
            page.wait_for_timeout(50)
        tabs = drawer.get_by_role("group", name="Trace view").get_by_role("button")
        assert tabs.evaluate_all(
            "ts => ts.every(t => {const r=t.getBoundingClientRect(); return [[r.left+2,r.top+2],[r.right-2,r.bottom-2],[r.left+r.width/2,r.top+r.height/2]].every(([x,y]) => t.contains(document.elementFromPoint(x,y)))})"
        ), font
        waterfall.locator("[data-span]").nth(4).hover()
        for _ in range(6):
            page.mouse.wheel(0, 300)
            page.wait_for_timeout(50)
        assert tabs.evaluate_all(
            "ts => ts.every(t => {const r=t.getBoundingClientRect(); return [[r.left+2,r.top+2],[r.right-2,r.bottom-2],[r.left+r.width/2,r.top+r.height/2]].every(([x,y]) => t.contains(document.elementFromPoint(x,y)))})"
        ), font
        assert waterfall.get_by_text("Span", exact=True).first.evaluate(
            "h => {const r=h.getBoundingClientRect(); return h.contains(document.elementFromPoint(r.left+r.width/2, r.top+r.height/2))}"
        ), font
        assert waterfall.locator("[data-span]").evaluate_all(
            "bs => bs.every(b => {const r=document.createRange(); r.selectNodeContents(b.firstChild); return r.getBoundingClientRect().right <= b.getBoundingClientRect().right + 0.5})"
        ), font
        for name in ("GET /api/products", "GetRecommendations", root_name):
            drawer.get_by_role("button", name="Close", exact=True).click()
            expect(drawer).to_have_count(0)
            page.evaluate("transitions.length = 0")
            page.get_by_role("row", name="Open trace " + name, exact=True).click()
            expect(drawer).to_be_visible()
            page.wait_for_function(
                "() => transitions.some(([c, p]) => c === 'sheet-content' && ['translate', 'transform'].includes(p))"
            )
        assert not errors, errors
        context.close()
    context, page, errors = open_page(browser, state=state, now=start + 1)
    context.route("**/loco.observability.v1.ObservabilityService/*Trace*", handle)
    page.goto(state["ui_url"] + base + "&trace=" + trace)
    arriving = (
        page.get_by_role("dialog", name=root_name)
        .get_by_role("status")
        .filter(has_text="Spans may still be arriving")
    )
    expect(arriving).to_be_visible()

    def trace_reads():
        return sum(1 for row in recorded if row.get("traceId") == trace)

    def wait_for_reads(count):
        for _ in range(50):
            if trace_reads() >= count:
                return
            page.wait_for_timeout(100)
        raise AssertionError(f"expected {count} trace reads, got {trace_reads()}")

    reads = trace_reads()
    page.clock.run_for(5000)
    wait_for_reads(reads + 1)
    expect(arriving).to_be_visible()
    page.clock.run_for(30000)
    expect(arriving).to_have_count(0)
    reads = trace_reads()
    page.clock.run_for(20000)
    page.wait_for_timeout(500)
    assert trace_reads() == reads, (reads, trace_reads())
    assert not errors, errors
    context.close()
    browser.close()
assert all(row.get("scope", {}).get("environmentId") for row in recorded)
assert all(
    not row.get("scope", {}).get("resourceIds")
    for row in recorded
    if row.get("traceId")
)
print(
    "PASS browser trace list, complete drawer spans, span selection and attributes, span attribute filters, query text requests, frozen bounds, whole-row trace buttons, resizable trace columns, recent queries per view with per-item removal, pinning and unpinning, typed-text suggestions, and a refetch while spans of a recent trace may still arrive, and production UI; trace responses use explicit browser fixtures."
)
