import argparse
from datetime import datetime, timedelta, timezone
import json
import os
import re
import signal
from pathlib import Path
import subprocess
import time
from urllib.request import urlopen

from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[2]
ORG = '00000000-0000-7000-8000-000000000001'
WORKSPACE = '00000000-0000-7000-8000-000000000002'
ENVIRONMENT = '00000000-0000-7000-8000-000000000003'
RESOURCE = '00000000-0000-7000-8000-000000000004'
HISTORY_SIZE = 221
PAGE_SIZE = 200


def deployment(index, region='us-east-1'):
    created = datetime(2026, 10, 12, tzinfo=timezone.utc) - timedelta(hours=index)
    return {'id': f'00000000-0000-7000-8000-{index + 100:012d}',
            'resourceId': RESOURCE, 'environmentId': ENVIRONMENT, 'region': region,
            'replicas': 1, 'isActive': index in (0, HISTORY_SIZE - 1),
            'status': 'DEPLOYMENT_PHASE_RUNNING', 'message': f'Release {index}',
            'createdAt': created.isoformat(),
            'spec': {'service': {'build': {'type': 'image', 'image': f'example.test/api:v{index}'},
                                 'cpu': '100m', 'memory': '256Mi', 'minReplicas': 1, 'maxReplicas': 1, 'port': 8080}}}


HISTORY = [deployment(index) for index in range(HISTORY_SIZE)]
HISTORY[-1] = deployment(HISTORY_SIZE - 1, 'eu-west-1')
REGIONS = [{'region': region, 'status': 'RESOURCE_STATUS_HEALTHY', 'readyReplicas': 1,
            'desiredReplicas': 1, 'deploymentId': HISTORY[index]['id'],
            'deploymentStatus': 'DEPLOYMENT_PHASE_RUNNING'}
           for region, index in [('eu-west-1', HISTORY_SIZE - 1), ('us-east-1', 0)]]
RESOURCE_DATA = {'id': RESOURCE, 'workspaceId': WORKSPACE, 'name': 'history-fixture',
                 'type': 'RESOURCE_TYPE_SERVICE', 'spec': {'service': {'regions': {
                     region: {'enabled': True} for region in ('eu-west-1', 'us-east-1')}}},
                 'environmentStatuses': [{'environmentId': ENVIRONMENT, 'environmentName': 'production',
                                          'status': 'RESOURCE_STATUS_HEALTHY', 'regions': REGIONS}]}


def check(browser, url, output, before, theme, width):
    context = browser.new_context(viewport={'width': width, 'height': 1000}, color_scheme=theme)
    context.add_init_script(f"localStorage.setItem('loco:auth:oidc:v1', JSON.stringify({{idToken:'fixture-token',refreshToken:null,expiresAt:Date.now()+3600000}})); localStorage.setItem('loco:theme:v1','{theme}');")
    calls = []
    fail_older = {'enabled': not before}

    def respond(route):
        request = route.request
        if '.well-known/openid-configuration' in request.url:
            result = {'issuer': 'http://fixture.test', 'authorization_endpoint': 'http://fixture.test/auth',
                      'token_endpoint': 'http://fixture.test/token', 'jwks_uri': 'http://fixture.test/keys'}
        elif '/loco.' not in request.url:
            route.continue_()
            return
        else:
            procedure = request.url.rsplit('/', 1)[-1]
            body = request.post_data_json or {}
            calls.append((procedure, body))
            result = {
                'GetConfig': {'auth': {'issuer': 'http://fixture.test', 'clientId': 'fixture', 'scopes': 'openid'}},
                'WhoAmI': {'user': {'id': ORG, 'email': 'fixture@loco.test', 'name': 'Fixture'}},
                'ListUserOrgs': {'orgs': [{'id': ORG, 'name': 'Fixture organization'}]},
                'ListOrgWorkspaces': {'workspaces': [{'id': WORKSPACE, 'orgId': ORG, 'name': 'Fixture workspace'}]},
                'GetScopes': {'scopes': [{'entityType': 'ENTITY_TYPE_ORGANIZATION', 'entityId': ORG, 'scope': 'SCOPE_ADMIN'}]},
                'GetResource': {'resource': RESOURCE_DATA},
                'ListEnvironments': {'environments': [{'id': ENVIRONMENT, 'name': 'production', 'type': 'ENVIRONMENT_TYPE_PRODUCTION'}]},
            }.get(procedure, {})
            if procedure == 'GetDeployment':
                result = {'deployment': next(item for item in HISTORY if item['id'] == body['deploymentId'])}
            if procedure == 'ListDeployments':
                offset = int(body.get('pageToken') or 0)
                if offset and fail_older['enabled']:
                    route.fulfill(status=503, content_type='application/json',
                                  body=json.dumps({'code': 'unavailable', 'message': 'Older history is temporarily unavailable.'}),
                                  headers={'Access-Control-Allow-Origin': '*'})
                    return
                result = {'deployments': HISTORY[offset:offset + PAGE_SIZE],
                          'nextPageToken': str(offset + PAGE_SIZE) if offset + PAGE_SIZE < HISTORY_SIZE else ''}
        route.fulfill(content_type='application/json', body=json.dumps(result),
                      headers={'Access-Control-Allow-Origin': '*'})

    context.route('**/*', respond)
    page = context.new_page()
    page.goto(f'{url}/org/{ORG}/wks/{WORKSPACE}/resource/{RESOURCE}')
    expect(page.get_by_text('history-fixture', exact=True).first).to_be_visible()
    expect(page.get_by_text('200 total' if before else '200 loaded', exact=True)).to_be_visible()
    if before:
        expect(page.get_by_text('No deployments in eu-west-1 yet.', exact=True)).to_be_visible()
    else:
        expect(page.get_by_text('Release 220', exact=True)).to_be_visible()
        assert any(name == 'GetDeployment' and body['deploymentId'] == HISTORY[-1]['id'] for name, body in calls)
    screenshot = output / f'{"before" if before else "after"}-{theme}-{width}.png'
    if before:
        page.screenshot(path=str(screenshot), full_page=True)
    history = page.locator('section').filter(has_text=re.compile(r'Deployments\s*\d+ (?:loaded|total)'))
    history.get_by_role('button', name='100', exact=True).click()
    history.get_by_role('button', name='Next', exact=True).click()
    expect(page.get_by_text('Release 199', exact=True)).to_be_visible()
    if before:
        expect(history.get_by_role('button', name='Next', exact=True)).to_be_disabled()
    else:
        history.get_by_role('button', name='Next', exact=True).click()
        expect(page.get_by_role('alert').filter(has_text='Older history is temporarily unavailable.')).to_be_visible()
        expect(page.get_by_text('Release 199', exact=True)).to_be_visible()
        fail_older['enabled'] = False
        history.get_by_role('button', name='Next', exact=True).click()
        expect(page.get_by_text('Release 200', exact=True)).to_be_visible()
        expect(page.get_by_text('221 total', exact=True)).to_be_visible()
        expect(history.get_by_role('button', name='Next', exact=True)).to_be_disabled()
        assert any(name == 'ListDeployments' and body.get('pageToken') == '200' for name, body in calls)
        page.screenshot(path=str(screenshot), full_page=True)
        history.get_by_role('button', name='Previous', exact=True).focus()
        page.keyboard.press('Enter')
        expect(page.get_by_text('Release 199', exact=True)).to_be_visible()
    context.close()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--url')
    parser.add_argument('--before', action='store_true')
    parser.add_argument('--screenshots', type=Path, default=Path('/tmp/loco-history-screenshots'))
    args = parser.parse_args()
    args.screenshots.mkdir(parents=True, exist_ok=True)
    server = None
    url = args.url or 'http://localhost:5173'
    try:
        if args.url is None:
            server = subprocess.Popen(['bun', 'run', 'dev'], cwd=ROOT / 'web', stdout=subprocess.DEVNULL, start_new_session=True,
                                      env={**os.environ, 'PUBLIC_API_URL': 'http://fixture.test', 'PUBLIC_APP_ENV': 'DEVELOPMENT'})
            for attempt in range(100):
                if server.poll() is not None:
                    raise RuntimeError('The UI dev server could not start')
                try:
                    urlopen(url, timeout=1).close()
                    break
                except OSError:
                    time.sleep(0.1)
            else:
                raise RuntimeError('The UI dev server did not become ready')
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            for theme in ('light', 'dark'):
                for width in (1440, 390):
                    check(browser, url, args.screenshots, args.before, theme, width)
                    print(f'{theme} {width}: passed')
            browser.close()
    finally:
        if server is not None:
            os.killpg(server.pid, signal.SIGTERM)
            server.wait(timeout=10)


if __name__ == '__main__':
    main()
