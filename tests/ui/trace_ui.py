import argparse
import atexit
import contextlib
import importlib.util
import io
import os
import sys
import json
import time
from pathlib import Path
import re
import urllib.parse
from playwright.sync_api import expect, sync_playwright
sys.dont_write_bytecode = True
repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description='Exercise the production trace viewer with fixture trace responses')
parser.add_argument('--state')
parser.add_argument('--keep', action='store_true')
parser.add_argument('--out', default=str(repo / 'tests/ui/out/trace-ui'))
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location('evidence', repo / '.agents/skills/ui-evidence/scripts/evidence.py')
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref='HEAD', name='after', scenario=str(repo / '.agents/skills/ui-evidence/scenario.example.json')))
    args.state = output.getvalue().strip().splitlines()[-1]
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
    print('Preview state:', args.state, flush=True)
state = json.loads(Path(args.state).read_text())
orgs = evidence.rpc(state['api_url'], state['users']['owner']['token'], 'loco.org.v1.OrgService/ListUserOrgs', {'userId': state['users']['owner']['id'], 'pageSize': 200})
org_id = orgs['orgs'][0]['id']
workspace_id = state['workspaces']['acme/shared']
base = f'/org/{org_id}/wks/{workspace_id}/observability?view=traces'
trace = '4bf92f3577b34da6a3ce929d0e0e4736'
start = int(time.time()) - 60
rows = [('0001', '', 'frontend-proxy', 'POST /api/checkout', 0, 427.6), ('0002', '0001', 'frontend', 'POST /api/checkout', 1.2, 424.1), ('0003', '0002', 'checkout', 'oteldemo.CheckoutService/PlaceOrder', 5.3, 412.0), ('0004', '0003', 'cart', 'GetCart', 8.4, 24.5), ('0005', '0004', 'valkey', 'GET cart:session', 10.1, 7.8), ('0006', '0003', 'product-catalog', 'GetProduct · OLJCESPC7Z', 34.1, 18.9), ('0007', '0003', 'currency', 'Convert · USD → EUR', 58.7, 12.4), ('0008', '0003', 'shipping', 'GetQuote', 76.3, 86.2), ('0009', '0008', 'quote', 'POST /quote', 79.5, 71.4), ('000a', '0003', 'payment', 'Charge · attempt 1', 166.8, 96.1), ('000b', '000a', 'payment', 'POST /payments/authorize', 168.2, 91.7), ('000c', '0003', 'payment', 'Charge · attempt 2', 275.2, 78.6), ('000d', '0003', 'email', 'SendOrderConfirmation', 357.4, 45.2), ('000e', '0003', 'cart', 'EmptyCart', 405.1, 8.6)]

def stamp(offset):
    return f"{time.strftime('%Y-%m-%dT%H:%M:%S', time.gmtime(start))}.{int(offset * 1000000):09d}Z"

def span(row):
    sid, parent, service, name, offset, duration = row
    return {'traceId': trace, 'spanId': sid.zfill(16), 'parentSpanId': parent.zfill(16) if parent else '', 'name': name, 'kind': 'Server', 'serviceName': service, 'startTime': stamp(offset), 'durationNanos': str(int(duration * 1000000)), 'statusCode': 'Error' if sid in ('000a', '000b') else 'Ok', 'statusMessage': 'Payment authorization timed out after 90 ms' if sid == '000b' else '', 'spanAttributes': {'http.request.method': 'POST', 'http.response.status_code': '504' if sid == '000b' else '200', 'server.address': 'payments.internal', 'payment.attempt': '1'} if sid == '000b' else {'rpc.system': 'grpc', 'rpc.service': f'oteldemo.{service.title()}Service'}, 'resourceAttributes': {'service.name': service, 'service.version': '2.1.0', 'deployment.environment.name': 'production', 'cloud.region': 'us-east-1'}, 'scopeName': '@opentelemetry/instrumentation-http', 'scopeVersion': '0.210.0'}
spans = [span(row) for row in rows]
spans[10]['events'] = [
    {'name': 'retry.scheduled', 'timestamp': stamp(168.2 + 12.4), 'attributes': {'attempt': '1'}},
    {'name': 'exception', 'timestamp': stamp(168.2 + 90), 'attributes': {'exception.type': 'TimeoutError', 'exception.message': 'Authorization timed out', 'exception.stacktrace': 'TimeoutError: Authorization timed out\n    at authorize (payments.ts:42:7)\n    at checkout (checkout.ts:18:3)'}},
]
recorded = []
mode = ['normal']

def last_page_request():
    return next(row for row in reversed(recorded) if 'startTime' in row and row.get('limit') in (25, 50, 100))

def handle(route):
    body = route.request.post_data_json
    recorded.append(body)
    if mode[0] == 'error':
        route.fulfill(status=503, content_type='application/json', body=json.dumps({'code': 'unavailable', 'message': 'Trace storage is temporarily unavailable'}))
        return
    if mode[0] == 'empty':
        route.fulfill(json={'spans': []})
        return
    if mode[0] == 'many' and not route.request.url.endswith('/ListTraces'):
        route.fulfill(json={'spans': spans + [dict(spans[5],spanId=f'{index + 256:016x}') for index in range(100)]})
        return
    if route.request.url.endswith('/ListTraces'):
        summary = body
        others = [dict(spans[index], traceId=f'{index + 100:032x}', parentSpanId='', name=name, durationNanos=str(duration * 1000000)) for index, (name, duration) in enumerate([('GET /api/products', 18), ('GetRecommendations', 132), ('GetProduct', 9), ('GET /cart', 42), ('Convert · USD → EUR', 12), ('GetQuote', 86)])]
        summaries = [{'traceId': trace, 'rootSpanName': spans[0]['name'], 'serviceName': spans[0]['serviceName'], 'startTime': spans[0]['startTime'], 'durationNanos': '427600000', 'status': 'TRACE_STATUS_ERROR', 'spanCount': str(len(spans))}]
        summaries += [{'traceId': row['traceId'], 'rootSpanName': row['name'], 'serviceName': row['serviceName'], 'startTime': row['startTime'], 'durationNanos': row['durationNanos'], 'status': 'TRACE_STATUS_OK', 'spanCount': '1'} for row in others]
        if mode[0] == 'paged':
            summaries += [dict(summaries[1], traceId=f'{index + 1000:032x}', rootSpanName=f'Paged trace {index}') for index in range(60)]
        if body.get('traceId'):
            summaries = [row for row in summaries if row['traceId'] == body['traceId']]
        summaries = [row for row in summaries if all(term.lower() in row['rootSpanName'].lower() for term in summary.get('rootNameTerms', [])) and (not summary.get('status') or row['status'] == summary['status'])]
        summaries = [row for row in summaries if int(summary.get('minDurationNanos', 0)) <= int(row['durationNanos']) <= int(summary.get('maxDurationNanos', 10**18))]
        if summary.get('sort') == 'TRACE_SUMMARY_SORT_DURATION':
            summaries.sort(key=lambda row: (-int(row['durationNanos']), row['traceId']), reverse=bool(summary.get('ascending')))
        offset = summary.get('offset', 0)
        route.fulfill(json={'totalCount': str(len(summaries)), 'summaries': summaries[offset:offset + body['limit']]})
        return
    route.fulfill(json={'spans': spans})
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with sync_playwright() as p:
    browser = p.chromium.launch()
    root_name = 'POST /api/checkout'
    failing = re.compile(r'^payment POST \/payments\/authorize, .*, error$')
    for theme in ('light', 'dark'):
        for narrow in (False, True):
            context = browser.new_context(permissions=['clipboard-read', 'clipboard-write'], viewport=evidence.NARROW if narrow else evidence.DESKTOP, device_scale_factor=2, color_scheme=theme)
            auth = json.dumps({'idToken': state['users']['owner']['token'], 'refreshToken': None, 'expiresAt': evidence.SESSION_EXPIRY_MS})
            context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},{json.dumps(auth)});localStorage.setItem({json.dumps(evidence.MODE_KEY)},{json.dumps(theme)});')
            context.route('**/loco.observability.v1.ObservabilityProxyService/*Trace*', handle)
            page = context.new_page()
            errors = []
            page.on('pageerror', lambda err: errors.append(str(err)))
            page.goto(state['ui_url'] + base)
            evidence.settle(page)
            open_trace = page.get_by_role('region', name='Trace list').get_by_role('button', name='Open trace ' + root_name, exact=True)
            open_trace.wait_for()
            expect(page.get_by_role('heading', level=1)).to_have_text(re.compile(r'^Traces\s*7$'))
            suffix = f"{theme}{('-narrow' if narrow else '')}"
            page.screenshot(path=out / f'trace-list-after-{suffix}.png', full_page=True, animations='disabled')
            expect(open_trace).to_have_attribute('aria-pressed', 'false')
            open_trace.click(position={'x': 20, 'y': 10})
            drawer = page.get_by_role('dialog', name=root_name)
            assert 'trace=' + trace in page.url
            evidence.settle(page)
            assert drawer.evaluate('(element) => element.contains(document.activeElement)')
            if not narrow:
                page.screenshot(path=out / f'trace-open-after-{suffix}.png', full_page=True, animations='disabled')
            flame = drawer.get_by_role('region', name='Flame graph')
            expect(flame.get_by_role('button')).to_have_count(len(spans))
            flame.get_by_role('button', name=failing).click()
            evidence.settle(page)
            expect(flame.get_by_role('button', name=failing)).to_have_attribute('aria-pressed', 'true')
            assert 'span=' + spans[10]['spanId'] in page.url
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
            expect(attributes.get_by_text(spans[10]['spanId'], exact=True)).to_be_visible()
            expect(attributes.get_by_text('http.response.status_code', exact=True)).to_be_visible()
            expect(attributes.get_by_text('service.version', exact=True)).to_be_visible()
            expect(attributes.get_by_text('@opentelemetry/instrumentation-http', exact=True)).to_be_visible()
            page.screenshot(path=out / f'trace-detail-after-{suffix}.png', full_page=True, animations='disabled')
            drawer.get_by_role('button', name='More for this trace', exact=True).click()
            page.get_by_role('menuitem', name='Copy trace ID', exact=True).click()
            assert page.evaluate('navigator.clipboard.readText()') == trace
            assert not errors, errors
            if not narrow and theme == 'light':
                previous_url = page.url
                previous_request = dict(next(row for row in reversed(recorded) if "startTime" in row))
                page.keyboard.press('Escape')
                expect(drawer).to_have_count(0)
                assert 'trace=' not in page.url
                spans.append(dict(spans[5], spanId='0000000000000f01', name='Late exported span'))
                open_trace.click()
                evidence.settle(page)
                expect(flame.get_by_role('button')).to_have_count(len(spans))
                expect(flame.get_by_role('button', name=re.compile('Late exported span'))).to_have_count(1)
                drawer.get_by_role('button', name='Close', exact=True).click()
                expect(drawer).to_have_count(0)
                spans.pop()
                page.get_by_role('button', name='Past hour', exact=True).click()
                page.get_by_role('group', name='Presets').get_by_role('button', name='Past 15 minutes', exact=False).click()
                evidence.settle(page)
                assert 'range=15m' in page.url
                page.go_back()
                evidence.settle(page)
                assert 'range=15m' not in page.url
                page.goto(previous_url)
                evidence.settle(page)
                expect(flame.get_by_role('button', name=failing)).to_have_attribute('aria-pressed', 'true')
                drawer.get_by_role('button', name='Close', exact=True).click()
                expect(drawer).to_have_count(0)
                page.get_by_role('button', name='Refresh', exact=True).click()
                evidence.settle(page)
                assert next(row for row in reversed(recorded) if 'startTime' in row)['startTime'] != previous_request['startTime']
                page.goto(state['ui_url'] + base + '&q=' + urllib.parse.quote('@trace:invalid'))
                evidence.settle(page)
                expect(page.get_by_role('alert')).to_have_text('A trace ID is 32 lowercase hexadecimal characters.')
                assert not any(row.get('traceId') == 'invalid' for row in recorded)
                page.get_by_role('button', name='Clear filters', exact=True).click()
                evidence.settle(page)
                expect(open_trace).to_be_visible()
                page.goto(state['ui_url'] + base + '&trace=' + trace + '&span=ffffffffffffffff')
                evidence.settle(page)
                drawer.get_by_text('The selected span is unavailable.', exact=True).wait_for()
                drawer.get_by_role('button', name='Close', exact=True).click()
                expect(drawer).to_have_count(0)
            if drawer.count():
                drawer.get_by_role('button', name='Close', exact=True).click()
                expect(drawer).to_have_count(0)
            if not narrow:
                mode[0] = 'empty'
                page.get_by_role('button', name='Refresh', exact=True).click()
                evidence.settle(page)
                page.get_by_text('Send your first trace', exact=True).wait_for()
                expect(page.get_by_text('OTEL_EXPORTER_OTLP_ENDPOINT is already set on every replica', exact=True)).to_be_visible()
                page.get_by_role('tab', name='Python', exact=True).click()
                expect(page.get_by_text('opentelemetry-instrument python app.py', exact=False)).to_be_visible()
                page.screenshot(path=out / f'trace-empty-after-{suffix}.png', full_page=True, animations='disabled')
                mode[0] = 'error'
                page.get_by_role('button', name='Refresh', exact=True).click()
                page.get_by_text('Trace storage is temporarily unavailable', exact=False).wait_for(timeout=20000)
                evidence.settle(page)
                page.screenshot(path=out / f'trace-error-after-{suffix}.png', full_page=True, animations='disabled')
                mode[0] = 'normal'
                page.get_by_role('button', name='Retry', exact=True).click()
                open_trace.wait_for()
            if not narrow:
                previous_count = len(recorded)
                page.goto(state['ui_url'] + base + '&r=not-a-resource')
                evidence.settle(page)
                page.get_by_text("The filtered resources aren't in this workspace", exact=True).wait_for()
                assert len(recorded) == previous_count
                page.get_by_role('button', name='Clear filters', exact=True).click()
                evidence.settle(page)
                open_trace.wait_for()
            context.close()
    mode[0] = 'paged'
    context = browser.new_context(viewport=evidence.DESKTOP)
    auth = json.dumps({'idToken': state['users']['owner']['token'], 'refreshToken': None, 'expiresAt': evidence.SESSION_EXPIRY_MS})
    context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},{json.dumps(auth)});')
    context.route('**/loco.observability.v1.ObservabilityProxyService/*Trace*', handle)
    page = context.new_page()
    page.goto(state['ui_url'] + base)
    evidence.settle(page)
    assert last_page_request().get('limit') == 25
    expect(page.get_by_text('1–25 of 67', exact=True)).to_be_visible()
    expect(page.get_by_role('region', name='Trace list').locator('button[aria-label^="Open trace "]')).to_have_count(25)
    page.get_by_role('separator', name='Resize column Root span').press('ArrowRight')
    expect(page).to_have_url(re.compile(r'widths=span%3A276'))
    expect(page.get_by_role('region', name='Trace list').get_by_role('button', name=re.compile(r'^Spans'))).to_have_count(0)
    page.get_by_role('button', name='Next page', exact=True).click()
    evidence.settle(page)
    assert last_page_request()['offset'] == 25
    expect(page.get_by_text('26–50 of 67', exact=True)).to_be_visible()
    page.get_by_role('button', name='Last page', exact=True).click()
    evidence.settle(page)
    assert last_page_request()['offset'] == 50
    expect(page.get_by_role('region', name='Trace list').locator('button[aria-label^="Open trace "]')).to_have_count(17)
    expect(page.get_by_text('51–67 of 67', exact=True)).to_be_visible()
    expect(page.get_by_role('button', name=re.compile(r'^All\s*67$'))).to_be_visible()
    expect(page.get_by_role('button', name=re.compile(r'^Errors\s*1$'))).to_be_visible()
    page.get_by_role('button', name='Duration', exact=True).click()
    evidence.settle(page)
    assert last_page_request().get('offset', 0) == 0
    assert last_page_request()['sort'] == 'TRACE_SUMMARY_SORT_DURATION'
    assert not last_page_request().get('ascending')
    page.get_by_role('button', name='Duration ↓', exact=True).click()
    evidence.settle(page)
    assert last_page_request()['ascending'] is True
    expect(page.get_by_role('region', name='Trace list').locator('button[aria-label^="Open trace "]').first).to_contain_text('GetProduct')
    page.get_by_role('button', name=re.compile(r'^Errors')).click()
    evidence.settle(page)
    assert last_page_request()['status'] == 'TRACE_STATUS_ERROR'
    expect(page.get_by_role('region', name='Trace list').locator('button[aria-label^="Open trace "]')).to_have_count(1)
    page.goto(state['ui_url'] + base + '&q=' + urllib.parse.quote('@duration:>100'))
    evidence.settle(page)
    assert last_page_request()['minDurationNanos'] == '100000000'
    expect(page.get_by_role('heading', level=1)).to_have_text(re.compile(r'^Traces\s*2$'))
    page.goto(state['ui_url'] + base + '&q=' + urllib.parse.quote('@duration:100'))
    evidence.settle(page)
    expect(page.get_by_role('alert')).to_contain_text('A duration filter compares in milliseconds')
    page.goto(state['ui_url'] + base)
    evidence.settle(page)
    chart = page.get_by_role('img', name=re.compile(r'^Duration of \d+ traces'))
    box = chart.bounding_box()
    page.mouse.move(box['x'] + 2, box['y'] + 2)
    page.mouse.down()
    page.mouse.move(box['x'] + box['width'] - 2, box['y'] + box['height'] * 0.8, steps=5)
    page.mouse.up()
    evidence.settle(page)
    zoomed = urllib.parse.parse_qs(urllib.parse.urlparse(page.url).query)
    assert 'from' in zoomed and 'to' in zoomed, page.url
    assert '@duration:>=' in zoomed['q'][0] and '@duration:<=' in zoomed['q'][0], zoomed
    # The zoom's request can land after the page settles, so wait for it.
    for _ in range(50):
        if 'minDurationNanos' in last_page_request() and 'maxDurationNanos' in last_page_request():
            break
        page.wait_for_timeout(100)
    assert 'minDurationNanos' in last_page_request() and 'maxDurationNanos' in last_page_request()
    page.get_by_role('button', name='Reset zoom', exact=True).click()
    evidence.settle(page)
    assert '@duration' not in urllib.parse.unquote(page.url)
    assert 'from=' not in page.url
    expect(page.get_by_role('button', name='Reset zoom', exact=True)).to_have_count(0)
    page.goto(state['ui_url'] + base + '&q=products')
    evidence.settle(page)
    assert last_page_request()['rootNameTerms'] == ['products']
    expect(page.get_by_role('button', name='Open trace GET /api/products', exact=True)).to_be_visible()
    context.close()
    mode[0] = 'normal'
    environments = evidence.rpc(state['api_url'], state['users']['owner']['token'], 'loco.environment.v1.EnvironmentService/ListEnvironments', {'workspaceId': workspace_id})['environments']
    staging = next((environment['id'] for environment in environments if environment['name'] == 'staging'), None)
    if staging is None:
        staging = evidence.rpc(state['api_url'], state['users']['owner']['token'], 'loco.environment.v1.EnvironmentService/CreateEnvironment', {'workspaceId':workspace_id,'name':'staging','type':'ENVIRONMENT_TYPE_STAGING'})['environmentId']
    context = browser.new_context()
    auth = json.dumps({'idToken':state['users']['owner']['token'],'refreshToken':None,'expiresAt':evidence.SESSION_EXPIRY_MS})
    context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},{json.dumps(auth)});localStorage.setItem({json.dumps("loco:env:v1:"+workspace_id)},"staging");')
    context.route('**/loco.observability.v1.ObservabilityProxyService/*Trace*',handle)
    page = context.new_page()
    page.goto(state['ui_url'] + base + '&trace=' + trace)
    evidence.settle(page)
    shared_url = page.url
    assert 'env=staging' in shared_url
    assert recorded[-1]['scope']['environmentId'] == staging
    context.close()
    context = browser.new_context()
    context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},{json.dumps(auth)});')
    context.route('**/loco.observability.v1.ObservabilityProxyService/*Trace*',handle)
    page = context.new_page()
    page.goto(shared_url)
    evidence.settle(page)
    assert recorded[-1]['scope']['environmentId'] == staging
    context.close()
    raw_service = 'resource-' + workspace_id
    spans[0]['serviceName'] = raw_service
    spans[0]['resourceAttributes']['service.name'] = raw_service
    context = browser.new_context(viewport=evidence.NARROW)
    context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},{json.dumps(auth)});')
    context.route('**/loco.observability.v1.ObservabilityProxyService/*Trace*',handle)
    page = context.new_page()
    page.goto(state['ui_url'] + base + '&trace=' + trace + '&span=' + spans[0]['spanId'])
    evidence.settle(page)
    page.get_by_role('dialog', name=root_name).get_by_role('region', name='Span attributes').get_by_text(raw_service, exact=True).first.wait_for()
    assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth')
    context.close()
    mode[0] = 'many'
    context = browser.new_context(viewport=evidence.DESKTOP)
    context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},{json.dumps(auth)});')
    context.route('**/loco.observability.v1.ObservabilityProxyService/*Trace*',handle)
    page = context.new_page()
    page.goto(state['ui_url'] + base + '&trace=' + trace)
    evidence.settle(page)
    flame = page.get_by_role('dialog', name=root_name).get_by_role('region', name='Flame graph')
    expect(flame.get_by_role('button')).to_have_count(114)
    assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth')
    context.close()
    mode[0] = 'normal'
    context = browser.new_context(viewport=evidence.DESKTOP)
    context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},{json.dumps(auth)});')
    context.route('**/loco.observability.v1.ObservabilityProxyService/*Trace*', handle)
    page = context.new_page()
    search = page.get_by_role('combobox', name='Search and filter')
    suggestions = page.get_by_role('listbox', name='Suggestions')
    recent = suggestions.get_by_role('option').filter(has_text='checkout')
    page.goto(state['ui_url'] + base + '&q=checkout')
    evidence.settle(page)
    page.wait_for_timeout(2500)
    page.goto(state['ui_url'] + base + '&q=quick')
    page.get_by_role('button', name='Clear', exact=True).click()
    page.wait_for_timeout(2500)
    search.click()
    expect(recent).to_have_count(1)
    expect(suggestions.get_by_role('option').filter(has_text='quick')).to_have_count(0)
    page.goto(state['ui_url'] + base.replace('view=traces', 'view=logs'))
    evidence.settle(page)
    search.click()
    expect(suggestions.get_by_role('option').filter(has_text='checkout')).to_have_count(0)
    page.goto(state['ui_url'] + base)
    evidence.settle(page)
    search.click()
    expect(recent).to_have_count(1)
    page.get_by_role('button', name='Clear recent searches', exact=True).click()
    expect(recent).to_have_count(0)
    page.reload()
    evidence.settle(page)
    search.click()
    expect(recent).to_have_count(0)
    search.press_sequentially('chec')
    expect(suggestions.get_by_role('option', name='Search root span names for “chec”')).to_be_visible()
    expect(page.get_by_role('button', name='Pin search')).to_be_visible()
    search.fill('')
    page.keyboard.press('Escape')
    expect(page.get_by_role('button', name='Pin search')).to_have_count(0)
    page.goto(state['ui_url'] + base + '&q=checkout')
    evidence.settle(page)
    page.wait_for_timeout(2500)
    page.get_by_role('button', name='Pin search').click()
    expect(page.locator('[data-sonner-toast]').filter(has_text='Pinned')).to_be_visible()
    page.goto(state['ui_url'] + base)
    evidence.settle(page)
    search.click()
    expect(recent).to_have_count(1)
    suggestions.get_by_role('button', name='Unpin checkout').click()
    expect(suggestions.get_by_role('button', name='Remove checkout')).to_be_visible()
    suggestions.get_by_role('button', name='Remove checkout').click()
    expect(recent).to_have_count(0)
    context.close()
    browser.close()
assert all((row.get('scope', {}).get('environmentId') for row in recorded))
assert all((not row.get('scope', {}).get('resourceIds') for row in recorded if row.get('traceId')))
print('PASS browser trace list, complete drawer spans, span selection and attributes, trace ID validation, frozen bounds, whole-row trace buttons, resizable trace columns, recent queries per view with per-item removal, pinning and unpinning, typed-text suggestions, and production UI light/dark desktop/390px; trace responses use explicit browser fixtures.')
