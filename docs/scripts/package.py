import base64
import hashlib
from html.parser import HTMLParser
import json
from pathlib import Path
import tomllib
from urllib.parse import urlsplit

DOCS = Path(__file__).resolve().parents[1]
CONFIG = DOCS.parent / 'web' / 'sws.toml'
GENERATED_CONFIG = DOCS / '.sws.toml'
SCRIPT_DIRECTIVE = "script-src 'self';"


class InlineScripts(HTMLParser):
    def __init__(self):
        super().__init__()
        self.inline = False
        self.chunks = []
        self.scripts = []

    def handle_starttag(self, tag, attrs):
        if tag == 'script':
            self.inline = 'src' not in dict(attrs)
            self.chunks = []

    def handle_data(self, data):
        if self.inline:
            self.chunks.append(data)

    def handle_endtag(self, tag):
        if tag == 'script' and self.inline:
            self.scripts.append(''.join(self.chunks))
            self.inline = False


def package():
    hashes = set()
    for page in (DOCS / 'site').rglob('*.html'):
        parser = InlineScripts()
        parser.feed(page.read_text())
        for script in parser.scripts:
            digest = hashlib.sha256(script.encode()).digest()
            encoded = base64.b64encode(digest).decode()
            hashes.add(f"'sha256-{encoded}'")
    assert hashes, 'documentation has no inline scripts'
    config = CONFIG.read_text()
    parsed = tomllib.loads(config)
    policies = [entry['headers']['Content-Security-Policy'] for entry in parsed['advanced']['headers'] if 'Content-Security-Policy' in entry['headers']]
    assert len(policies) == 1, 'UI must have one content security policy'
    policy = policies[0]
    assert SCRIPT_DIRECTIVE in policy, 'UI script policy has changed'
    allowed_scripts = ' '.join(sorted(hashes))
    replacement = policy.replace(SCRIPT_DIRECTIVE, f"script-src 'self' {allowed_scripts};")
    config = config.replace(json.dumps(policy), json.dumps(replacement))
    project = tomllib.loads((DOCS / '.generated.toml').read_text())['project']
    hostname = urlsplit(project['site_url']).hostname
    config = config.replace('host = "docs.loco.build"', f'host = {json.dumps(hostname)}')
    GENERATED_CONFIG.write_text(config)


if __name__ == '__main__':
    package()
