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
from playwright.sync_api import sync_playwright
sys.dont_write_bytecode = True
repo = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description='Exercise the production trace viewer with fixture trace responses')
parser.add_argument('--state')
parser.add_argument('--out', default=str(repo / 'e2e/out/trace-ui'))
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
    atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = json.loads(Path(args.state).read_text())
orgs = evidence.rpc(state['api_url'], state['users']['owner']['token'], 'loco.org.v1.OrgService/ListUserOrgs', {'userId': state['users']['owner']['id'], 'pageSize': 200})
org_id = orgs['orgs'][0]['id']
workspace_id = state['workspaces']['acme/shared']
base = f'/org/{org_id}/wks/{workspace_id}/observability?view=traces'
trace = '4bf92f3577b34da6a3ce929d0e0e4736'
start = int(time.time()) - 60
rows = [('0001', '', 'frontend-proxy', 'POST /api/checkout', 0, 427.6), ('0002', '0001', 'frontend', 'POST /api/checkout', 1.2, 424.1), ('0003', '0002', 'checkout', 'oteldemo.CheckoutService/PlaceOrder', 5.3, 412.0), ('0004', '0003', 'cart', 'GetCart', 8.4, 24.5), ('0005', '0004', 'valkey', 'GET cart:session', 10.1, 7.8), ('0006', '0003', 'product-catalog', 'GetProduct · OLJCESPC7Z', 34.1, 18.9), ('0007', '0003', 'currency', 'Convert · USD → EUR', 58.7, 12.4), ('0008', '0003', 'shipping', 'GetQuote', 76.3, 86.2), ('0009', '0008', 'quote', 'POST /quote', 79.5, 71.4), ('000a', '0003', 'payment', 'Charge · attempt 1', 166.8, 96.1), ('000b', '000a', 'payment', 'POST /payments/authorize', 168.2, 91.7), ('000c', '0003', 'payment', 'Charge · attempt 2', 275.2, 78.6), ('000d', '0003', 'email', 'SendOrderConfirmation', 357.4, 45.2), ('000e', '0003', 'cart', 'EmptyCart', 405.1, 8.6)]

def span(row):
    sid, parent, service, name, offset, duration = row
    return {'traceId': trace, 'spanId': sid.zfill(16), 'parentSpanId': parent.zfill(16) if parent else '', 'name': name, 'kind': 'Server', 'serviceName': service, 'startTime': f"{time.strftime('%Y-%m-%dT%H:%M:%S', time.gmtime(start))}.{int(offset * 1000000):09d}Z", 'durationNanos': str(int(duration * 1000000)), 'statusCode': 'Error' if sid in ('000a', '000b') else 'Ok', 'statusMessage': 'Payment authorization timed out after 90 ms' if sid == '000b' else '', 'spanAttributes': {'http.request.method': 'POST', 'http.response.status_code': '504' if sid == '000b' else '200', 'server.address': 'payments.internal', 'payment.attempt': '1'} if sid == '000b' else {'rpc.system': 'grpc', 'rpc.service': f'oteldemo.{service.title()}Service'}, 'resourceAttributes': {'service.name': service, 'service.version': '2.1.0', 'deployment.environment.name': 'production', 'cloud.region': 'us-east-1'}, 'scopeName': '@opentelemetry/instrumentation-http', 'scopeVersion': '0.210.0'}
spans = [span(row) for row in rows]
recorded = []
mode = ['normal']

def handle(route):
    body = route.request.post_data_json
    recorded.append(body)
    if mode[0] == 'error':
        route.fulfill(status=503, content_type='application/json', body=json.dumps({'code': 'unavailable', 'message': 'Trace storage is temporarily unavailable'}))
        return
    if mode[0] == 'empty':
        route.fulfill(json={'spans': []})
        return
    if body.get('traceId'):
        route.fulfill(json={'spans': spans[7:] if body.get('cursor') else spans[:7], 'nextCursor': '' if body.get('cursor') else 'next-opaque-span-page'})
    else:
        other = dict(spans[0], traceId='80f198ee56343ba864fe8b2a57d3eff7', name='GET /api/products', durationNanos='18700000')
        route.fulfill(json={'spans': spans + [other]})
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
with sync_playwright() as p:
    browser = p.chromium.launch()
    for theme in ('light', 'dark'):
        for narrow in (False, True):
            context = browser.new_context(viewport=evidence.NARROW if narrow else evidence.DESKTOP, device_scale_factor=2, color_scheme=theme)
            auth = json.dumps({'idToken': state['users']['owner']['token'], 'refreshToken': None, 'expiresAt': evidence.SESSION_EXPIRY_MS})
            context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},{json.dumps(auth)});localStorage.setItem({json.dumps(evidence.THEME_KEY)},{json.dumps(theme)});')
            context.route('**/loco.observability.v1.ObservabilityProxyService/QueryTraces', handle)
            page = context.new_page()
            errors = []
            page.on('pageerror', lambda err: errors.append(str(err)))
            page.goto(state['ui_url'] + base)
            evidence.settle(page)
            page.get_by_role('button', name='POST /api/checkout', exact=False).first.wait_for()
            suffix = f"{theme}{('-narrow' if narrow else '')}"
            page.screenshot(path=out / f'trace-list-after-{suffix}.png', full_page=True)
            page.get_by_role('button', name='POST /api/checkout', exact=False).first.click()
            evidence.settle(page)
            page.get_by_text('This waterfall is partial.', exact=False).wait_for()
            page.evaluate('window.scrollTo(0,0)')
            evidence.settle(page)
            if not narrow:
                page.screenshot(path=out / f'trace-partial-after-{suffix}.png', full_page=True)
            page.get_by_role('button', name='Load more spans').click()
            evidence.settle(page)
            page.get_by_role('button',name='payment POST /payments/authorize',exact=True).click()
            evidence.settle(page)
            page.get_by_role('complementary', name='Span details').wait_for()
            if narrow:
                assert page.get_by_role('complementary', name='Span details').evaluate('(element) => element === document.activeElement')
                assert page.get_by_role('complementary', name='Span details').bounding_box()['y'] >= 0
                page.get_by_role('button',name='Close span details',exact=True).click()
                assert page.get_by_role('button',name='payment POST /payments/authorize',exact=True).evaluate('(element) => element === document.activeElement')
                page.get_by_role('button',name='payment POST /payments/authorize',exact=True).click()
                evidence.settle(page)
            page.evaluate('window.scrollTo(0,0)')
            evidence.settle(page)
            page.screenshot(path=out / f'trace-detail-after-{suffix}.png', full_page=True)
            assert not errors, errors
            if not narrow and theme == 'light':
                range_button = page.get_by_role('button', name='Last hour', exact=False)
                range_button.focus()
                range_button.press('Enter')
                page.get_by_role('menu').wait_for()
                page.get_by_role('menuitem', name='Last 15 minutes', exact=False).click()
                evidence.settle(page)
                assert 'range=15m' in page.url
                page.go_back()
                evidence.settle(page)
                page.get_by_role('button', name='payment POST /payments/authorize', exact=True).focus()
                assert page.get_by_role('button', name='payment POST /payments/authorize', exact=True).evaluate('(element) => element === document.activeElement')
                previous_url = page.url
                previous_request = dict(recorded[-1])
                page.get_by_role('button', name='Refresh', exact=True).click()
                evidence.settle(page)
                assert page.get_by_role('button', name='Load more spans').is_visible()
                assert recorded[-1]['startTime'] != previous_request['startTime']
                page.get_by_text('The selected span has not loaded.',exact=False).wait_for()
                page.go_back()
                evidence.settle(page)
                assert page.url == previous_url
                assert page.get_by_text('Payment authorization timed out after 90 ms', exact=True).is_visible()
                assert page.get_by_role('button', name='Load more spans').count() == 0
                page.get_by_role('button', name='Collapse oteldemo.CheckoutService/PlaceOrder').click()
                assert page.get_by_role('button', name='payment POST /payments/authorize', exact=True).count() == 0
                page.get_by_role('button', name='Expand all').click()
                page.get_by_role('button', name='payment POST /payments/authorize', exact=True).wait_for()
                page.get_by_role('textbox', name='Search span attributes').fill('status_code')
                assert page.get_by_text('http.response.status_code', exact=True).is_visible()
                assert page.get_by_text('http.request.method', exact=True).count() == 0
                page.get_by_role('button', name='Clear attribute search').click()
                page.get_by_role('textbox', name='Trace ID', exact=True).fill('invalid')
                page.get_by_role('button', name='Search', exact=True).click()
                assert page.get_by_text('Enter a 32-character hexadecimal trace ID.').is_visible()
                page.get_by_role('button', name='Clear trace search').click()
                evidence.settle(page)
                assert page.get_by_role('textbox', name='Trace ID', exact=True).input_value() == ''
                page.goto(state['ui_url'] + base + '&trace=' + trace + '&span=ffffffffffffffff')
                evidence.settle(page)
                page.get_by_text('The selected span has not loaded.',exact=False).wait_for()
                page.get_by_role('button',name='Load more spans').click()
                evidence.settle(page)
                page.get_by_text('The selected span is unavailable in this time range.',exact=True).wait_for()
            if not narrow:
                mode[0] = 'empty'
                page.get_by_role('button', name='Refresh', exact=True).click()
                evidence.settle(page)
                page.get_by_text('No traces in this time range', exact=True).wait_for()
                page.screenshot(path=out / f'trace-empty-after-{suffix}.png', full_page=True)
                mode[0] = 'error'
                page.get_by_role('button', name='Refresh', exact=True).click()
                page.get_by_text('Trace storage is temporarily unavailable', exact=False).wait_for(timeout=20000)
                evidence.settle(page)
                page.screenshot(path=out / f'trace-error-after-{suffix}.png', full_page=True)
                page.get_by_role('button', name='Retry', exact=True).wait_for()
                mode[0] = 'normal'
            if not narrow:
                previous_count = len(recorded)
                page.goto(state['ui_url'] + base + '&r=not-a-resource')
                evidence.settle(page)
                page.get_by_text('The selected resources are unavailable.', exact=False).wait_for()
                assert len(recorded) == previous_count
                page.get_by_role('button', name='Show all resources').click()
                evidence.settle(page)
                page.get_by_role('button', name='POST /api/checkout', exact=False).first.wait_for()
            context.close()
    environments = evidence.rpc(state['api_url'], state['users']['owner']['token'], 'loco.environment.v1.EnvironmentService/ListEnvironments', {'workspaceId': workspace_id})['environments']
    staging = next((environment['id'] for environment in environments if environment['name'] == 'staging'), None)
    if staging is None:
        staging = evidence.rpc(state['api_url'], state['users']['owner']['token'], 'loco.environment.v1.EnvironmentService/CreateEnvironment', {'workspaceId':workspace_id,'name':'staging','type':'ENVIRONMENT_TYPE_STAGING'})['environmentId']
    context = browser.new_context()
    auth = json.dumps({'idToken':state['users']['owner']['token'],'refreshToken':None,'expiresAt':evidence.SESSION_EXPIRY_MS})
    context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},{json.dumps(auth)});localStorage.setItem({json.dumps("loco:env:v1:"+workspace_id)},"staging");')
    context.route('**/loco.observability.v1.ObservabilityProxyService/QueryTraces',handle)
    page = context.new_page()
    page.goto(state['ui_url'] + base + '&trace=' + trace)
    evidence.settle(page)
    shared_url = page.url
    assert 'env=' + staging in shared_url
    assert recorded[-1]['environmentId'] == staging
    context.close()
    context = browser.new_context()
    context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},{json.dumps(auth)});')
    context.route('**/loco.observability.v1.ObservabilityProxyService/QueryTraces',handle)
    page = context.new_page()
    page.goto(shared_url)
    evidence.settle(page)
    assert recorded[-1]['environmentId'] == staging
    context.close()
    browser.close()
assert all((row.get('environmentId') for row in recorded))
assert all((not row.get('resourceIds') for row in recorded if row.get('traceId')))
for index, row in enumerate(recorded):
    if row.get('cursor'):
        previous = recorded[index - 1]
        assert row['startTime'] == previous['startTime'] and row['endTime'] == previous['endTime']
print('PASS browser trace search, partial pagination, frozen bounds, hierarchy collapse, attribute search, and production UI light/dark desktop/390px; trace responses use explicit browser fixtures.')
