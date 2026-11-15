import argparse
import os
from pathlib import Path
import shutil
import subprocess
from xml.sax.saxutils import escape

DOCS = Path(__file__).resolve().parents[1]
HOSTS = {
    'production': 'https://docs.loco.build/',
    'staging': 'https://docs.staging.loco.build/',
}


def export_markdown(site, url, environment):
    pages = sorted((DOCS / 'content').rglob('*.md'))
    index = ['# Loco documentation', '']
    full = []
    locations = []
    for page in pages:
        relative = page.relative_to(DOCS / 'content')
        destination = site / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(page, destination)
        markdown = page.read_text()
        title = next(line.removeprefix('title: ').strip('"') for line in markdown.splitlines() if line.startswith('title: '))
        index.append(f'- [{title}]({url}{relative.as_posix()})')
        full.append(markdown)
        path = relative.with_suffix('').as_posix()
        if path == 'index':
            path = ''
        locations.append(f'<url><loc>{escape(url + path)}</loc></url>')
    (site / 'llms.txt').write_text('\n'.join(index) + '\n')
    (site / 'llms-full.txt').write_text('\n\n'.join(full))
    (site / 'sitemap.xml').write_text('<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">' + ''.join(locations) + '</urlset>')
    rule = 'Disallow: /' if environment == 'staging' else 'Allow: /'
    (site / 'robots.txt').write_text(f'User-agent: *\n{rule}\nSitemap: {url}sitemap.xml\n')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('command', choices=['build', 'serve'])
    parser.add_argument('environment', choices=HOSTS, default='production', nargs='?')
    args = parser.parse_args()
    assets = DOCS / 'public' / 'assets'
    assets.mkdir(parents=True, exist_ok=True)
    for name in ['favicon.svg', 'logo.svg']:
        shutil.copyfile(DOCS.parent / 'web' / 'public' / name, assets / name)
    env = {**os.environ, 'VITE_DOCS_URL': HOSTS[args.environment], 'VITE_DOCS_ENVIRONMENT': args.environment}
    if args.command == 'serve':
        subprocess.run(['bun', '--bun', 'run', 'dev'], cwd=DOCS, env=env, check=True)
        return
    # Router basenames and asset URLs are compiled into each static entry point.
    for base, output in [('/', 'site'), ('/docs/', 'site-mounted')]:
        subprocess.run(['bun', '--bun', 'run', 'build'], cwd=DOCS, env={**env, 'DOCS_BASE_PATH': base}, check=True)
        destination = DOCS / output
        shutil.rmtree(destination, ignore_errors=True)
        shutil.copytree(DOCS / 'build' / 'client', destination)
        if base != '/':
            shutil.copytree(destination / 'docs', destination, dirs_exist_ok=True)
            shutil.rmtree(destination / 'docs')
        shutil.copyfile(destination / '404' / 'index.html', destination / '404.html')
        export_markdown(destination, HOSTS[args.environment], args.environment)


if __name__ == '__main__':
    main()
