import json
import os
from pathlib import Path
import re
import subprocess

GLOBAL_PATHS = ('mise.toml', 'mise.lock', '.golangci.yml', 'mise-tasks/lint/go')
GLOBAL_PREFIXES = ('.github/actions/setup-tools/', '.github/actions/go-changes/')
WORKFLOWS = ('.github/workflows/lint.yml', '.github/workflows/test.yml')
ROOT_LIBRARIES = ('internal/', 'gen/go/', 'tools/')
PROXY_TEST_INPUTS = ('mise-tasks/test/obs-proxy', 'charts/loco-worker/values.yaml')


def module_dependencies(root):
    files = subprocess.check_output(['git', 'ls-files', '*go.mod'], cwd=root, text=True).splitlines()
    modules = {str(Path(file).parent): set() for file in files}
    for file in files:
        module = str(Path(file).parent)
        for replacement in re.findall(r'=>\s+(\.\.?/\S*)', (root / file).read_text()):
            dependency = os.path.normpath(os.path.join(module, replacement))
            if dependency in modules:
                modules[dependency].add(module)
    return modules


def select_modules(paths, dependencies):
    selected = set()
    ordered = sorted(dependencies, key=len, reverse=True)
    for path in paths:
        if path in GLOBAL_PATHS or path in WORKFLOWS or path.startswith(GLOBAL_PREFIXES):
            return set(dependencies)
        if path in PROXY_TEST_INPUTS:
            selected.add('observability-proxy')
        owner = next((module for module in ordered if module != '.' and path.startswith(module + '/')), None)
        if owner is None:
            if path in ('go.mod', 'go.sum') or path.startswith(('cmd/', *ROOT_LIBRARIES)):
                owner = '.'
            else:
                continue
        affected = {owner}
        if not path.endswith('_test.go') and (owner != '.' or path.startswith(ROOT_LIBRARIES) or path in ('go.mod', 'go.sum')):
            pending = [owner]
            while pending:
                for dependent in dependencies[pending.pop()]:
                    if dependent not in affected:
                        affected.add(dependent)
                        pending.append(dependent)
        selected.update(affected)
    return selected


def changed_files(event, base, head):
    if not base or set(base) == {'0'}:
        return None
    if event == 'pull_request':
        base = subprocess.check_output(['git', 'merge-base', base, head], text=True).strip()
    return subprocess.check_output(['git', 'diff', '--no-renames', '--name-only', '-z', base, head]).decode().split('\0')[:-1]


def main():
    dependencies = module_dependencies(Path.cwd())
    paths = changed_files(os.environ['EVENT'], os.environ.get('BASE', ''), os.environ['HEAD'])
    selected = set(dependencies) if paths is None else select_modules(paths, dependencies)
    entries = json.loads(os.environ['GO_MATRIX'])
    included = [entry for entry in entries if entry['working-directory'] in selected]
    outputs = {
        'run': str(bool(included)).lower(),
        'api': str('api' in selected).lower(),
        'matrix': json.dumps({'include': included}, separators=(',', ':')),
    }
    with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
        for key, value in outputs.items():
            output.write(f'{key}={value}\n')
    print('Selected Go modules:', ', '.join(sorted(selected)) or 'none')


if __name__ == '__main__':
    main()
