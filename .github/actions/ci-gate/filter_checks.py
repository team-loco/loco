import json
from pathlib import Path
import sys

PASSING = {'success', 'skipped', 'neutral'}


def with_run(run_pages, current):
    runs = [run for page in run_pages for run in page['workflow_runs'] if run['id'] != current['id']]
    return [{'workflow_runs': runs + [current]}]


def current_checks(run_pages, check_pages, self_name):
    runs = [run for page in run_pages for run in page['workflow_runs']]
    suites = {run['check_suite_id'] for run in runs}
    latest_runs = {}
    for run in runs:
        previous = latest_runs.get(run['workflow_id'])
        if previous is None or run['id'] > previous['id']:
            latest_runs[run['workflow_id']] = run
    checks = {}
    for page in check_pages:
        for check in page['check_runs']:
            if check['name'] == self_name or check['check_suite']['id'] in suites:
                continue
            previous = checks.get(check['name'])
            if previous is None or check['id'] > previous['id']:
                checks[check['name']] = check
    result = [(check['name'], check['status'], check['conclusion'] or '') for check in checks.values()]
    for run in latest_runs.values():
        if run['name'] != self_name:
            result.append((run['name'] + ' (workflow)', run['status'], run['conclusion'] or ''))
    return sorted(result)


def verdict(rows):
    if any(status == 'completed' and conclusion not in PASSING for _, status, conclusion in rows):
        return 'completed', 'failure'
    if any(status != 'completed' for _, status, _ in rows):
        return 'in_progress', None
    return 'completed', 'success'


def gate_request(rows, self_name, head_sha, details_url):
    status, conclusion = verdict(rows)
    pending = [name for name, state, _ in rows if state != 'completed']
    failing = [name for name, state, result in rows if state == 'completed' and result not in PASSING]
    body = {
        'state': conclusion or 'pending',
        'context': self_name,
        'target_url': details_url,
        'description': f'{len(failing)} failing, {len(pending)} running, {len(rows)} total',
    }
    return f'POST statuses/{head_sha}', body


if __name__ == '__main__':
    runs_path, checks_path, event_path, self_name, details_url, body_path = sys.argv[1:]
    current = json.loads(Path(event_path).read_text())['workflow_run']
    check_pages = json.loads(Path(checks_path).read_text())
    run_pages = with_run(json.loads(Path(runs_path).read_text()), current)
    rows = current_checks(run_pages, check_pages, self_name)
    for name, state, result in rows:
        print(f'  {name:<34} {result or state}', file=sys.stderr)
    print(f'{self_name}: {" ".join(filter(None, verdict(rows)))}', file=sys.stderr)
    request = gate_request(rows, self_name, current['head_sha'], details_url)
    Path(body_path).write_text(json.dumps(request[1]))
    print(request[0])
