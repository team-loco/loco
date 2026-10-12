import json
from pathlib import Path
import sys


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


if __name__ == '__main__':
    run_pages = json.loads(Path(sys.argv[1]).read_text())
    check_pages = json.loads(Path(sys.argv[2]).read_text())
    for row in current_checks(run_pages, check_pages, sys.argv[3]):
        print('\t'.join(row))
