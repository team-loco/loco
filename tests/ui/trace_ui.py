import argparse
import calendar
import sys
import json
import time
from pathlib import Path
import re
import urllib.parse
from playwright.sync_api import expect
from harness import wait_for_query, evidence, load_state, open_page, repo, session
sys.dont_write_bytecode = True
parser = argparse.ArgumentParser(description='Exercise the production trace viewer with fixture trace responses')
parser.add_argument('--state')
parser.add_argument('--keep', action='store_true')
parser.add_argument('--out', default=str(repo / 'tests/ui/out/trace-ui'))
args = parser.parse_args()
state = load_state(args, 'trace_ui', '.agents/skills/ui-evidence/scenario.example.json')
orgs = evidence.rpc(state['api_url'], state['users']['owner']['token'], 'loco.org.v1.OrgService/ListUserOrgs', {'userId': state['users']['owner']['id'], 'pageSize': 200})
org_id = orgs['orgs'][0]['id']
workspace_id = state['workspaces']['acme/shared']
base = f'/org/{org_id}/wks/{workspace_id}/observability?view=traces'
trace = '4bf92f3577b34da6a3ce929d0e0e4736'
start = int(time.time()) - 600
rows = [('0001', '', 'frontend-proxy', 'POST /api/checkout', 0, 427.6), ('0002', '0001', 'frontend', 'POST /api/checkout', 1.2, 424.1), ('0003', '0002', 'checkout', 'oteldemo.CheckoutService/PlaceOrder', 5.3, 412.0), ('0004', '0003', 'cart', 'GetCart', 8.4, 24.5), ('0005', '0004', 'valkey', 'GET cart:session', 10.1, 7.8), ('0006', '0003', 'product-catalog', 'GetProduct · OLJCESPC7Z', 34.1, 18.9), ('0007', '0003', 'currency', 'Convert · USD → EUR', 58.7, 12.4), ('0008', '0003', 'shipping', 'GetQuote', 76.3, 86.2), ('0009', '0008', 'quote', 'POST /quote', 79.5, 71.4), ('000a', '0003', 'payment', 'Charge · attempt 1', 166.8, 96.1), ('000b', '000a', 'payment', 'POST /payments/authorize', 168.2, 91.7), ('000c', '0003', 'payment', 'Charge · attempt 2', 275.2, 78.6), ('000d', '0003', 'email', 'SendOrderConfirmation', 357.4, 45.2), ('000e', '0003', 'cart', 'EmptyCart', 405.1, 8.6)]

def stamp(offset):
    return f"{time.strftime('%Y-%m-%dT%H:%M:%S', time.gmtime(start))}.{int(offset * 1000000):09d}Z"

def span(row):
    sid, parent, service, name, offset, duration = row
    return {'traceId': trace, 'spanId': sid.zfill(16), 'parentSpanId': parent.zfill(16) if parent else '', 'name': name, 'kind': 'Server', 'serviceName': service, 'startTime': stamp(offset), 'durationNanos': str(int(duration * 1000000)), 'statusCode': 'Error' if sid in ('000a', '000b') else 'Ok', 'statusMessage': 'Payment authorization timed out after 90 ms' if sid == '000b' else '', 'spanAttributes': {'http.request.method': 'POST', 'http.response.status_code': '504' if sid == '000b' else '200', 'server.address': 'payments.internal', 'payment.attempt': '1'} if sid == '000b' else {'rpc.system': 'grpc', 'rpc.service': f'oteldemo.{service.title()}Service'}, 'resourceAttributes': {'service.name': service, 'service.version': '2.1.0', 'deployment.environment.name': 'production', 'cloud.region': 'us-east-1'}, 'scopeName': '@opentelemetry/instrumentation-http', 'scopeVersion': '0.210.0'}
spans = [span(row) for row in rows]
spans[0]['startTime'] = stamp(0.000123)
concurrent_spans = [span(row) for row in [
    ('1000', '', 'frontend', 'GET /recommendations', 0, 10),
    ('1001', '1000', 'product-catalog', 'Product lookup', 0.5, 8),
    ('1002', '1001', 'product-catalog', 'grpc.oteldemo.ProductCatalogService/GetProduct', 1, 2.62),
    ('1003', '1001', 'product-catalog', 'grpc.oteldemo.ProductCatalogService/GetProduct', 2, 3.98),
    ('1004', '1002', 'cache', 'Read product cache', 2, 4),
    ('1005', '1001', 'product-catalog', 'grpc.oteldemo.ProductCatalogService/GetProduct', 4, 3),
]]

spans[10]['events'] = [
    {'name': 'retry.scheduled', 'timestamp': stamp(168.2 + 12.4), 'attributes': {'attempt': '1'}},
    {'name': 'exception', 'timestamp': stamp(168.2 + 90), 'attributes': {'exception.type': 'TimeoutError', 'exception.message': 'Authorization timed out', 'exception.stacktrace': 'TimeoutError: Authorization timed out\n    at authorize (payments.ts:42:7)\n    at checkout (checkout.ts:18:3)'}},
]
linked_trace = '1' * 32
linked_span = '2' * 16
spans[11]['links'] = [
    {'traceId': trace, 'spanId': spans[5]['spanId'], 'attributes': {'link.reason': 'retry'}},
    {'traceId': linked_trace, 'spanId': linked_span, 'attributes': {}},
]
recorded = []
mode = ['normal']

def iso_seconds(value):
    return calendar.timegm(time.strptime(value[:19], '%Y-%m-%dT%H:%M:%S'))

def trace_buckets(body):
    width = body['intervalSeconds']
    first = int(iso_seconds(body['startTime'])) // width * width
    last = int(iso_seconds(body['endTime'])) // width * width
    buckets = [{'startTime': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(at))} for at in range(first, last + 1, width)]
    for index, bucket in enumerate(buckets[-4:]):
        bucket.update(traceCount=str(index + 2), errorCount=str(index % 2), p50DurationNanos=str((index + 1) * 20000000), p95DurationNanos=str((index + 1) * 90000000))
    return {'buckets': buckets, 'total': {'traceCount': '14', 'errorCount': '2', 'p50DurationNanos': '42000000', 'p95DurationNanos': '360000000'}}

def last_page_request():
    return next(row for row in reversed(recorded) if 'startTime' in row and row.get('limit') in (25, 50, 100))

def handle(route):
    body = route.request.post_data_json
    recorded.append(body)
    if mode[0] == 'drawer-error' and route.request.url.endswith('/GetTrace'):
        route.fulfill(status=500, content_type='application/json', body=json.dumps({'code': 'internal', 'message': 'Trace read failed'}))
        return
    if mode[0] == 'zero' and route.request.url.endswith('/GetTrace'):
        route.fulfill(json={'spans': [dict(spans[0], durationNanos='0')]})
        return
    if mode[0] == 'drawer-empty' and route.request.url.endswith('/GetTrace'):
        route.fulfill(json={'spans': []})
        return
    if mode[0] == 'error':
        route.fulfill(status=503, content_type='application/json', body=json.dumps({'code': 'unavailable', 'message': 'Trace storage is temporarily unavailable'}))
        return
    if mode[0] == 'empty':
        route.fulfill(json={'spans': []})
        return
    if route.request.url.endswith('/QueryTraceBuckets'):
        route.fulfill(json=trace_buckets(body))
        return
    if mode[0] == 'concurrent' and route.request.url.endswith('/GetTrace'):
        route.fulfill(json={'spans': concurrent_spans})
        return
    if mode[0] == 'large' and route.request.url.endswith('/GetTrace'):
        route.fulfill(json={'spans': [spans[0]] + [dict(spans[5],spanId=f'{index + 256:016x}',parentSpanId=spans[0]['spanId']) for index in range(3000)]})
        return
    if mode[0] == 'many' and not route.request.url.endswith('/ListTraces'):
        route.fulfill(json={'spans': spans + [dict(spans[5],spanId=f'{index + 256:016x}',name=f'Lookup {index}') for index in range(100)]})
        return
    if route.request.url.endswith('/ListTraces'):
        others = [dict(spans[index], traceId=f'{index + 100:032x}', parentSpanId='', name=name, durationNanos=str(duration * 1000000)) for index, (name, duration) in enumerate([('GET /api/products', 18), ('GetRecommendations', 132), ('GetProduct', 9), ('GET /cart', 42), ('Convert · USD → EUR', 12), ('GetQuote', 86)])]
        summaries = [{'traceId': trace, 'rootSpanName': spans[0]['name'], 'serviceName': spans[0]['serviceName'], 'startTime': spans[0]['startTime'], 'durationNanos': '427600000', 'status': 'TRACE_STATUS_ERROR', 'spanCount': str(len(spans)), 'httpStatusCode': 200}]
        summaries += [{'traceId': row['traceId'], 'rootSpanName': row['name'], 'serviceName': row['serviceName'], 'startTime': row['startTime'], 'durationNanos': row['durationNanos'], 'status': 'TRACE_STATUS_OK', 'spanCount': '1'} for row in others]
        if mode[0] == 'paged':
            summaries += [dict(summaries[1], traceId=f'{index + 1000:032x}', rootSpanName=f'Paged trace {index}') for index in range(60)]
        offset = body.get('offset', 0)
        route.fulfill(json={'totalCount': str(len(summaries)), 'summaries': summaries[offset:offset + body['limit']]})
        return
    if body.get('traceId') == linked_trace:
        route.fulfill(json={'spans': [dict(spans[5], traceId=linked_trace, spanId=linked_span, parentSpanId='', name='Linked product lookup')]})
        return
    route.fulfill(json={'spans': spans})
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with session(out) as browser:
    root_name = 'POST /api/checkout'
    failing = re.compile(r'^payment POST \/payments\/authorize, .*, error$')
    context, page, errors = open_page(browser, state=state, permissions=['clipboard-read', 'clipboard-write'], device_scale_factor=2, reduced_motion='reduce')
    context.route('**/loco.observability.v1.ObservabilityService/*Trace*', handle)
    config_requests = []
    config_legacy = [True]
    def six_hour_config(route):
        response = route.fetch()
        body = response.json()
        if config_legacy[0]:
            body.pop('observabilityMaxTimeRange', None)
        else:
            body['observabilityMaxTimeRange'] = '21600s'
        config_requests.append(body)
        route.fulfill(response=response, json=body)
    context.route('**/loco.config.v1.ConfigService/GetConfig', six_hour_config)
    page.goto(state['ui_url'] + f'/org/{org_id}/wks/{workspace_id}/resources')
    page.wait_for_load_state('networkidle')
    assert config_requests and all('observabilityMaxTimeRange' not in body for body in config_requests)
    cached_requests = len(config_requests)
    config_legacy[0] = False
    page.get_by_role('link', name='Traces', exact=True).click()
    open_trace = page.get_by_role('region', name='Trace list').get_by_role('button', name='Open trace ' + root_name, exact=True)
    open_trace.wait_for()
    assert len(config_requests) > cached_requests and config_requests[-1]['observabilityMaxTimeRange'] == '21600s'
    expect(page.get_by_role('heading', level=1)).to_have_text('Traces')
    wait_for_query(page, lambda query: query.get('env') == ['production'] and query.get('view') == ['traces'])
    expect(page.get_by_role('button', name=re.compile(r'^All\s*7$'))).to_be_visible()
    expect(open_trace).to_contain_text('200')
    expect(open_trace).to_have_attribute('aria-pressed', 'false')
    open_trace.click(position={'x': 20, 'y': 10})
    drawer = page.get_by_role('dialog', name=root_name)
    expect(page).to_have_url(re.compile('trace=' + trace))
    assert drawer.evaluate('(element) => element.contains(document.activeElement)')
    flame = drawer.get_by_role('region', name='Flame graph')
    expect(flame.get_by_role('button')).to_have_count(len(spans))
    flame.get_by_role('button', name=failing).click()
    expect(flame.get_by_role('button', name=failing)).to_have_attribute('aria-pressed', 'true')
    expect(page).to_have_url(re.compile('span=' + spans[10]['spanId']))
    expect(drawer.get_by_role('region', name='Selected span').get_by_role('heading', name='POST /payments/authorize', exact=True)).to_be_visible()
    expect(drawer.get_by_text('Payment authorization timed out after 90 ms', exact=True)).to_be_visible()
    events = drawer.get_by_role('region', name='Span events')
    expect(events.get_by_text('TimeoutError', exact=True)).to_be_visible()
    expect(events.get_by_text('Authorization timed out', exact=True)).to_be_visible()
    expect(events.locator('pre')).to_have_text(spans[10]['events'][1]['attributes']['exception.stacktrace'])
    expect(events.get_by_role('listitem')).to_have_count(2)
    expect(events.get_by_role('listitem').nth(0)).to_contain_text('+12.40 ms')
    expect(events.get_by_role('listitem').nth(1)).to_contain_text('+90.00 ms')
    events.get_by_role('button', name='Copy stack trace', exact=True).click()
    assert page.evaluate('navigator.clipboard.readText()') == spans[10]['events'][1]['attributes']['exception.stacktrace']
    attributes = drawer.get_by_role('region', name='Span attributes')
    expect(drawer.get_by_role('region', name='Span details').get_by_text(spans[10]['spanId'], exact=True)).to_be_visible()
    expect(attributes.get_by_text('http.response.status_code', exact=True)).to_be_visible()
    attributes.get_by_role('tab', name='Telemetry', exact=True).click()
    expect(attributes.get_by_role('tablist', name='Span detail tabs')).to_be_visible()
    attributes.get_by_role('tab', name='Telemetry', exact=True).press('ArrowLeft')
    expect(attributes.get_by_role('tab', name='Kubernetes', exact=True)).to_be_focused()
    attributes.get_by_role('tab', name='Kubernetes', exact=True).press('Enter')
    expect(attributes.get_by_role('tab', name='Kubernetes', exact=True)).to_have_attribute('aria-selected', 'true')
    expect(attributes.get_by_role('tabpanel', name='Kubernetes', exact=True)).to_be_visible()
    attributes.get_by_role('tab', name='Telemetry', exact=True).click()
    expect(attributes.get_by_text('service.version', exact=True)).to_be_visible()
    expect(attributes.get_by_text('@opentelemetry/instrumentation-http', exact=True)).to_be_visible()
    attributes.get_by_role('tab', name='Attributes', exact=True).click()
    expect(attributes.get_by_role('button', name='Only traces with this value: http.response.status_code', exact=True)).to_be_enabled()
    expect(attributes.get_by_role('button', name='Hide traces with this value: http.response.status_code', exact=True)).to_be_enabled()
    attributes.get_by_role('tab', name='Telemetry', exact=True).click()
    expect(attributes.get_by_role('button', name='Only traces with this value: scope.name', exact=True)).to_be_disabled()
    attributes.get_by_role('tab', name='Attributes', exact=True).click()
    drawer.get_by_role('button', name='Copy trace ID ' + trace, exact=True).click()
    assert page.evaluate('navigator.clipboard.readText()') == trace
    drawer.get_by_role('button', name='More for this trace', exact=True).click()
    expect(page.get_by_role('menuitem', name='Copy trace ID', exact=True)).to_have_count(0)
    page.get_by_role('menuitem', name='Copy as JSON', exact=True).click()
    exported = json.loads(page.evaluate('navigator.clipboard.readText()'))
    exported_spans = [span for resource in exported['resourceSpans'] for scope in resource['scopeSpans'] for span in scope['spans']]
    assert len(exported_spans) == len(spans)
    assert next(span for span in exported_spans if span['spanId'] == spans[10]['spanId'])['events'][1]['attributes']
    drawer.get_by_role('button', name='More for this trace', exact=True).click()
    page.get_by_role('menuitem', name='Copy link', exact=True).click()
    assert page.evaluate('navigator.clipboard.readText()') == page.url
    drawer.get_by_role('button', name='More for this trace', exact=True).click()
    with page.expect_download() as download_info:
        page.get_by_role('menuitem', name='Download JSON', exact=True).click()
    assert download_info.value.suggested_filename == f'trace-{trace}.json'
    ranking = drawer.get_by_role('group', name='Time by resource')
    expect(ranking.get_by_role('button', name='+7 more', exact=True)).to_be_visible()
    expect(ranking.locator('button i')).to_have_count(4)
    ranking.get_by_role('button', name='+7 more', exact=True).click()
    expect(ranking.locator('button i')).to_have_count(11)
    colors = ranking.locator('button i').evaluate_all('(dots) => dots.map(dot => getComputedStyle(dot).backgroundColor)')
    assert len(set(colors)) == 11, colors
    ranking.get_by_role('button', name='Show fewer', exact=True).click()
    expect(ranking.locator('button i')).to_have_count(4)
    payment = ranking.get_by_role('button').filter(has_text='payment')
    payment.click()
    expect(payment).to_have_attribute('aria-pressed', 'true')
    drawer.get_by_role('button', name='Show all resources', exact=True).click()
    expect(payment).to_have_attribute('aria-pressed', 'false')
    overview = drawer.get_by_role('slider', name='Trace overview')
    expect(overview).to_have_count(0)
    axis = flame.locator('[data-trace-axis]')
    axis_box = axis.bounding_box()
    page.mouse.move(axis_box['x'] + axis_box['width'] * 0.2, axis_box['y'] + 8)
    page.mouse.down()
    page.mouse.move(axis_box['x'] + axis_box['width'] * 0.6, axis_box['y'] + 8, steps=6)
    page.mouse.up()
    expect(overview).to_have_attribute('aria-valuetext', '20–60% of trace')
    assert overview.bounding_box()['height'] == 24
    overview_box = overview.bounding_box()
    page.mouse.move(overview_box['x'] + overview_box['width'] * 0.3, overview_box['y'] + 12)
    page.mouse.down()
    page.mouse.move(overview_box['x'] + overview_box['width'] * 0.4, overview_box['y'] + 12, steps=4)
    page.mouse.up()
    expect(overview).to_have_attribute('aria-valuetext', '30–70% of trace')
    overview.click(position={'x': overview_box['width'] * 0.85, 'y': 12})
    expect(overview).to_have_attribute('aria-valuetext', '60–100% of trace')
    page.mouse.move(overview_box['x'] + overview_box['width'] * 0.1, overview_box['y'] + 12)
    page.mouse.down()
    page.mouse.move(overview_box['x'] + overview_box['width'] * 0.4, overview_box['y'] + 12, steps=4)
    page.mouse.up()
    expect(overview).to_have_attribute('aria-valuetext', '10–40% of trace')
    drawer.get_by_role('button', name='Reset zoom', exact=True).click()
    expect(overview).to_have_count(0)
    flame.get_by_role('button', name=failing).dblclick()
    overview = drawer.get_by_role('slider', name='Trace overview')
    before_window = overview.get_attribute('aria-valuetext')
    overview.press('ArrowLeft')
    expect(overview).not_to_have_attribute('aria-valuetext', before_window)
    drawer.get_by_role('button', name='Reset zoom', exact=True).click()
    plot = drawer.locator('[data-trace-plot]')
    plot.hover(position={'x': 150, 'y': 50})
    page.mouse.wheel(0, -250)
    expect(drawer.get_by_role('button', name='Reset zoom', exact=True)).to_have_count(0)
    page.keyboard.down('Control')
    page.mouse.wheel(0, -250)
    page.keyboard.up('Control')
    expect(drawer.get_by_role('button', name='Reset zoom', exact=True)).to_be_visible()
    window = overview.get_attribute('aria-valuetext')
    plot.hover(position={'x': 160, 'y': 50})
    page.mouse.down()
    page.mouse.move(plot.bounding_box()['x'] + 100, plot.bounding_box()['y'] + 50, steps=4)
    page.mouse.up()
    expect(overview).not_to_have_attribute('aria-valuetext', window)
    plot.hover(position={'x': 160, 'y': 50})
    page.mouse.down()
    page.mouse.move(plot.bounding_box()['x'] + 140, plot.bounding_box()['y'] + 50, steps=2)
    plot.dispatch_event('pointercancel', {'pointerId': 1})
    canceled_window = overview.get_attribute('aria-valuetext')
    page.mouse.move(plot.bounding_box()['x'] + 100, plot.bounding_box()['y'] + 50, steps=2)
    assert overview.get_attribute('aria-valuetext') == canceled_window
    page.mouse.up()
    drawer.get_by_role('button', name='Reset zoom', exact=True).click()
    drawer.get_by_role('button', name='Waterfall', exact=True).click()
    waterfall = drawer.get_by_role('region', name='Waterfall')
    axis_box = waterfall.locator('[data-trace-axis]').bounding_box()
    page.mouse.move(axis_box['x'] + axis_box['width'] * 0.1, axis_box['y'] + 8)
    page.mouse.down()
    page.mouse.move(axis_box['x'] + axis_box['width'] * 0.5, axis_box['y'] + 8, steps=6)
    page.mouse.up()
    expect(overview).to_have_attribute('aria-valuetext', '10–50% of trace')
    drawer.get_by_role('button', name='Reset zoom', exact=True).click()

    expect(waterfall.locator('[data-span]')).to_have_count(len(spans))
    waterfall.get_by_role('button', name='Collapse POST /api/checkout', exact=True).first.click()
    expect(waterfall.locator('[data-span]')).to_have_count(1)
    waterfall.get_by_role('button', name='Expand POST /api/checkout', exact=True).click()
    expect(waterfall.locator('[data-span]')).to_have_count(len(spans))
    first_name = waterfall.locator('[data-span]').first
    first_name.press('ArrowDown')
    expect(waterfall.locator('[data-span]').nth(1)).to_be_focused()
    waterfall.get_by_role('separator', name='Resize span column').press('ArrowRight')
    expect(waterfall.get_by_role('separator', name='Resize span column')).to_have_attribute('aria-valuenow','256')
    drawer.get_by_role('button', name='Span list', exact=True).click()
    span_list = drawer.get_by_role('region', name='Span list')
    span_list.get_by_role('separator', name='Resize % of exec time', exact=True).press('ArrowLeft')
    assert span_list.locator('.tv-share-bar').evaluate_all("bars => bars.every(b => {const r=b.getBoundingClientRect(), c=b.closest('td').getBoundingClientRect();return r.x >= c.x && r.right <= c.right && r.width <= 110})")
    search_operations = span_list.get_by_role('textbox', name='Filter operations')
    search_operations.fill('Charge')
    expect(span_list.locator('tbody tr')).to_have_count(2)
    search_operations.fill('unknown-operation')
    expect(span_list.get_by_text('No operations match this filter.', exact=True)).to_be_visible()
    span_list.get_by_role('button', name='Clear search', exact=True).click()
    expect(span_list.locator('tbody tr')).to_have_count(len(spans))
    drawer.get_by_role('button', name='Flame graph', exact=True).click()
    flame.get_by_role('button', name=failing).click()
    page.get_by_text('Copied trace ID', exact=True).wait_for(state='hidden')
    linking_span = flame.get_by_role('button', name=re.compile(r'^payment Charge · attempt 2,'))
    linking_span.click()
    links = drawer.get_by_role('region', name='Span links')
    expect(links.get_by_role('listitem')).to_have_count(2)
    expect(links).to_contain_text('link.reason')
    expect(links).to_contain_text(linked_trace)
    links.get_by_role('link', name='Open linked span', exact=True).first.click()
    expect(page).to_have_url(re.compile('span=' + spans[5]['spanId']))
    expect(drawer.get_by_role('region', name='Selected span')).to_contain_text('GetProduct · OLJCESPC7Z')
    linking_span.click()
    flame.get_by_role('button', name=re.compile(r'^payment Charge · attempt 2,')).dblclick()
    expect(drawer.get_by_role('button', name='Reset zoom', exact=True)).to_be_visible()
    links.get_by_role('link', name='Open linked span', exact=True).nth(1).click()
    wait_for_query(page, lambda query: query.get('trace') == [linked_trace] and query.get('span') == [linked_span])
    linked_drawer = page.get_by_role('dialog', name='Linked product lookup', exact=True)
    linked_drawer.wait_for()
    expect(linked_drawer.get_by_role('button', name='Reset zoom', exact=True)).to_have_count(0)
    page.go_back()
    drawer.wait_for()
    flame.get_by_role('button', name=failing).click()
    expect(page).to_have_url(re.compile('span=' + spans[10]['spanId']))
    assert not errors, errors
    previous_url = page.url
    previous_request = dict(next(row for row in reversed(recorded) if "startTime" in row))
    page.keyboard.press('Escape')
    expect(drawer).to_have_count(0)
    expect(page).not_to_have_url(re.compile(r'[?&]trace='))
    spans.append(dict(spans[5], spanId='0000000000000f01', name='Late exported span'))
    open_trace.click()
    expect(flame.get_by_role('button')).to_have_count(len(spans))
    expect(flame.get_by_role('button', name=re.compile('Late exported span'))).to_have_count(1)
    drawer.get_by_role('button', name='Close', exact=True).click()
    expect(drawer).to_have_count(0)
    spans.pop()
    page.get_by_role('button', name=re.compile(r'^Past hour ')).click()
    expect(page.get_by_text('Ranges up to 6h.', exact=True)).to_be_visible()
    expect(page.get_by_role('group', name='Presets').get_by_role('button', name=re.compile('Past 12 hours'))).to_have_count(0)
    page.get_by_role('group', name='Presets').get_by_role('button', name='Past 15 minutes', exact=False).click()
    expect(page).to_have_url(re.compile(r'[?&]range=15m'))
    page.go_back()
    expect(page).not_to_have_url(re.compile(r'[?&]range=15m'))
    page.goto(previous_url)
    expect(flame.get_by_role('button', name=failing)).to_have_attribute('aria-pressed', 'true')
    drawer.get_by_role('button', name='Close', exact=True).click()
    expect(drawer).to_have_count(0)
    page.get_by_role('button', name='Refresh', exact=True).click()
    assert next(row for row in reversed(recorded) if 'startTime' in row)['startTime'] != previous_request['startTime']
    with page.expect_response(lambda response: response.url.endswith('/ListTraces') and response.request.post_data_json.get('query') == '@trace:invalid') as filtered:
        page.goto(state['ui_url'] + base + '&q=' + urllib.parse.quote('@trace:invalid'))
    assert filtered.value.request.post_data_json['query'] == '@trace:invalid'
    page.goto(state['ui_url'] + base)
    expect(open_trace).to_be_visible()
    page.goto(state['ui_url'] + base + '&q=checkout&trace=' + trace + '&span=' + spans[10]['spanId'])
    with page.expect_response(lambda response: response.url.endswith('/ListTraces') and response.request.post_data_json.get('query') == '-@http.response.status_code:504 checkout'):
        drawer.get_by_role('region', name='Span attributes').get_by_role('button', name='Hide traces with this value: http.response.status_code', exact=True).click()
    expect(drawer).to_have_count(0)
    page.goto(state['ui_url'] + base)
    expect(open_trace).to_be_visible()
    page.goto(state['ui_url'] + base + '&trace=' + trace + '&span=ffffffffffffffff')
    drawer.get_by_text('The selected span is unavailable.', exact=True).wait_for()
    drawer.get_by_role('button', name='Close', exact=True).click()
    expect(drawer).to_have_count(0)
    if drawer.count():
        drawer.get_by_role('button', name='Close', exact=True).click()
        expect(drawer).to_have_count(0)
    mode[0] = 'drawer-error'
    page.goto(state['ui_url'] + base + '&trace=' + trace)
    failed_drawer = page.get_by_role('dialog', name='Trace', exact=True)
    expect(failed_drawer.get_by_role('heading', name='Couldn’t load this trace')).to_be_visible()
    expect(failed_drawer.get_by_text('ObservabilityService/GetTrace', exact=True)).to_be_visible()
    failed_drawer.get_by_role('button', name='Copy trace ID ' + trace, exact=True).click()
    assert page.evaluate('navigator.clipboard.readText()') == trace
    mode[0] = 'normal'
    failed_drawer.get_by_role('button', name='Retry', exact=True).click()
    expect(drawer).to_be_visible()
    drawer.get_by_role('button', name='Close', exact=True).click()
    mode[0] = 'drawer-empty'
    page.goto(state['ui_url'] + base + '&trace=' + trace)
    gone = page.get_by_role('dialog')
    expect(gone.get_by_role('heading', name='This trace is no longer available')).to_be_visible()
    limits = evidence.rpc(state['api_url'], state['users']['owner']['token'], 'loco.org.v1.OrgService/GetOrgLimits', {'orgId': org_id})['limits']['limits']
    expect(gone.get_by_text(f"Traces are kept for {limits['traceRetentionDays']} days. It may also have been dropped before it was stored.", exact=True)).to_be_visible()
    gone.get_by_role('button', name='Back to traces', exact=True).click()
    expect(gone).to_have_count(0)
    mode[0] = 'empty'
    page.get_by_role('button', name='Refresh', exact=True).click()
    page.get_by_text('Send your first trace', exact=True).wait_for()
    expect(page.get_by_text('Loco configures the collector connection by default. Add instrumentation to your app, or configure its SDK endpoint yourself.', exact=True)).to_be_visible()
    page.get_by_role('tab', name='Python', exact=True).click()
    expect(page.get_by_text('opentelemetry-instrument python app.py', exact=False)).to_be_visible()
    mode[0] = 'error'
    page.get_by_role('button', name='Refresh', exact=True).click()
    page.get_by_text('Trace storage is temporarily unavailable', exact=False).wait_for(timeout=20000)
    expect(page.get_by_role('heading', name='Couldn\u2019t load traces', exact=True)).to_be_visible()
    expect(page.get_by_text('ObservabilityService/ListTraces', exact=True)).to_be_visible()
    mode[0] = 'normal'
    page.get_by_role('button', name='Retry', exact=True).click()
    open_trace.wait_for()
    with page.expect_response(lambda response: response.url.endswith('/ListTraces') and response.request.post_data_json.get('query') == '@resource:not-a-resource') as filtered:
        page.goto(state['ui_url'] + base + '&r=not-a-resource')
    assert filtered.value.request.post_data_json['query'] == '@resource:not-a-resource'
    page.goto(state['ui_url'] + base)
    open_trace.wait_for()
    context.close()
    mode[0] = 'paged'
    context, page, _ = open_page(browser, state=state)
    context.route('**/loco.observability.v1.ObservabilityService/*Trace*', handle)
    with page.expect_response("**/ListTraces"):
        page.goto(state['ui_url'] + base)
    assert last_page_request().get('limit') == 25
    expect(page.get_by_text('1–25 of 67', exact=True)).to_be_visible()
    expect(page.get_by_role('region', name='Trace list').locator('button[aria-label^="Open trace "]')).to_have_count(25)
    page.get_by_role('separator', name='Resize column Root span').press('ArrowRight')
    expect(page).to_have_url(re.compile(r'widths=span:276'))
    with page.expect_response("**/ListTraces"):
        page.get_by_role('button', name='Next page', exact=True).click()
    assert last_page_request()['offset'] == 25
    expect(page.get_by_text('26–50 of 67', exact=True)).to_be_visible()
    with page.expect_response("**/ListTraces"):
        page.get_by_role('button', name='Last page', exact=True).click()
    assert last_page_request()['offset'] == 50
    expect(page.get_by_role('region', name='Trace list').locator('button[aria-label^="Open trace "]')).to_have_count(17)
    expect(page.get_by_text('51–67 of 67', exact=True)).to_be_visible()
    expect(page.get_by_role('button', name=re.compile(r'^All\s*67$'))).to_be_visible()
    with page.expect_response("**/ListTraces"):
        page.get_by_role('button', name='Duration', exact=True).click()
    assert last_page_request().get('offset', 0) == 0
    assert last_page_request()['sort'] == 'TRACE_SUMMARY_SORT_DURATION'
    assert not last_page_request().get('ascending')
    with page.expect_response("**/ListTraces"):
        page.get_by_role('button', name=re.compile(r'^Duration\W*sorted descending$')).click()
    assert last_page_request()['ascending'] is True
    for label, key in [('Root span', 'OPERATION'), ('Resource', 'RESOURCE'), ('Status', 'STATUS'), ('Spans', 'SPAN_COUNT')]:
        with page.expect_response("**/ListTraces"):
            page.get_by_role('button', name=label, exact=True).click()
        assert last_page_request()['sort'] == 'TRACE_SUMMARY_SORT_' + key
        assert not last_page_request().get('ascending')
        with page.expect_response("**/ListTraces"):
            page.get_by_role('button', name=re.compile('^' + label + r'\W*sorted descending$')).click()
        assert last_page_request()['ascending'] is True
    with page.expect_response("**/ListTraces"):
        page.get_by_role('button', name=re.compile(r'^Errors')).click()
    assert last_page_request()['query'] == '@status:error'
    with page.expect_response(lambda response: response.url.endswith("/ListTraces") and response.request.post_data_json.get("query") == "@duration:>100" and response.request.post_data_json.get("limit") == 25):
        page.goto(state['ui_url'] + base + '&q=' + urllib.parse.quote('@duration:>100'))
    token_remove = page.get_by_role('button', name='Remove duration >100', exact=True)
    expect(page.get_by_role('heading', level=1)).to_have_text('Traces')
    page.goto(state['ui_url'] + base)
    chart = page.get_by_role('img', name=re.compile(r'^Traces per \d+-second bucket, 14 in range with 2 errors'))
    expect(chart).to_be_visible()
    expect(page.get_by_text('14% errors', exact=True)).to_be_visible()
    expect(page.get_by_text('360.00 ms', exact=True)).to_be_visible()
    box = chart.bounding_box()
    chart.hover(position={'x': box['width'] - 2, 'y': box['height'] / 2})
    expect(page.get_by_text(re.compile(r'^\d\d:\d\d(:\d\d)?–\d\d:\d\d(:\d\d)? · 5 traces · 1 error · p50 80\.00 ms · p95 360\.00 ms$'))).to_be_visible()
    chart.hover(position={'x': 2, 'y': 2})
    page.mouse.down()
    page.mouse.move(box['x'] + box['width'] - 2, box['y'] + box['height'] * 0.8, steps=5)
    with page.expect_request(lambda request: request.url.endswith('/QueryTraceBuckets')) as zoom_request:
        page.mouse.up()
    assert '@duration' not in zoom_request.value.post_data_json.get('query', ''), zoom_request.value.post_data_json
    expect(page).to_have_url(re.compile(r'[?&]from='))
    zoomed = urllib.parse.parse_qs(urllib.parse.urlparse(page.url).query)
    assert 'from' in zoomed and 'to' in zoomed, page.url
    assert '@duration' not in zoomed.get('q', [''])[0], zoomed
    page.get_by_role('button', name='Reset zoom', exact=True).click()
    expect(page).not_to_have_url(re.compile(r'(?:%40|@)duration|[?&]from='))
    assert '@duration' not in urllib.parse.unquote(page.url)
    assert 'from=' not in page.url
    expect(page.get_by_role('button', name='Reset zoom', exact=True)).to_have_count(0)
    with page.expect_response(lambda response: response.url.endswith('/ListTraces') and response.request.post_data_json.get('query') == 'products') as filtered:
        page.goto(state['ui_url'] + base + '&q=products')
    assert filtered.value.request.post_data_json['query'] == 'products'
    expect(page.get_by_role('button', name='Open trace GET /api/products', exact=True)).to_be_visible()
    context.close()
    mode[0] = 'normal'
    environments = evidence.rpc(state['api_url'], state['users']['owner']['token'], 'loco.environment.v1.EnvironmentService/ListEnvironments', {'workspaceId': workspace_id})['environments']
    staging = next((environment['id'] for environment in environments if environment['name'] == 'staging'), None)
    if staging is None:
        staging = evidence.rpc(state['api_url'], state['users']['owner']['token'], 'loco.environment.v1.EnvironmentService/CreateEnvironment', {'workspaceId':workspace_id,'name':'staging','type':'ENVIRONMENT_TYPE_STAGING'})['environmentId']
    context, page, _ = open_page(browser, state=state, storage={"loco:env:v1:" + workspace_id: "staging"})
    context.route('**/loco.observability.v1.ObservabilityService/*Trace*',handle)
    with page.expect_response(lambda response: response.url.endswith('/GetTrace') and response.request.post_data_json.get('scope', {}).get('environmentId') == staging):
        page.goto(state['ui_url'] + base + '&trace=' + trace)
    wait_for_query(page, lambda query: query.get('env') == ['staging'] and query.get('trace') == [trace])
    shared_url = page.url
    assert 'env=staging' in shared_url
    assert recorded[-1]['scope']['environmentId'] == staging
    context.close()
    context, page, _ = open_page(browser, state=state)
    context.route('**/loco.observability.v1.ObservabilityService/*Trace*',handle)
    with page.expect_response("**/loco.observability.v1.ObservabilityService/*Trace*"):
        page.goto(shared_url)
    assert recorded[-1]['scope']['environmentId'] == staging
    context.close()
    raw_service = 'resource-' + workspace_id
    spans[0]['serviceName'] = raw_service
    spans[0]['resourceAttributes']['service.name'] = raw_service
    context, page, _ = open_page(browser, narrow=True, state=state)
    context.route('**/loco.observability.v1.ObservabilityService/*Trace*',handle)
    page.goto(state['ui_url'] + base + '&trace=' + trace + '&span=' + spans[0]['spanId'])
    page.get_by_role('dialog', name=root_name).get_by_role('region', name='Span attributes').get_by_role('tab', name='Telemetry', exact=True).click()
    page.get_by_role('dialog', name=root_name).get_by_role('region', name='Span attributes').get_by_text(raw_service, exact=True).first.wait_for()
    assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth')
    context.close()
    mode[0] = 'normal'
    context, page, _ = open_page(browser, state=state)
    original_spans = json.loads(json.dumps(spans))
    for row in spans:
        row['resourceId'] = workspace_id
        row['resourceAttributes'].update({'k8s.pod.name': 'checkout-abc-123', 'k8s.node.name': 'worker-1', 'k8s.pod.start_time': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(start - 3600)), 'loco.region': 'us-east-1'})
    spans[6]['resourceAttributes'].pop('k8s.pod.name')
    spans[5]['resourceAttributes']['k8s.pod.name'] = 'catalog-def-456'
    context.route('**/loco.resource.v1.ResourceService/ListWorkspaceResources', lambda route: route.fulfill(json={'resources': [{'id': workspace_id, 'workspaceId': workspace_id, 'name': 'checkout', 'type': 'RESOURCE_TYPE_SERVICE', 'spec': {'service': {'regions': {'us-east-1': {'enabled': True, 'cpu': '500m', 'memory': '512Mi'}}}}}]}))

    context.route('**/loco.observability.v1.ObservabilityService/*Trace*', handle)
    log_entries = [
        {'timestamp':stamp(0.000456), 'severity':'INFO','body':'Trace started','resourceId':workspace_id,'traceId':trace,'spanId':spans[0]['spanId']},
        {'timestamp':stamp(172), 'severity':'ERROR','body':'Payment request failed','resourceId':workspace_id,'traceId':trace,'spanId':spans[10]['spanId']},
        {'timestamp':stamp(35), 'severity':'INFO','body':'Product lookup complete','resourceId':workspace_id,'traceId':trace,'spanId':spans[5]['spanId']},
    ]
    log_requests = []
    def trace_logs(route):
        log_requests.append(route.request.post_data_json)
        route.fulfill(json={'entries':log_entries,'totalMatched':str(len(log_entries))})
    context.route('**/loco.observability.v1.ObservabilityService/QueryLogs', trace_logs)
    metric_requests = []
    metric_mode = ['normal']
    def metrics(route):
        body = route.request.post_data_json
        metric_requests.append(body)
        if metric_mode[0] == 'error':
            route.fulfill(status=500, json={'code': 'internal', 'message': 'Replica metrics unavailable'})
            return
        if metric_mode[0] == 'empty':
            route.fulfill(json={'series': []})
            return
        values = [0.3, 0.92, 0.45] if body['metricName'] == 'k8s.pod.cpu_limit_utilization' else [0.4, 0.6, 0.5] if body['metricName'] == 'k8s.pod.memory_limit_utilization' else [1024, 2048, 512] if body['attributes'].get('direction') == 'receive' else [512, 1024, 256]
        points = [{'timestamp': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(start + offset)), 'value': value} for offset, value in zip((-60, 60, 120), values)]
        route.fulfill(json={'series': [{'resourceId': workspace_id, 'points': points}]})
    context.route('**/loco.observability.v1.ObservabilityService/QueryMetrics', metrics)
    page.mouse.move(0, 0)
    page.goto(state['ui_url'] + base + '&trace=' + trace)
    drawer = page.get_by_role('dialog', name=root_name)
    log_rows = drawer.get_by_role('region', name='Logs in this trace')
    expect(log_rows.get_by_role('button', name='Open log: Payment request failed')).to_be_visible()
    expect(drawer.get_by_role('button', name=re.compile('^Log at .*Trace started$'))).to_be_visible()
    expect(log_rows.get_by_role('button', name='Open log: Trace started')).to_contain_text('+333 ns')
    failing_button = drawer.get_by_role('region', name='Flame graph').get_by_role('button', name=failing)
    assert len(log_requests) == 1, log_requests
    assert log_requests[0]['query'] == '@trace:' + trace, log_requests
    expect(failing_button).not_to_have_attribute('data-linked', 'true')
    log_rows.get_by_role('button', name='Open log: Payment request failed').dispatch_event('mousemove')
    expect(failing_button).to_have_attribute('data-linked', 'true')
    log_rows.get_by_role('button', name='Open log: Payment request failed').dispatch_event('mouseout')
    log_rows.get_by_role('button', name='Open log: Payment request failed').hover()
    expect(failing_button).to_have_attribute('data-linked', 'true')
    marker = drawer.get_by_role('button', name=re.compile('^Log at .*Payment request failed$'))
    marker.click()
    expect(page).to_have_url(re.compile('span=' + spans[10]['spanId']))
    expect(drawer.get_by_role('complementary', name='Log line')).to_be_visible()
    expect(drawer.get_by_role('button', name=re.compile('^Add .* as a column$'))).to_have_count(0)
    drawer.get_by_role('complementary', name='Log line').get_by_role('button', name='Close', exact=True).click()
    drawer.get_by_role('button', name='This span', exact=True).click()
    expect(log_rows.get_by_role('button')).to_have_count(1)
    drawer.get_by_role('region', name='Flame graph').get_by_role('button', name=re.compile('^currency Convert')).click()
    expect(log_rows.get_by_text('No logs from this span or its children.', exact=True)).to_be_visible()
    expect(drawer.get_by_role('separator', name='Resize bottom panel')).to_have_attribute('aria-valuenow', '310')
    drawer.get_by_role('separator', name='Resize bottom panel').press('ArrowUp')
    expect(drawer.get_by_role('separator', name='Resize bottom panel')).to_have_attribute('aria-valuenow','334')
    drawer.get_by_role('button', name='Collapse bottom panel').click()
    expect(drawer.get_by_role('button', name='Expand bottom panel')).to_be_visible()
    drawer.get_by_role('button', name='Expand bottom panel').click()
    drawer.get_by_role('tab', name='Infrastructure', exact=True).click()
    expect(drawer.get_by_role('tablist', name='Trace bottom panel')).to_be_visible()
    expect(drawer.get_by_role('tab', name='Infrastructure', exact=True)).to_have_attribute('aria-selected', 'true')
    drawer.get_by_role('tab', name='Infrastructure', exact=True).press('ArrowLeft')
    expect(drawer.get_by_role('tab', name=re.compile('^Logs '))).to_be_focused()
    drawer.get_by_role('tab', name=re.compile('^Logs ')).press('Enter')
    expect(drawer.get_by_role('tab', name=re.compile('^Logs '))).to_have_attribute('aria-selected', 'true')
    drawer.get_by_role('tab', name=re.compile('^Logs ')).press('ArrowRight')
    expect(drawer.get_by_role('tab', name='Infrastructure', exact=True)).to_be_focused()
    drawer.get_by_role('tab', name='Infrastructure', exact=True).press('Enter')
    expect(drawer.get_by_role('tabpanel', name='Infrastructure', exact=True)).to_be_visible()
    infrastructure = drawer.get_by_role('region', name='Trace infrastructure')
    expect(infrastructure.get_by_text('No replica identity was recorded for this span.', exact=True)).to_be_visible()
    drawer.get_by_role('region', name='Flame graph').get_by_role('button', name=failing).click()
    expect(infrastructure.get_by_text(re.compile('Up .* when this ran'))).to_be_visible()
    expect(infrastructure.get_by_role('region', name='Events on checkout-abc-123', exact=True)).to_be_visible()
    expect(infrastructure.get_by_role('button', name=re.compile('Replica events'))).to_be_disabled()
    cpu = infrastructure.get_by_role('region', name='CPU for checkout-abc-123', exact=True)
    memory = infrastructure.get_by_role('region', name='Memory for checkout-abc-123', exact=True)
    network = infrastructure.get_by_role('region', name='Network for checkout-abc-123', exact=True)
    for chart, title in ((cpu, 'CPU'), (memory, 'Memory'), (network, 'Network')):
        expect(chart.get_by_role('group', name=title + ' usage', exact=True)).to_be_visible()
    expect(cpu.get_by_label('CPU at trace start')).to_have_text('30%')
    expect(cpu.get_by_text('Peak 92% · High', exact=True)).to_be_visible()
    expect(memory.get_by_label('Memory at trace start')).to_have_text('40%')
    expect(memory.get_by_text('Peak 60%', exact=True)).to_be_visible()
    expect(cpu.get_by_text('Limit 100%', exact=True)).to_be_visible()
    assert len(metric_requests) == 4, metric_requests
    assert all(request['resourceIds'] == [workspace_id] and request['attributes']['k8s.pod.name'] == 'checkout-abc-123' for request in metric_requests)
    assert {request['metricName'] for request in metric_requests} == {'k8s.pod.cpu_limit_utilization', 'k8s.pod.memory_limit_utilization', 'k8s.pod.network.io'}
    network_requests = [request for request in metric_requests if request['metricName'] == 'k8s.pod.network.io']
    assert {request['attributes']['direction'] for request in network_requests} == {'receive', 'transmit'}
    assert all(request['aggregation'] == 'rate' for request in network_requests)
    grid = cpu.locator('.recharts-cartesian-grid').bounding_box()
    page.mouse.move(grid['x'] + grid['width'] * 0.55, grid['y'] + grid['height'] / 2, steps=3)
    expect(cpu.get_by_label('CPU at cursor')).to_contain_text('92% at ')
    expect(memory.get_by_label('Memory at cursor')).to_contain_text('60% at ')
    expect(network.get_by_label('Network at cursor')).to_contain_text(' B/s at ')
    cursor_times = [chart.get_by_label(title + ' at cursor').inner_text().split(' at ')[-1] for chart, title in ((cpu, 'CPU'), (memory, 'Memory'), (network, 'Network'))]
    assert len(set(cursor_times)) == 1, cursor_times
    window = infrastructure.get_by_role('group', name='Infrastructure window')
    expect(window.get_by_role('button', name='±10m', exact=True)).to_have_attribute('aria-pressed', 'true')
    window.get_by_role('button', name='±1h', exact=True).click()
    expect(network.get_by_role('group', name='Network usage', exact=True)).to_be_visible()
    assert len(metric_requests) == 8, metric_requests
    assert all(7200 <= iso_seconds(request['endTime']) - iso_seconds(request['startTime']) <= 7201 for request in metric_requests[4:])
    drawer.get_by_role('region', name='Flame graph').get_by_role('button', name=re.compile('^product-catalog GetProduct')).click()
    expect(infrastructure.get_by_role('region', name='Events on catalog-def-456', exact=True)).to_be_visible()
    expect(window.get_by_role('button', name='±1h', exact=True)).to_have_attribute('aria-pressed', 'true')
    expect(infrastructure.get_by_role('group', name='Network usage', exact=True)).to_be_visible()
    assert len(metric_requests) == 12, metric_requests
    assert all(request['attributes']['k8s.pod.name'] == 'catalog-def-456' for request in metric_requests[8:])
    page.set_viewport_size({'width': 390, 'height': 844})
    infrastructure.get_by_role('region', name='Network for catalog-def-456', exact=True).scroll_into_view_if_needed()
    assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth')
    page.set_viewport_size({'width': 1280, 'height': 800})
    metric_mode[0] = 'empty'
    window.get_by_role('button', name='±5m', exact=True).click()
    expect(infrastructure.get_by_text('No metrics in this range', exact=True)).to_have_count(3)
    metric_mode[0] = 'error'
    window.get_by_role('button', name='±30m', exact=True).click()
    expect(infrastructure.get_by_role('alert').first).to_contain_text('Replica metrics unavailable', timeout=20000)
    metric_mode[0] = 'normal'
    window.get_by_role('button', name='±10m', exact=True).click()
    expect(infrastructure.get_by_role('group', name='Network usage', exact=True)).to_be_visible()
    drawer.get_by_role('tab', name=re.compile('^Logs ')).click()
    assert len(log_requests) == 1, log_requests
    marker.click()
    drawer.get_by_role('complementary', name='Log line').get_by_role('button', name='More for this line').click()
    page.get_by_role('menuitem', name=re.compile('^Logs around this line')).click()
    wait_for_query(page, lambda query: query.get('view') == ['logs'] and query.get('q') == ['@trace:' + trace])
    page.go_back()
    marker.click()
    drawer.get_by_role('complementary', name='Log line').get_by_role('button', name='Only level error', exact=True).click()
    wait_for_query(page, lambda query: query.get('view') == ['logs'] and '@trace:' + trace in query.get('q', [''])[0] and '@level:error' in query.get('q', [''])[0])
    page.set_viewport_size({'width':390,'height':844})
    page.goto(state['ui_url'] + base + '&trace=' + trace)
    drawer = page.get_by_role('dialog', name=root_name)
    narrow_flame = drawer.get_by_role('region', name='Flame graph')
    expect(narrow_flame.get_by_role('button', name=failing)).to_be_visible()
    assert narrow_flame.bounding_box()['height'] > 100
    narrow_flame.hover(position={'x':30,'y':30})
    ancestors = """el => {const values=[]; for(let p=el.parentElement;p;p=p.parentElement) values.push(p.scrollTop); return values;}"""
    before_hover = narrow_flame.evaluate(ancestors)
    box = narrow_flame.bounding_box()
    page.mouse.move(box['x']+200,box['y']+40,steps=20)
    assert narrow_flame.evaluate(ancestors) == before_hover

    message = drawer.get_by_role('button', name='Open log: Payment request failed').locator(':scope > span').last
    assert message.bounding_box()['width'] > 200
    assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth')
    context.close()
    spans[:] = original_spans
    mode[0] = 'zero'
    context, page, errors = open_page(browser, state=state)
    context.route('**/loco.observability.v1.ObservabilityService/*Trace*', handle)
    page.goto(state['ui_url'] + base + '&trace=' + trace)
    zero = page.get_by_role('dialog', name=root_name).get_by_role('region', name='Flame graph').get_by_role('button').first
    expect(zero).to_be_visible()
    assert zero.bounding_box()['width'] > 0
    zero.click()
    expect(page).to_have_url(re.compile('span=' + spans[0]['spanId']))
    assert not errors, errors
    context.close()
    mode[0] = 'large'
    context, page, errors = open_page(browser, state=state)
    context.route('**/loco.observability.v1.ObservabilityService/*Trace*', handle)
    page.goto(state['ui_url'] + base + '&trace=' + trace)
    drawer = page.get_by_role('dialog', name=root_name)
    large_flame = drawer.get_by_role('region', name='Flame graph')
    expect(large_flame.get_by_role('button')).to_have_count(3001)
    first = large_flame.get_by_role('button').first
    first.hover()
    before_top = first.bounding_box()['y']
    page.mouse.wheel(0, 400)
    page.wait_for_function("""top => document.querySelector('[aria-label="Flame graph"] button').getBoundingClientRect().top < top""", arg=before_top)
    expect(drawer.get_by_role('button', name='Reset zoom', exact=True)).to_have_count(0)
    frames = page.locator('[data-trace-plot]').evaluate("""async plot => {
        const samples=[];
        for(let i=0;i<20;i++) {
            const start=performance.now();
            plot.querySelectorAll('[data-span-id]')[i+1].dispatchEvent(new PointerEvent('pointermove',{bubbles:true,clientX:100+i,clientY:300}));
            await new Promise(requestAnimationFrame);
            samples.push(performance.now()-start);
        }
        return samples;
    }""")
    print('Large trace pointer frame samples (ms):', frames)
    assert not errors, errors
    context.close()
    mode[0] = 'many'
    context, page, _ = open_page(browser, state=state)
    context.route('**/loco.observability.v1.ObservabilityService/*Trace*',handle)
    page.goto(state['ui_url'] + base + '&trace=' + trace)
    flame = page.get_by_role('dialog', name=root_name).get_by_role('region', name='Flame graph')
    expect(flame.get_by_role('button')).to_have_count(114)
    many_drawer = page.get_by_role('dialog', name=root_name)
    many_drawer.get_by_role('button', name='Span list', exact=True).click()
    span_list = many_drawer.get_by_role('region', name='Span list')
    expect(span_list.locator('tbody tr')).to_have_count(25)
    span_list.get_by_role('button', name='Next page', exact=True).click()
    expect(span_list.get_by_text('26–50 of 114', exact=True)).to_be_visible()
    span_list.get_by_role('button', name='Last page', exact=True).click()
    expect(span_list.locator('tbody tr')).to_have_count(14)
    span_list.get_by_role('button', name='First page', exact=True).click()
    span_list.get_by_role('separator', name='Resize Operation', exact=True).press('ArrowRight')
    span_list.get_by_role('button', name='Operation', exact=True).click()
    assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth')
    context.close()
    mode[0] = 'normal'
    context, page, _ = open_page(browser, state=state)
    context.route('**/loco.observability.v1.ObservabilityService/*Trace*', handle)
    page.clock.install()
    recent_save_ms = 1500
    search = page.get_by_role('combobox', name='Search and filter')
    suggestions = page.get_by_role('listbox', name='Suggestions')
    recent = suggestions.get_by_role('option').filter(has_text='checkout')
    page.goto(state['ui_url'] + base + '&q=checkout')
    expect(search).to_have_value('checkout')
    page.clock.run_for(recent_save_ms)
    page.goto(state['ui_url'] + base + '&q=quick')
    page.get_by_role('button', name='Clear', exact=True).click()
    page.clock.run_for(recent_save_ms)
    search.click()
    expect(recent).to_have_count(1)
    expect(suggestions.get_by_role('option').filter(has_text='quick')).to_have_count(0)
    page.goto(state['ui_url'] + base.replace('view=traces', 'view=logs'))
    search.click()
    expect(suggestions.get_by_role('option').filter(has_text='checkout')).to_have_count(0)
    page.goto(state['ui_url'] + base)
    search.click()
    expect(recent).to_have_count(1)
    page.get_by_role('button', name='Clear recent searches', exact=True).click()
    expect(recent).to_have_count(0)
    page.reload()
    search.click()
    expect(recent).to_have_count(0)
    search.press_sequentially('chec')
    expect(suggestions.get_by_role('option', name='Search root span names for “chec”')).to_be_visible()
    expect(page.get_by_role('button', name='Pin search')).to_be_visible()
    search.fill('')
    page.keyboard.press('Escape')
    expect(page.get_by_role('button', name='Pin search')).to_have_count(0)
    page.goto(state['ui_url'] + base + '&q=checkout')
    expect(search).to_have_value('checkout')
    page.clock.run_for(recent_save_ms)
    page.get_by_role('button', name='Pin search').click()
    expect(page.locator('[data-sonner-toast]').filter(has_text='Pinned')).to_be_visible()
    page.goto(state['ui_url'] + base)
    search.click()
    expect(recent).to_have_count(1)
    suggestions.get_by_role('button', name='Unpin checkout').click()
    expect(suggestions.get_by_role('button', name='Remove checkout')).to_be_visible()
    suggestions.get_by_role('button', name='Remove checkout').click()
    expect(recent).to_have_count(0)
    context.close()
    mode[0] = 'concurrent'
    for narrow in (False, True):
        context, page, errors = open_page(browser, narrow=narrow, state=state)
        context.route('**/loco.observability.v1.ObservabilityService/*Trace*', handle)
        page.goto(state['ui_url'] + base + '&trace=' + trace)
        drawer = page.get_by_role('dialog', name='GET /recommendations')
        flame = drawer.get_by_role('region', name='Flame graph')
        expect(flame.get_by_role('button')).to_have_count(len(concurrent_spans))
        boxes = flame.get_by_role('button').evaluate_all('(buttons) => buttons.map(b => {const r=b.getBoundingClientRect(); return {x:r.x,y:r.y,right:r.right,bottom:r.bottom}})')
        for i, a in enumerate(boxes):
            for b in boxes[i+1:]:
                assert a['bottom'] <= b['y'] or b['bottom'] <= a['y'] or a['right'] <= b['x'] + 0.1 or b['right'] <= a['x'] + 0.1, (a,b)
        assert flame.get_by_role('button').evaluate_all("buttons => buttons.every(b => [...b.querySelectorAll('span')].every(s => {const r=s.getBoundingClientRect(),p=b.getBoundingClientRect();return r.width===0 || (r.x>=p.x-1 && r.right<=p.right+1 && r.y>=p.y-1 && r.bottom<=p.bottom+1)}))")
        drawer.get_by_role('button', name='Waterfall', exact=True).click()
        waterfall = drawer.get_by_role('region', name='Waterfall')
        expect(waterfall.locator('[data-span]')).to_have_count(len(concurrent_spans))
        assert waterfall.locator('[data-span]').evaluate_all("buttons => buttons.every(b => b.clientHeight === b.scrollHeight)")
        assert waterfall.locator('button > span.absolute').evaluate_all("bars => bars.every(b => b.clientHeight >= b.scrollHeight)")
        assert not errors, errors
        context.close()
    browser.close()
assert all((row.get('scope', {}).get('environmentId') for row in recorded))
assert all((not row.get('scope', {}).get('resourceIds') for row in recorded if row.get('traceId')))
print('PASS browser trace list, complete drawer spans, span selection and attributes, span attribute filters, query text requests, frozen bounds, whole-row trace buttons, resizable trace columns, recent queries per view with per-item removal, pinning and unpinning, typed-text suggestions, and production UI; trace responses use explicit browser fixtures.')
