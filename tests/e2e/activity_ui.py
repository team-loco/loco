import argparse
import atexit
import contextlib
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
parser.add_argument('--out', default=str(repo / 'tests/e2e/out/activity-ui'))
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location('evidence', repo / '.agents/skills/ui-evidence/scripts/evidence.py')
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref='HEAD', name='after', scenario=str(repo / 'tests/e2e/fixtures/activity/scenario.json')))
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
                context.add_init_script(f'localStorage.setItem({json.dumps(evidence.AUTH_KEY)},JSON.stringify({json.dumps(auth)}));localStorage.setItem({json.dumps(evidence.THEME_KEY)},{json.dumps(theme)});')
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
                        if method == 'ListEvents' and mode['hold_initial']:
                            held.append(route)
                            return
                        if method == 'ListEvents' and ((mode['fail_older'] and body.get('beforeSeq')) or (mode['fail_initial'] and not body.get('beforeSeq'))):
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
                if mode['hold_initial']:
                    panel = page.get_by_role('heading', name=title, exact=True).locator('xpath=ancestor::section[1]')
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
                panel = page.get_by_role('heading', name=title, exact=True).locator('xpath=ancestor::section[1]')
                expect(panel.get_by_role('listitem')).to_have_count(50)
                assert panel.get_by_text('Olivia Owner', exact=True).count() > 0
                if user != 'owner':
                    forbidden = [request for request in recorded if request['method'] in ('GetOrg', 'ListOrgMembers') or (request['method'] == 'ListOrgWorkspaces' and request['body'].get('orgId') == activity['orgId'])]
                    assert not forbidden, forbidden
                    breadcrumb = page.locator('nav[aria-label=breadcrumb]').inner_text()
                    assert 'other-company' not in breadcrumb
                    if user == 'workspace_admin':
                        assert 'shared' in breadcrumb
                        assert page.get_by_role('button', name='shared', exact=True).count() == 1
                        assert page.get_by_role('button', name='Audit log', exact=True).count() == 0
                        for tab in ('org', 'audit'):
                            page.goto(origin + base + '/settings?tab=' + tab)
                            expect(page.get_by_role('list', name='Activity')).to_be_visible()
                        panel = page.get_by_role('heading', name=title, exact=True).locator('xpath=ancestor::section[1]')
                    else:
                        assert not any(request['method'] == 'ListEnvironments' for request in recorded)
                    assert not [request for request in recorded if request['status'] >= 400 and request['method'] != 'ListResourceEvents' and not request.get('controlledFailure')], recorded
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
                    filtered = [request['body'] for request in recorded if request['method'] == 'ListEvents' and request['body'].get('types')]
                    assert filtered and all('workspace.created' in request['types'] for request in filtered)
                if user == 'owner' and theme == 'light' and not narrow:
                    page.goto(origin + base + '/resource/' + activity['resourceId'])
                    page.get_by_role('heading', name='Activity', exact=True).wait_for()
                    page.get_by_title('Environment', exact=True).click()
                    page.get_by_role('menuitem', name='staging', exact=False).click()
                    page.keyboard.press('Escape')
                    expect(page.get_by_title('Environment', exact=True)).to_contain_text('staging')
                    assert 'env=staging' in page.url
                    page.get_by_title('Switch workspace', exact=True).click()
                    page.get_by_role('menuitem', name='sibling', exact=False).click()
                    page.keyboard.press('Escape')
                    page.wait_for_url('**/wks/' + activity['siblingId'] + '**')
                    assert not [request for request in recorded if request['status'] >= 400 and request['method'] != 'ListResourceEvents'], recorded
                assert not errors, errors
                results.append({'user': user, 'scene': scene, 'theme': theme, 'narrow': narrow, 'requests': recorded, 'errors': errors})
                context.close()
    browser.close()
(out / 'results.json').write_text(json.dumps({'access': access, 'browser': results}, indent=2))
print(f'{phase}: scoped Activity API access and {len(results)} production-browser cases passed', flush=True)
