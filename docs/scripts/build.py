import argparse
from pathlib import Path
import re
import shutil
import subprocess
import sys

DOCS = Path(__file__).resolve().parents[1]
HOSTS = {
    'production': 'https://docs.loco.build/',
    'staging': 'https://docs.staging.loco.build/',
}
PREVIEW_ADDRESS = '127.0.0.1:8001'


def prepare_brand_assets():
    assets = DOCS / 'content' / 'assets'
    web = DOCS.parent / 'web'
    shutil.copyfile(web / 'public' / 'favicon.svg', assets / 'favicon.svg')
    source = (web / 'src' / 'components' / 'design' / 'logo-strokes.ts').read_text()
    viewbox = re.search(r'LOGO_VIEWBOX = "([^"]+)"', source)
    strokes = re.findall(r'\["([^"]+)", ([\d.]+),', source)
    assert viewbox is not None, 'web logo has no viewbox'
    assert strokes, 'web logo has no strokes'
    svg = f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{viewbox.group(1)}" role="img"><title>Loco</title>'
    svg += '<g fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round">'
    svg += ''.join(f'<path d="{path}" stroke-width="{width}"/>' for path, width in strokes)
    svg += '</g></svg>\n'
    partials = DOCS / 'overrides' / 'partials'
    partials.mkdir(parents=True, exist_ok=True)
    (partials / 'logo.html').write_text(svg)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('command', choices=['build', 'serve'])
    parser.add_argument('environment', choices=HOSTS, default='production', nargs='?')
    args = parser.parse_args()
    prepare_brand_assets()
    url = HOSTS[args.environment]
    config = (DOCS / 'zensical.toml').read_text()
    config = config.replace(HOSTS['production'], url)
    config = config.replace('environment = "production"', f'environment = "{args.environment}"')
    path = DOCS / '.generated.toml'
    path.write_text(config)
    command = [sys.executable, '-m', 'zensical', args.command, '-f', str(path)]
    if args.command == 'build':
        command.extend(['--strict', '--clean'])
    else:
        command.extend(['--dev-addr', PREVIEW_ADDRESS])
    subprocess.run(command, cwd=DOCS, check=True)
    if args.command == 'build':
        rule = 'Disallow: /' if args.environment == 'staging' else 'Allow: /'
        (DOCS / 'site' / 'robots.txt').write_text(f'User-agent: *\n{rule}\nSitemap: {url}sitemap.xml\n')


if __name__ == '__main__':
    main()
