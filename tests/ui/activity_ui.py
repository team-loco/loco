import argparse
import atexit
import contextlib
import datetime
import http.server
import importlib.util
import io
import json
import os
import sys
import threading
import urllib.parse
from pathlib import Path

from playwright.sync_api import expect, sync_playwright
from activity_fixture import prepare_activity, verify_activity_access

sys.dont_write_bytecode = True
repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description='Exercise scoped audit Activity with a throwaway API and database')
parser.add_argument('--state')
parser.add_argument('--ui-root')
parser.add_argument('--keep', action='store_true')
parser.add_argument('--out', default=str(repo / 'tests/ui/out/activity-ui'))
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location('evidence', repo / '.agents/skills/ui-evidence/scripts/evidence.py')
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref='HEAD', name='after', scenario=str(repo / 'tests/ui/fixtures/activity/scenario.json')))
    args.state = output.getvalue().strip().splitlines()[-1]
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
activity = prepare_activity(evidence, state)
state['activity'] = activity
evidence.write_json(Path(args.state), state)
access = verify_activity_access(evidence, state, activity)
origin = state['ui_url']
if args.ui_root:
    ui_root = Path(args.ui_root).resolve()

    class Static(http.server.SimpleHTTPRequestHandler):
        def __init__(self, *values, **options):
            super().__init__(*values, directory=str(ui_root), **options)

        def log_message(self, *values):
            pass

        def do_GET(self):
            if not (ui_root / self.path.split('?')[0].lstrip('/')).is_file():
                self.path = '/index.html'
            super().do_GET()

    server = http.server.ThreadingHTTPServer((evidence.HOST, evidence.free_port()), Static)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    atexit.register(server.shutdown)
    origin = f'http://{evidence.HOST}:{server.server_port}'
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
(out / 'full').mkdir(exist_ok=True)
base = f"/org/{activity['orgId']}/wks/{activity['workspaceId']}"
phase = 'after'
results = []
def check_scoped_user(browser, theme):
    """A member whose only grant is read on one resource sees it on every page without a refused request."""
    rid = activity['resourceId']
    owner = state['users']['owner']['token']
    environments = evidence.rpc(state['api_url'], owner, 'loco.environment.v1.EnvironmentService/ListEnvironments', {'workspaceId': activity['workspaceId']})['environments']
    production = next(e['id'] for e in environments if e['type'] == 'ENVIRONMENT_TYPE_PRODUCTION')
    minute_ago = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(minutes=1)
    stamp = minute_ago.strftime('%Y-%m-%dT%H:%M:%S.000Z')
    telemetry = {
        'QueryLogs': {'entries': [{'timestamp': stamp, 'severity': 'INFO', 'body': 'checkout order placed', 'resourceId': rid, 'resourceAttributes': {'loco.region': 'us-east-1', 'k8s.pod.name': 'checkout-7d9f-x2k4p'}}]},
        'ListTraces': {'totalCount': '1', 'summaries': [{'traceId': '0af7651916cd43dd8448eb211c80319c', 'rootSpanName': 'POST /api/checkout', 'serviceName': 'checkout', 'startTime': stamp, 'durationNanos': '12000000', 'status': 'TRACE_STATUS_OK', 'spanCount': '1', 'resourceId': rid}]},
        'QueryMetrics': {'series': [{'resourceId': rid, 'points': [{'timestamp': stamp, 'value': 0.25}]}]},
    }
    back_off = {'seq': '1', 'id': '01a1c8b3-0000-7000-8000-00000000b0ff', 'type': 'resource.degraded', 'orgId': activity['orgId'], 'workspaceId': activity['workspaceId'], 'resourceId': rid, 'environmentId': production, 'createdAt': stamp, 'data': {'reason': 'crash_looping', 'message': 'Back-off restarting failed container', 'placementId': 'checkout-us-east-1', 'region': 'us-east-1'}}
    rollout = {'seq': '2', 'id': '01a1c8b3-0000-7000-8000-00000000d001', 'type': 'deployment.created', 'orgId': activity['orgId'], 'workspaceId': activity['workspaceId'], 'resourceId': rid, 'createdAt': stamp, 'data': {'resourceId': rid, 'environmentId': production}}
    other_rollout = dict(rollout, id='01a1c8b3-0000-7000-8000-00000000d002', type='resource.scaled', data={'environmentId': '01a1c8b3-0000-7000-8000-0000000000ff', 'replicas': 3})
    down = {'environmentId': production, 'environmentName': 'production', 'status': 'RESOURCE_STATUS_UNAVAILABLE', 'regions': [{'region': 'us-east-1', 'status': 'RESOURCE_STATUS_UNAVAILABLE', 'desiredReplicas': 1, 'message': 'Back-off restarting failed container'}]}
    pages = [
        ('dashboard', base, lambda page: expect(page.get_by_role('list', name='Your resources').get_by_role('link', name='checkout', exact=True)).to_be_visible()),
        ('resources', base + '/resources', lambda page: expect(page.get_by_role('link', name='checkout', exact=True).first).to_be_visible()),
        ('resource', base + '/resource/' + rid, lambda page: expect(page.get_by_label('Last log lines in us-east-1')).to_contain_text('checkout order placed')),
        ('logs', base + '/observability?view=logs', lambda page: expect(page.get_by_text('checkout order placed').first).to_be_visible()),
        ('metrics', base + '/observability?view=metrics', lambda page: expect(page.get_by_role('heading', name='Metrics')).to_be_visible()),
        ('traces', base + '/observability?view=traces', lambda page: expect(page.get_by_text('POST /api/checkout').first).to_be_visible()),
        ('events', base + '/observability?view=events', lambda page: (expect(page.get_by_text('Back-off restarting failed container').first).to_be_visible(), expect(page.get_by_text('Rollout started', exact=True)).to_be_visible(), expect(page.get_by_text('Scaled', exact=True)).to_have_count(0))),
        ('settings', base + '/settings?scope=workspace', lambda page: expect(page.get_by_text('You have access to 1 resource in this workspace', exact=True)).to_be_visible()),
        ('team', f"/org/{activity['orgId']}/team", lambda page: expect(page.get_by_role('heading', name='Team')).to_be_visible()),
        ('tokens', '/tokens', lambda page: expect(page.get_by_role('heading', name='Tokens')).to_be_visible()),
        ('sibling', f"/org/{activity['orgId']}/wks/{activity['siblingId']}", lambda page: expect(page.get_by_text('You have access to 0 resources in this workspace', exact=True)).to_be_visible()),
    ]
    checked = []
    for scene, path, visible in pages:
        context = browser.new_context(viewport={'width': 1440, 'height': 1000}, color_scheme=theme)
        auth = {'idToken': state['users']['scoped']['token'], 'refreshToken': None, 'expiresAt': evidence.SESSION_EXPIRY_MS}
        context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.MODE_KEY)},{json.dumps(theme)});')
        recorded = []

        def route_request(route):
            url = urllib.parse.urlparse(route.request.url)
            if url.path == '/runtime-config.js':
                route.fulfill(content_type='application/javascript', body='window.locoConfig=' + json.dumps({'apiURL': origin}) + ';')
                return
            if url.path == '/dex/.well-known/openid-configuration':
                route.fulfill(json=evidence.discovery_document(origin + '/dex'))
                return
            if not url.path.startswith('/loco.'):
                route.continue_()
                return
            method = url.path.rsplit('/', 1)[-1]
            body = route.request.post_data_json
            if method in telemetry:
                # The proxy serves a caller without workspace read only for resources it names and reads.
                named = body.get('scope', body).get('resourceIds') or []
                status = 200 if named and set(named) == {rid} and (body.get('scope', body)).get('environmentId') == production else 403
                recorded.append({'method': method, 'body': body, 'status': status})
                if status == 200:
                    route.fulfill(json=telemetry[method])
                else:
                    route.fulfill(status=403, json={'code': 'permission_denied', 'message': 'permission denied'})
                return
            response = route.fetch(url=state['api_url'] + url.path)
            recorded.append({'method': method, 'body': body, 'status': response.status})
            if method == 'GetConfig':
                config = response.json()
                config['auth']['issuer'] = origin + '/dex'
                route.fulfill(response=response, json=config)
            elif method == 'ListEvents' and scene in ('resource', 'events') and body.get('environmentId') and response.status == 200:
                # The cluster reports crash loops as runtime events; this resource never ran.
                listed = response.json()
                listed['events'] = [back_off, *listed.get('events', [])]
                route.fulfill(response=response, json=listed)
            elif method == 'ListEvents' and scene == 'events' and not body.get('environmentId') and response.status == 200:
                # Deploys and scaling carry their environment only in their data.
                listed = response.json()
                listed['events'] = [rollout, other_rollout, *listed.get('events', [])]
                route.fulfill(response=response, json=listed)
            elif method == 'GetResource' and scene == 'resource' and response.status == 200:
                resource = response.json()
                resource['resource']['environmentStatuses'] = [down]
                route.fulfill(response=response, json=resource)
            else:
                route.fulfill(response=response)

        context.route('**/*', route_request)
        page = context.new_page()
        errors = []
        page.on('console', lambda message: message.type == 'error' and errors.append(message.text))
        page.on('pageerror', lambda error: errors.append(str(error)))
        page.goto(origin + path)
        visible(page)
        evidence.settle(page)
        refused = [request for request in recorded if request['status'] >= 400]
        assert not refused, (scene, refused)
        assert not errors, (scene, errors)
        if scene in ('logs', 'metrics', 'traces', 'resource'):
            queries = [request for request in recorded if request['method'] in telemetry]
            assert queries and all(request['status'] == 200 for request in queries), (scene, queries)
        if scene == 'events':
            assert any(request['method'] == 'ListEvents' and request['body'].get('resourceId') == rid and request['status'] == 200 for request in recorded), recorded
        if scene == 'dashboard':
            assert 'shared' in page.locator('nav[aria-label=breadcrumb]').inner_text()
            # Environment-wide traffic needs workspace read; the scoped view shows none.
            assert not [request for request in recorded if request['method'] == 'QueryMetrics'], recorded
        page.screenshot(path=out / f'scoped-{scene}-{phase}-{theme}.png', full_page=True, animations='disabled')
        checked.append({'scene': scene, 'theme': theme, 'requests': recorded})
        context.close()
    return checked


with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    for theme in ('light', 'dark'):
        for narrow in (False, True):
            suffix = theme + ('-narrow' if narrow else '')
            for user, scene, path, title in [
                ('owner', 'org-audit', base + '/settings?tab=audit', 'Audit log'),
                ('member', 'org-member', base + '/settings?tab=org', ''),
                ('workspace_admin', 'workspace', base + '/settings?tab=ws', 'Activity'),
                ('resource_reader', 'resource', base + '/resource/' + activity['resourceId'], 'Activity'),
            ]:
                context = browser.new_context(viewport={'width': 390, 'height': 844} if narrow else {'width': 1440, 'height': 1000}, color_scheme=theme)
                auth = {'idToken': state['users'][user]['token'], 'refreshToken': None, 'expiresAt': evidence.SESSION_EXPIRY_MS}
                context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.MODE_KEY)},{json.dumps(theme)});')
                recorded = []
                mode = {'fail_older': False, 'hold_initial': scene == 'resource' and not narrow, 'fail_initial': False}
                held = []

                def route_request(route):
                    url = urllib.parse.urlparse(route.request.url)
                    if url.path == '/runtime-config.js':
                        route.fulfill(content_type='application/javascript', body='window.locoConfig=' + json.dumps({'apiURL': origin}) + ';')
                        return
                    if url.path == '/dex/.well-known/openid-configuration':
                        route.fulfill(json=evidence.discovery_document(origin + '/dex'))
                        return
                    if url.path.startswith('/loco.'):
                        method = url.path.rsplit('/', 1)[-1]
                        assert method.startswith(('Get', 'List', 'WhoAmI', 'Query')), method
                        body = route.request.post_data_json
                        is_activity = method == 'ListEvents' and not body.get('environmentId')
                        if is_activity and mode['hold_initial']:
                            held.append(route)
                            return
                        if is_activity and ((mode['fail_older'] and body.get('beforeSeq')) or (mode['fail_initial'] and not body.get('beforeSeq'))):
                            recorded.append({'method': method, 'body': body, 'status': 503, 'controlledFailure': True})
                            route.fulfill(status=503, json={'code': 'unavailable', 'message': 'Activity is temporarily unavailable'})
                            return
                        response = route.fetch(url=state['api_url'] + url.path)
                        recorded.append({'method': method, 'body': body, 'status': response.status})
                        if method == 'GetConfig':
                            config = response.json()
                            config['auth']['issuer'] = origin + '/dex'
                            route.fulfill(response=response, json=config)
                        else:
                            route.fulfill(response=response)
                        return
                    route.continue_()

                context.route('**/*', route_request)
                page = context.new_page()
                errors = []
                page.on('pageerror', lambda error: errors.append(str(error)))
                page.goto(origin + path)
                marker = 'Audit log' if scene == 'org-audit' else 'Settings' if scene in ('workspace', 'org-member') else 'checkout'
                page.get_by_text(marker, exact=True).first.wait_for()
                page.locator('[role=status][aria-busy]').wait_for(state='detached')
                if scene == 'resource':
                    page.get_by_role('button', name='Events', exact=True).click()
                    page.get_by_role('dialog', name='Events').get_by_role('button', name='Activity', exact=True).click()
                if mode['hold_initial']:
                    panel = page.get_by_role('region', name=title, exact=True)
                    expect(panel.locator('[data-slot=skeleton]')).to_have_count(3)
                    expect(panel.get_by_role('listitem')).to_have_count(0)
                    page.screenshot(path=out / f'initial-loading-{phase}-{suffix}.png', full_page=True, animations='disabled')
                    assert len(held) == 1
                    mode['hold_initial'] = False
                    mode['fail_initial'] = True
                    held[0].fulfill(status=503, json={'code': 'unavailable', 'message': 'Activity is temporarily unavailable'})
                    panel.get_by_role('alert').wait_for()
                    expect(panel.get_by_role('listitem')).to_have_count(0)
                    page.screenshot(path=out / f'initial-error-{phase}-{suffix}.png', full_page=True, animations='disabled')
                    mode['fail_initial'] = False
                    panel.get_by_role('button', name='Retry', exact=True).click()
                evidence.settle(page)
                if user == 'member':
                    assert any(request['method'] == 'GetOrg' and request['status'] == 200 for request in recorded)
                    assert any(request['method'] == 'ListOrgMembers' and request['status'] == 200 for request in recorded)
                    assert not any(request['method'] == 'ListEvents' for request in recorded)
                    expect(page.get_by_role('button', name='Audit log', exact=True)).to_have_count(0)
                    assert not errors, errors
                    results.append({'user': user, 'scene': scene, 'theme': theme, 'narrow': narrow, 'requests': recorded, 'errors': errors})
                    context.close()
                    continue
                panel = page.get_by_role('region', name=title, exact=True)
                expect(panel.get_by_role('listitem')).to_have_count(50)
                heading = panel.evaluate("(section) => { const h = section.querySelector('h2'); return { size: getComputedStyle(h).fontSize, inCard: section.querySelector('ul').closest('.rounded-card').contains(h) }; }")
                assert heading == {'size': '18px', 'inCard': False}, heading
                assert panel.get_by_text('Olivia Owner', exact=True).count() > 0
                if user != 'owner':
                    forbidden = [request for request in recorded if request['method'] in ('GetOrg', 'ListOrgMembers') or (request['method'] == 'ListOrgWorkspaces' and request['body'].get('orgId') == activity['orgId'])]
                    assert not forbidden, forbidden
                    breadcrumb = page.locator('nav[aria-label=breadcrumb]').inner_text()
                    assert 'other-company' not in breadcrumb
                    if user == 'workspace_admin':
                        assert 'shared' in breadcrumb
                        scopes = page.get_by_role('group', name='Settings for', exact=True)
                        expect(scopes.get_by_role('button', name='Workspace', exact=True)).to_have_attribute('aria-pressed', 'true')
                        expect(scopes.get_by_role('button', name='Organization', exact=True)).to_have_count(0)
                        for tab in ('org', 'audit'):
                            page.goto(origin + base + '/settings?tab=' + tab)
                            expect(page.get_by_role('list', name='Activity')).to_be_visible()
                        panel = page.get_by_role('region', name=title, exact=True)
                    else:
                        assert any(request['method'] == 'ListEnvironments' and request['status'] == 200 for request in recorded)
                    assert not [request for request in recorded if request['status'] >= 400 and not request.get('controlledFailure')], recorded
                page.screenshot(path=out / 'full' / f'{scene}-{phase}-{suffix}.png', full_page=True, animations='disabled')
                panel.evaluate('(element) => element.scrollIntoView({block: "start", behavior: "instant"})')
                page.screenshot(path=out / f'{scene}-{phase}-{suffix}.png', clip={'x': 0, 'y': 0, 'width': 390 if narrow else 1440, 'height': 844 if narrow else 700}, animations='disabled')
                if scene == 'resource':
                    panel.get_by_role('button', name='Category: All', exact=True).click()
                    page.get_by_role('menuitemradio', name='Workspaces', exact=True).click()
                    page.keyboard.press('Escape')
                    expect(panel.get_by_text('No matching activity', exact=True)).to_be_visible()
                    panel.get_by_role('button', name='Clear filter', exact=True).click()
                    expect(panel.get_by_role('listitem')).to_have_count(50)
                    if not narrow:
                        loaded_rows = panel.get_by_role('listitem').all_text_contents()
                        mode['fail_older'] = True
                        panel.get_by_role('button', name='Load more', exact=True).click()
                        panel.get_by_role('alert').wait_for()
                        expect(panel.get_by_role('listitem')).to_have_text(loaded_rows)
                        page.screenshot(path=out / f'older-error-{phase}-{suffix}.png', full_page=True, animations='disabled')
                        cursor = [request['body']['beforeSeq'] for request in recorded if request.get('controlledFailure')][-1]
                        mode['fail_older'] = False
                        panel.get_by_role('button', name='Retry', exact=True).click()
                        expect(panel.get_by_role('listitem')).to_have_count(54)
                        assert panel.get_by_role('listitem').all_text_contents()[:50] == loaded_rows
                        retries = [request for request in recorded if request['method'] == 'ListEvents' and request['status'] == 200 and request['body'].get('beforeSeq')]
                        assert retries[-1]['body']['beforeSeq'] == cursor, retries[-1]
                        expect(panel.get_by_role('button', name='Load more', exact=True)).to_have_count(0)
                    filtered = [request['body'] for request in recorded if request['method'] == 'ListEvents' and not request['body'].get('environmentId') and request['body'].get('types')]
                    assert filtered and all('workspace.created' in request['types'] for request in filtered)
                if user == 'owner' and theme == 'light' and not narrow:
                    page.goto(origin + base + '/resource/' + activity['resourceId'])
                    page.get_by_role('button', name='Events', exact=True).wait_for()
                    environment_switcher = page.get_by_title('Switch environment', exact=True)
                    environment_switcher.click()
                    page.get_by_role('menuitem', name='staging', exact=False).click()
                    page.keyboard.press('Escape')
                    expect(environment_switcher).to_contain_text('staging')
                    assert 'env=staging' in page.url
                    page.get_by_title('Switch workspace', exact=True).click()
                    page.get_by_role('menuitem', name='sibling', exact=False).click()
                    page.keyboard.press('Escape')
                    page.wait_for_url('**/wks/' + activity['siblingId'] + '**')
                    assert not [request for request in recorded if request['status'] >= 400], recorded
                assert not errors, errors
                results.append({'user': user, 'scene': scene, 'theme': theme, 'narrow': narrow, 'requests': recorded, 'errors': errors})
                context.close()
    scoped = [case for theme in ('light', 'dark') for case in check_scoped_user(browser, theme)]
    browser.close()
(out / 'results.json').write_text(json.dumps({'access': access, 'browser': results, 'scoped': scoped}, indent=2))
print(f'{phase}: scoped Activity API access and {len(results)} production-browser cases passed', flush=True)
