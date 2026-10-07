import argparse
from pathlib import Path
import subprocess
import sys

DOCS = Path(__file__).resolve().parents[1]
HOSTS = {
    'production': 'https://docs.loco.build/',
    'staging': 'https://docs.staging.loco.build/',
}
PREVIEW_ADDRESS = '127.0.0.1:8001'


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('command', choices=['build', 'serve'])
    parser.add_argument('environment', choices=HOSTS, default='production', nargs='?')
    args = parser.parse_args()
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
