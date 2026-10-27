# /// script
# requires-python = ">=3.14,<3.15"
# dependencies = ["playwright==1.63.0"]
# ///
import argparse
import base64
import hashlib
import http.server
import json
import os
import re
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

LABEL = "loco-ui-evidence"
HOST = "127.0.0.1"
UI_BACKLOG = 128
BASE_DIR = Path(tempfile.gettempdir()) / LABEL
DB_USER = "evidence"
DB_PASSWORD = "evidence"
DB_NAME = "evidence"
SESSION_PREFIX = "loco_s_"
THEME_KEY = "loco:theme:v1"
AUTH_KEY = "loco:auth:oidc:v1"
THEMES = ("light", "dark")
DESKTOP = {"width": 1280, "height": 800}
NARROW = {"width": 390, "height": 844}
SCALE = 2
SESSION_TTL = "1 day"
SESSION_EXPIRY_MS = 4102444800000
READY_TIMEOUT_S = 120
STEP_TIMEOUT_MS = 15000
SETTLE_TIMEOUT_MS = 10000
LOG_TAIL_LINES = 40
DISCOVERY_PATH = "/dex/.well-known/openid-configuration"
USER_SCOPES = ("read", "write", "admin")

RPC_CREATE_ORG = "loco.org.v1.OrgService/CreateOrg"
RPC_CREATE_WORKSPACE = "loco.workspace.v1.WorkspaceService/CreateWorkspace"
RPC_ADD_ORG_MEMBER = "loco.org.v1.OrgService/AddOrgMember"
RPC_CREATE_MEMBER = "loco.workspace.v1.WorkspaceService/CreateMember"
MISSING_RPC_CODES = ("unimplemented", "not_found")
OPENBAO_SERVICE = "openbao"
OPENBAO_SEED_SERVICE = "openbao-seed"
OPENBAO_CONTAINER_PORT = "8200"


class EvidenceError(Exception):
    pass


class RPCError(EvidenceError):
    def __init__(self, path, code, message):
        super().__init__(f"{path}: {code}: {message}")
        self.code = code


def log(message):
    print(message, file=sys.stderr, flush=True)


def run(cmd, cwd=None, env=None, capture=False):
    log(f"$ {' '.join(cmd)}")
    if capture:
        result = subprocess.run(cmd, cwd=cwd, env=env, text=True, capture_output=True)
    else:
        result = subprocess.run(cmd, cwd=cwd, env=env, text=True, stdout=sys.stderr)
    if result.returncode != 0:
        detail = (result.stderr or result.stdout or "").strip() if capture else ""
        raise EvidenceError(f"command failed ({result.returncode}): {' '.join(cmd)}\n{detail}")
    return result.stdout if capture else ""


def port_in_use(port):
    for host in ("127.0.0.1", "::1"):
        family = socket.AF_INET6 if ":" in host else socket.AF_INET
        with socket.socket(family, socket.SOCK_STREAM) as sock:
            sock.settimeout(0.2)
            if sock.connect_ex((host, port)) == 0:
                return True
    return False


def free_port(taken=()):
    while True:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.bind(("0.0.0.0", 0))
            port = sock.getsockname()[1]
        if port not in taken and not port_in_use(port):
            return port


def session_token():
    raw = base64.urlsafe_b64encode(uuid.uuid4().bytes).rstrip(b"=").decode()
    return SESSION_PREFIX + raw


def token_hash(token):
    return hashlib.sha256(token.encode()).hexdigest()


def discovery_document(issuer):
    return {
        "issuer": issuer,
        "authorization_endpoint": f"{issuer}/auth",
        "token_endpoint": f"{issuer}/token",
        "jwks_uri": f"{issuer}/keys",
        "response_types_supported": ["code"],
        "subject_types_supported": ["public"],
        "id_token_signing_alg_values_supported": ["RS256"],
    }


def compose(worktree, project, env, *args, capture=False):
    files = ["-f", str(worktree / "dev" / "compose.yaml")]
    return run(["docker", "compose", *files, "-p", project, *args], env=env, capture=capture)


def start_openbao(worktree, project, env):
    values = worktree / "charts" / "loco-control-plane" / "values.yaml"
    image = run(["mise", "exec", "--", "yq", ".openbao.image", str(values)], capture=True).strip()
    compose_env = dict(env, OPENBAO_PORT=f"{HOST}:", OPENBAO_IMAGE=image)
    compose(worktree, project, compose_env, "up", "-d", "--wait", OPENBAO_SERVICE)
    compose(worktree, project, compose_env, "run", "--rm", OPENBAO_SEED_SERVICE)
    published = compose(worktree, project, compose_env, "port", OPENBAO_SERVICE, OPENBAO_CONTAINER_PORT, capture=True)
    return f"http://{HOST}:{published.strip().rsplit(':', 1)[1]}"


def stop_compose(project):
    subprocess.run(["docker", "compose", "-p", project, "down", "-v", "--remove-orphans"], capture_output=True)


def compose_postgres_image(compose_text):
    match = re.search(r"^  postgres:\n(?:    .*\n)*?    image: (\S+)", compose_text, re.MULTILINE)
    if match is None:
        raise EvidenceError("dev/compose.yaml has no postgres service image")
    return match.group(1)


def read_api_env(text):
    values = {}
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key] = value.strip("'\"")
    return values


def api_env(dev_env, db_port, api_port, ui_port):
    issuer = f"http://{HOST}:{ui_port}/dex"
    unused = free_port({db_port, api_port, ui_port})
    env = dict(dev_env)
    env.update(
        {
            "APP_PORT": f":{api_port}",
            "DATABASE_URL": f"postgres://{DB_USER}:{DB_PASSWORD}@{HOST}:{db_port}/{DB_NAME}?sslmode=disable",
            "CACHE_TYPE": "in-memory",
            "CACHE_ADDR": "",
            "CORS_ALLOWED_ORIGINS": f"http://{HOST}:{ui_port}",
            "WEB_URL": f"http://{HOST}:{ui_port}",
            "PPROF_ADDR": "",
            "INSTALL_WEBHOOKS": "",
            "LOCO_IMAGE_RETENTION": "0",
            "LOCO_SOURCE_BUCKET_ENDPOINT": f"http://{HOST}:{unused}",
            "LOCO_REGISTRY_URL": f"http://{HOST}:{unused}",
            "AUTH_ISSUERS": json.dumps(
                [
                    {
                        "issuer": issuer,
                        "audience": "loco",
                        "emailVerification": "claim",
                        "web": {"clientId": "loco", "scopes": "openid email profile"},
                    }
                ]
            ),
        }
    )
    env["DATABASE_LISTEN_URL"] = env["DATABASE_URL"]
    return env


def shot_filename(shot, side, theme, narrow):
    suffix = "-narrow" if narrow else ""
    return f"{shot}-{side}-{theme}{suffix}.png"


def parse_filename(name):
    match = re.fullmatch(r"(.+)-(before|after)-(light|dark)(-narrow)?\.png", name)
    if match is None:
        return None
    return match.group(1), match.group(2), match.group(3), match.group(4) is not None


def validate_spec(spec):
    shots = spec.get("shots")
    if not isinstance(shots, list) or not shots:
        raise EvidenceError("spec needs a non-empty shots list")
    names = set()
    for shot in shots:
        name = shot.get("name", "")
        if not re.fullmatch(r"[a-z0-9][a-z0-9-]*", name):
            raise EvidenceError(f"shot name {name!r} must be lowercase letters, digits and dashes")
        if name in names:
            raise EvidenceError(f"shot name {name!r} is used twice")
        names.add(name)
        if not shot.get("user") or not shot.get("path", "").startswith("/"):
            raise EvidenceError(f"shot {name} needs a user and a path starting with /")
        crop = shot.get("crop", "viewport")
        valid_crop = crop in ("viewport", "full") or (
            isinstance(crop, dict) and (("selector" in crop) != ("clip" in crop))
        )
        if not valid_crop:
            raise EvidenceError(f"shot {name}: crop is viewport, full, {{selector, padding}} or {{clip}}")
        for step in shot.get("steps", []):
            if len(step) != 1 or next(iter(step)) not in ("click", "wait"):
                raise EvidenceError(f"shot {name}: each step is {{click: selector}} or {{wait: selector}}")
    return shots


def markdown_table(out, files):
    grouped = {}
    for name in sorted(files):
        parsed = parse_filename(name)
        if parsed is None:
            continue
        shot, side, theme, narrow = parsed
        grouped.setdefault((shot, narrow), {})[(side, theme)] = f"{out}/{name}"
    if not grouped:
        raise EvidenceError("no evidence files to tabulate")
    sections = []
    for (shot, narrow), cells in sorted(grouped.items()):
        title = f"{shot} (narrow)" if narrow else shot
        rows = [f"#### {title}", "", "| | Light | Dark |", "| --- | --- | --- |"]
        for side in ("before", "after"):
            if not any((side, theme) in cells for theme in THEMES):
                continue
            row = [side.capitalize()]
            for theme in THEMES:
                path = cells.get((side, theme))
                row.append(f"![{title}, {side}, {theme}]({path})" if path else "")
            rows.append("| " + " | ".join(row) + " |")
        sections.append("\n".join(rows))
    return "\n\n".join(sections) + "\n"


def write_json(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2) + "\n")


def load_state(path):
    return json.loads(Path(path).read_text())


def spawn(cmd, cwd, env, log_path):
    with open(log_path, "ab") as out:
        proc = subprocess.Popen(
            cmd, cwd=cwd, env=env, stdout=out, stderr=subprocess.STDOUT, start_new_session=True
        )
    return proc.pid


def log_tail(path):
    lines = Path(path).read_text(errors="replace").splitlines()
    return "\n".join(lines[-LOG_TAIL_LINES:])


def wait_http(url, log_path):
    deadline = time.monotonic() + READY_TIMEOUT_S
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2) as response:
                if response.status == 200:
                    return
        except (urllib.error.URLError, ConnectionError, TimeoutError):
            pass
        time.sleep(0.5)
    raise EvidenceError(f"{url} did not answer within {READY_TIMEOUT_S}s; log tail:\n{log_tail(log_path)}")


def psql(container, sql):
    cmd = ["docker", "exec", "-i", container, "psql", "-q", "-v", "ON_ERROR_STOP=1", "-U", DB_USER, "-d", DB_NAME, "-tA"]
    log(f"$ {' '.join(cmd)} <<SQL")
    result = subprocess.run(cmd, input=sql, text=True, capture_output=True)
    if result.returncode != 0:
        raise EvidenceError(f"psql failed: {result.stderr.strip()}")
    return result.stdout.strip()


def sql_literal(value):
    return "'" + str(value).replace("'", "''") + "'"


def rpc(api_url, token, path, body):
    request = urllib.request.Request(
        f"{api_url}/{path}",
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {token}"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.loads(response.read() or b"{}")
    except urllib.error.HTTPError as err:
        payload = err.read()
        try:
            detail = json.loads(payload)
        except json.JSONDecodeError:
            detail = {"code": f"http_{err.code}", "message": payload.decode(errors="replace")}
        raise RPCError(path, detail.get("code", f"http_{err.code}"), detail.get("message", "")) from None


def seed(state, scenario):
    container = state["container"]
    api_url = state["api_url"]
    notes = []
    users = {}
    for key, user in scenario["users"].items():
        user_id = psql(
            container,
            f"INSERT INTO users (email, name) VALUES ({sql_literal(user['email'])}, "
            f"{sql_literal(user.get('name', key))}) RETURNING id;",
        )
        scope_rows = ", ".join(f"({sql_literal(user_id)}, '{s}', 'user', {sql_literal(user_id)})" for s in USER_SCOPES)
        psql(container, f"INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) VALUES {scope_rows};")
        token = session_token()
        psql(
            container,
            "INSERT INTO session_tokens (id, access_token_hash, refresh_token_hash, user_id, "
            "access_expires_at, refresh_expires_at) VALUES "
            f"({sql_literal(uuid.uuid4())}, {sql_literal(token_hash(token))}, "
            f"{sql_literal(token_hash('refresh-' + token))}, {sql_literal(user_id)}, "
            f"NOW() + INTERVAL '{SESSION_TTL}', NOW() + INTERVAL '{SESSION_TTL}');",
        )
        users[key] = {"id": user_id, "email": user["email"], "token": token}
    notes.append("users and sessions are inserted with SQL: no API creates a user without an identity provider")

    workspaces = {}
    owners = {}
    for org in scenario.get("orgs", []):
        owner = users[org["owner"]]["token"]
        owners[org["name"]] = owner
        created = rpc(api_url, owner, RPC_CREATE_ORG, {"name": org["name"]})
        org_id = created["orgId"]
        for workspace in org.get("workspaces", []):
            ws = rpc(api_url, owner, RPC_CREATE_WORKSPACE, {"orgId": org_id, "name": workspace})
            workspaces[f"{org['name']}/{workspace}"] = ws["workspaceId"]
        for member in org.get("members", []):
            body = {"orgId": org_id, "userId": users[member]["id"]}
            try:
                rpc(api_url, owner, RPC_ADD_ORG_MEMBER, body)
            except RPCError as err:
                if err.code not in MISSING_RPC_CODES:
                    raise
                notes.append(f"{RPC_ADD_ORG_MEMBER} does not exist on this ref; {member} is not an org member of {org['name']}")

    for grant in scenario.get("grants", []):
        org_name = grant["workspace"].split("/", 1)[0]
        body = {
            "workspaceId": workspaces[grant["workspace"]],
            "userId": users[grant["user"]]["id"],
            "scopes": grant["scopes"],
        }
        rpc(api_url, owners[org_name], RPC_CREATE_MEMBER, body)
    return users, workspaces, notes


def cmd_up(args):
    repo = Path(run(["git", "rev-parse", "--show-toplevel"], capture=True).strip())
    scenario = json.loads(Path(args.scenario).read_text())
    run_id = f"{args.name}-{secrets.token_hex(4)}"
    root = BASE_DIR / run_id
    root.mkdir(parents=True)
    state_path = root / "state.json"
    worktree = root / "src"
    state = {"id": run_id, "name": args.name, "ref": args.ref, "root": str(root), "worktree": str(worktree), "pids": []}
    write_json(state_path, state)
    try:
        run(["git", "-C", str(repo), "worktree", "add", "--detach", str(worktree), args.ref])
        state["commit"] = run(["git", "-C", str(worktree), "rev-parse", "HEAD"], capture=True).strip()
        db_port = free_port()
        api_port = free_port({db_port})
        ui_port = free_port({db_port, api_port})
        state.update(
            {
                "ports": {"db": db_port, "api": api_port, "ui": ui_port},
                "api_url": f"http://{HOST}:{api_port}",
                "ui_url": f"http://{HOST}:{ui_port}",
            }
        )
        write_json(state_path, state)

        env = dict(os.environ)
        run(["mise", "trust", "--quiet", str(worktree / "mise.toml")])
        run(["mise", "exec", "--", "bun", "install", "--frozen-lockfile"], cwd=worktree, env=env)
        run(["mise", "exec", "--", "go", "build", "-o", "./bin/loco-api", "."], cwd=worktree / "api", env=env)
        web_env = dict(env, PUBLIC_API_URL=state["api_url"])
        run(["mise", "exec", "--", "bun", "run", "build"], cwd=worktree / "web", env=web_env)
        dist = worktree / "web" / "dist"
        write_json(dist / DISCOVERY_PATH.lstrip("/"), discovery_document(f"{state['ui_url']}/dex"))

        image = compose_postgres_image((worktree / "dev" / "compose.yaml").read_text())
        container = f"{LABEL}-{run_id}"
        run(
            [
                "docker", "run", "-d", "--name", container, "--label", f"{LABEL}={run_id}",
                "-e", f"POSTGRES_USER={DB_USER}", "-e", f"POSTGRES_PASSWORD={DB_PASSWORD}",
                "-e", f"POSTGRES_DB={DB_NAME}", "-p", f"{HOST}:{db_port}:5432", image,
            ],
            capture=True,
        )
        state["container"] = container
        write_json(state_path, state)
        deadline = time.monotonic() + READY_TIMEOUT_S
        while subprocess.run(
            ["docker", "exec", container, "pg_isready", "-h", HOST, "-U", DB_USER], capture_output=True
        ).returncode != 0:
            if time.monotonic() > deadline:
                raise EvidenceError("postgres did not become ready")
            time.sleep(0.5)

        state["compose"] = container
        write_json(state_path, state)
        dev_env = read_api_env((worktree / "dev" / "api.env").read_text())
        dev_env["LOCO_SECRETS_TRANSIT_ADDR"] = start_openbao(worktree, container, env)
        api_log = root / "api.log"
        api_cmd = [str(worktree / "api" / "bin" / "loco-api")]
        state["pids"].append(spawn(api_cmd, worktree, dict(env, **api_env(dev_env, db_port, api_port, ui_port)), api_log))
        ui_log = root / "ui.log"
        ui_cmd = [sys.executable, str(Path(__file__).resolve()), "serve-ui", "--root", str(dist), "--port", str(ui_port)]
        state["pids"].append(spawn(ui_cmd, worktree, env, ui_log))
        write_json(state_path, state)
        wait_http(f"{state['api_url']}/health", api_log)
        wait_http(state["ui_url"] + "/", ui_log)

        users, workspaces, notes = seed(state, scenario)
        state.update({"users": users, "workspaces": workspaces, "notes": notes})
        write_json(state_path, state)
    except BaseException:
        log(f"up failed; state kept at {state_path} for `down`")
        raise
    for note in notes:
        log(f"scenario note: {note}")
    print(state_path)


class UIHandler(http.server.SimpleHTTPRequestHandler):
    def send_head(self):
        path = self.path.split("?", 1)[0]
        target = Path(self.directory) / path.lstrip("/")
        if path == DISCOVERY_PATH:
            self.extensions_map = {"": "application/json"}
        elif path == "/" or not target.is_file():
            self.path = "/index.html"
        return super().send_head()

    def log_message(self, format, *args):
        return


class UIServer(http.server.ThreadingHTTPServer):
    request_queue_size = UI_BACKLOG


def cmd_serve_ui(args):
    handler = lambda *a, **kw: UIHandler(*a, directory=args.root, **kw)
    with UIServer((HOST, args.port), handler) as server:
        server.serve_forever()


SETTLE_SCRIPT = """async (timeout) => {
  const finite = () => document.getAnimations().filter((a) => {
    const end = a.effect && a.effect.getComputedTiming().endTime;
    return a.playState !== "finished" && Number.isFinite(end);
  });
  const deadline = performance.now() + timeout;
  while (finite().length > 0) {
    if (performance.now() > deadline) return false;
    await Promise.race([Promise.all(finite().map((a) => a.finished.catch(() => null))),
      new Promise((r) => setTimeout(r, 100))]);
  }
  await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
  return true;
}"""


def settle(page):
    page.wait_for_load_state("networkidle")
    if not page.evaluate(SETTLE_SCRIPT, SETTLE_TIMEOUT_MS):
        raise EvidenceError("animations did not finish")


def capture(browser, state, shot, theme, narrow, target):
    from playwright.sync_api import TimeoutError as PlaywrightTimeout

    user = state["users"].get(shot["user"])
    if user is None:
        raise EvidenceError(f"state has no user {shot['user']!r}")
    viewport = NARROW if narrow else DESKTOP
    context = browser.new_context(viewport=viewport, device_scale_factor=SCALE, color_scheme=theme)
    stored = json.dumps({"idToken": user["token"], "refreshToken": None, "expiresAt": SESSION_EXPIRY_MS})
    context.add_init_script(
        f"localStorage.setItem({json.dumps(AUTH_KEY)}, {json.dumps(stored)});"
        f"localStorage.setItem({json.dumps(THEME_KEY)}, {json.dumps(theme)});"
    )
    problems = []
    page = context.new_page()
    page.on("console", lambda m: problems.append(f"console {m.type}: {m.text}") if m.type == "error" else None)
    page.on("requestfailed", lambda r: problems.append(f"request failed: {r.url} {r.failure}"))
    try:
        page.goto(state["ui_url"] + shot["path"])
        settle(page)
        for step in shot.get("steps", []):
            action, selector = next(iter(step.items()))
            locator = page.locator(selector)
            try:
                locator.first.wait_for(state="visible", timeout=STEP_TIMEOUT_MS)
            except PlaywrightTimeout:
                raise EvidenceError(f"{shot['name']}: selector {selector!r} never became visible") from None
            if action == "click":
                locator.first.click()
            settle(page)
        applied = f"() => document.documentElement.classList.contains('dark') === {json.dumps(theme == 'dark')}"
        try:
            page.wait_for_function(applied, timeout=STEP_TIMEOUT_MS)
        except PlaywrightTimeout:
            raise EvidenceError(f"{shot['name']}: the page did not apply the {theme} theme") from None
        settle(page)
        crop = shot.get("crop", "viewport")
        tmp = target.with_suffix(".partial.png")
        if crop == "full":
            page.screenshot(path=tmp, full_page=True)
        elif crop == "viewport":
            page.screenshot(path=tmp)
        elif "clip" in crop:
            x, y, width, height = crop["clip"]
            page.screenshot(path=tmp, clip={"x": x, "y": y, "width": width, "height": height})
        else:
            locator = page.locator(crop["selector"])
            if locator.count() == 0:
                raise EvidenceError(f"{shot['name']}: crop selector {crop['selector']!r} matches nothing")
            box = locator.first.bounding_box()
            pad = crop.get("padding", 0)
            x, y = max(box["x"] - pad, 0), max(box["y"] - pad, 0)
            width = min(box["x"] + box["width"] + pad, viewport["width"]) - x
            height = min(box["y"] + box["height"] + pad, viewport["height"]) - y
            page.screenshot(path=tmp, clip={"x": x, "y": y, "width": width, "height": height})
        tmp.rename(target)
    except EvidenceError as err:
        failed = target.parent / "failed" / target.name
        failed.parent.mkdir(exist_ok=True)
        page.screenshot(path=failed)
        detail = "; ".join(problems[-5:]) or "no console errors or failed requests"
        raise EvidenceError(f"{err} (page at {page.url}; {detail}; screenshot {failed})") from None
    finally:
        context.close()


def cmd_shoot(args):
    from playwright.sync_api import sync_playwright

    state = load_state(args.state)
    shots = validate_spec(json.loads(Path(args.spec).read_text()))
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    run([sys.executable, "-m", "playwright", "install", "chromium"], capture=True)
    written, failures = [], []
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch()
        for shot in shots:
            for theme in THEMES:
                for narrow in (False, True) if shot.get("narrow") else (False,):
                    target = out / shot_filename(shot["name"], state["name"], theme, narrow)
                    target.unlink(missing_ok=True)
                    try:
                        capture(browser, state, shot, theme, narrow, target)
                        written.append(target)
                    except EvidenceError as err:
                        failures.append(f"{target.name}: {err}")
        browser.close()
    for path in written:
        print(f"{path}\t{path.stat().st_size} bytes")
    for failure in failures:
        log(f"FAILED {failure}")
    if failures:
        raise SystemExit(1)


def cmd_table(args):
    files = [p.name for p in Path(args.out).glob("*.png")]
    sys.stdout.write(markdown_table(args.out.rstrip("/"), files))


def stop_state(state):
    for pid in state.get("pids", []):
        try:
            os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
    if state.get("container"):
        subprocess.run(["docker", "rm", "-f", "-v", state["container"]], capture_output=True)
    if state.get("compose"):
        stop_compose(state["compose"])
    worktree = Path(state["worktree"])
    if worktree.exists():
        repo = run(["git", "-C", str(worktree), "rev-parse", "--path-format=absolute", "--git-common-dir"], capture=True)
        run(["git", "--git-dir", repo.strip(), "worktree", "remove", "--force", str(worktree)])
    shutil.rmtree(state["root"], ignore_errors=True)
    log(f"stopped {state['id']}")


def cmd_down(args):
    states = [Path(args.state)] if args.state else sorted(BASE_DIR.glob("*/state.json"))
    for path in states:
        stop_state(load_state(path))
    if args.all:
        listed = subprocess.run(
            ["docker", "ps", "-aq", "--filter", f"label={LABEL}"], capture_output=True, text=True
        ).stdout.split()
        for container in listed:
            subprocess.run(["docker", "rm", "-f", "-v", container], capture_output=True)
        projects = subprocess.run(
            ["docker", "compose", "ls", "-a", "-q", "--filter", f"name={LABEL}"], capture_output=True, text=True
        ).stdout.split()
        for project in projects:
            stop_compose(project)
        run(["git", "worktree", "prune"])


def main():
    parser = argparse.ArgumentParser(description="Collect before and after screenshots of the web UI")
    sub = parser.add_subparsers(dest="command", required=True)
    up = sub.add_parser("up", help="build a ref and start a throwaway API, database and UI")
    up.add_argument("--ref", required=True)
    up.add_argument("--name", required=True, choices=("before", "after"))
    up.add_argument("--scenario", required=True)
    up.set_defaults(func=cmd_up)
    shoot = sub.add_parser("shoot", help="capture every shot of a spec in light and dark")
    shoot.add_argument("--state", required=True)
    shoot.add_argument("--spec", required=True)
    shoot.add_argument("--out", required=True)
    shoot.set_defaults(func=cmd_shoot)
    table = sub.add_parser("table", help="print the before and after markdown for a PR body")
    table.add_argument("--out", required=True)
    table.set_defaults(func=cmd_table)
    down = sub.add_parser("down", help="stop a run, or every run with --all")
    target = down.add_mutually_exclusive_group(required=True)
    target.add_argument("--state")
    target.add_argument("--all", action="store_true")
    down.set_defaults(func=cmd_down)
    serve = sub.add_parser("serve-ui")
    serve.add_argument("--root", required=True)
    serve.add_argument("--port", required=True, type=int)
    serve.set_defaults(func=cmd_serve_ui)
    args = parser.parse_args()
    try:
        args.func(args)
    except EvidenceError as err:
        log(f"error: {err}")
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
