# /// script
# requires-python = ">=3.15,<3.16"
# dependencies = ["playwright==1.65.0"]
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
# A run nobody has used for this long is a leak: its agent died or never ran `down`.
LEASE_S = 2 * 60 * 60
OPENBAO_PROJECT = LABEL + "-openbao-"
DB_USER = "evidence"
DB_PASSWORD = "evidence"
DB_NAME = "evidence"
SESSION_PREFIX = "loco_s_"
MODE_KEY = "loco:mode:v1"
COLOR_KEY = "loco:color:v1"
AUTH_KEY = "loco:auth:oidc:v1"
THEMES = ("light", "dark")
DESKTOP = {"width": 1280, "height": 800}
NARROW = {"width": 390, "height": 844}
SCALE = 2
SESSION_TTL = "1 day"
SESSION_EXPIRY_MS = 4102444800000
READY_TIMEOUT_S = 120
POSTGRES_READY_TIMEOUT_S = 300
STEP_TIMEOUT_MS = 15000
SETTLE_TIMEOUT_MS = 10000
LOG_TAIL_LINES = 40
DISCOVERY_PATH = "/dex/.well-known/openid-configuration"
USER_SCOPES = ("read", "write", "admin")
RUNTIME_CONFIG_PATH = "/runtime-config.js"
# Set by `mise run test:ui` to the OpenBao every suite shares, after it built
# api/bin/loco-api and web/dist in this checkout.
OPENBAO_ENV = "UI_EVIDENCE_OPENBAO"

RPC_CREATE_ORG = "loco.org.v1.OrgService/CreateOrg"
RPC_CREATE_WORKSPACE = "loco.workspace.v1.WorkspaceService/CreateWorkspace"
RPC_CREATE_MEMBER = "loco.workspace.v1.WorkspaceService/CreateMember"
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
    run(["mise", "exec", "--", "go", "run", "./tools/pullimage", image], cwd=worktree, env=env)
    try:
        compose(worktree, project, compose_env, "up", "-d", "--wait", "--pull", "never", OPENBAO_SERVICE)
        compose(worktree, project, compose_env, "run", "--rm", "--pull", "never", OPENBAO_SEED_SERVICE)
        published = compose(worktree, project, compose_env, "port", OPENBAO_SERVICE, OPENBAO_CONTAINER_PORT, capture=True)
        return f"http://{HOST}:{published.strip().rsplit(':', 1)[1]}"
    except EvidenceError:
        try:
            for arguments in (("logs", "--no-color", OPENBAO_SERVICE), ("ps", "-a", "--format", "json")):
                try:
                    log(compose(worktree, project, compose_env, *arguments, capture=True))
                except EvidenceError as error:
                    log(str(error))
            try:
                containers = compose(worktree, project, compose_env, "ps", "-a", "-q", OPENBAO_SERVICE, capture=True).split()
                if containers:
                    log(run(["docker", "inspect", "--format", "{{json .State}} restart_count={{.RestartCount}}", *containers], capture=True))
            except EvidenceError as error:
                log(str(error))
        finally:
            stop_compose(project)
        raise


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
        if not shot.get("path", "").startswith("/"):
            raise EvidenceError(f"shot {name} needs a path starting with /")
        if "user" in shot and (not isinstance(shot["user"], str) or not shot["user"].strip()):
            raise EvidenceError(f"shot {name}: user must name a scenario user")
        if "ready_selector" in shot:
            selector = shot["ready_selector"]
            if not isinstance(selector, str) or not selector.strip():
                raise EvidenceError(f"shot {name}: ready_selector must be a non-empty selector")
        if "hold_requests" in shot:
            patterns = shot["hold_requests"]
            if not isinstance(patterns, list) or not patterns or any(
                not isinstance(pattern, str) or not pattern.strip() for pattern in patterns
            ):
                raise EvidenceError(f"shot {name}: hold_requests must be a non-empty list of URL patterns")
            if "ready_selector" not in shot:
                raise EvidenceError(f"shot {name}: hold_requests needs ready_selector")
        if "color" in shot and not isinstance(shot["color"], str):
            raise EvidenceError(f"shot {name}: color must be a colour theme id")
        crop = shot.get("crop", "viewport")
        valid_crop = crop in ("viewport", "full") or (
            isinstance(crop, dict) and (("selector" in crop) != ("clip" in crop))
        )
        if not valid_crop:
            raise EvidenceError(f"shot {name}: crop is viewport, full, {{selector, padding}} or {{clip}}")
        for response in shot.get("respond", []):
            if (not isinstance(response, dict) or not isinstance(response.get("url"), str)
                    or not response["url"].strip() or "json" not in response
                    or not isinstance(response.get("status", 200), int)
                    or set(response) - {"url", "status", "json", "headers"}):
                raise EvidenceError(f"shot {name}: each respond entry is {{url, json, status?, headers?}}")
        for step in shot.get("steps", []):
            if len(step) != 1 or next(iter(step)) not in ("click", "hover", "wait", "type", "press", "drag"):
                raise EvidenceError(
                    f"shot {name}: each step is {{click: selector}}, {{hover: selector}}, {{wait: selector}}, "
                    "{type: {selector, text}}, {press: {selector, key}} or {drag: {selector, from, to}}"
                )
            if "type" in step:
                value = step["type"]
                if (not isinstance(value, dict) or set(value) != {"selector", "text"}
                        or not isinstance(value["selector"], str) or not value["selector"].strip()
                        or not isinstance(value["text"], str)):
                    raise EvidenceError(f"shot {name}: type needs a non-empty selector and string text")
            if "press" in step:
                value = step["press"]
                if (not isinstance(value, dict) or set(value) != {"selector", "key"}
                        or not isinstance(value["selector"], str) or not value["selector"].strip()
                        or not isinstance(value["key"], str) or not value["key"]):
                    raise EvidenceError(f"shot {name}: press needs a non-empty selector and key")
            if "drag" in step:
                value = step["drag"]
                point = lambda v: (isinstance(v, list) and len(v) == 2
                                   and all(isinstance(n, (int, float)) and not isinstance(n, bool) for n in v))
                if (not isinstance(value, dict) or set(value) != {"selector", "from", "to"}
                        or not isinstance(value["selector"], str) or not value["selector"].strip()
                        or not point(value["from"]) or not point(value["to"])):
                    raise EvidenceError(f"shot {name}: drag needs a selector and from/to points as [x, y] fractions of its box")
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


def wait_postgres(container):
    deadline = time.monotonic() + POSTGRES_READY_TIMEOUT_S
    while subprocess.run(
        ["docker", "exec", container, "pg_isready", "-h", HOST, "-U", DB_USER], capture_output=True
    ).returncode != 0:
        if time.monotonic() > deadline:
            logs = subprocess.run(
                ["docker", "logs", "--tail", str(LOG_TAIL_LINES), container], capture_output=True, text=True
            )
            raise EvidenceError(f"postgres did not become ready within {POSTGRES_READY_TIMEOUT_S}s; {logs.stdout}{logs.stderr}")
        time.sleep(0.5)


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
        member_rows = ", ".join(
            f"({sql_literal(users[member]['id'])}, 'member', 'organization', {sql_literal(org_id)})"
            for member in org.get("members", [])
        )
        if member_rows:
            psql(container, f"INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) VALUES {member_rows};")

    notes.append(
        "users, sessions and org memberships are inserted with SQL: no API creates a user without an identity "
        "provider, and org membership comes only from an accepted invitation"
    )

    for grant in scenario.get("grants", []):
        org_name = grant["workspace"].split("/", 1)[0]
        body = {
            "workspaceId": workspaces[grant["workspace"]],
            "userId": users[grant["user"]]["id"],
            "scopes": grant["scopes"],
        }
        rpc(api_url, owners[org_name], RPC_CREATE_MEMBER, body)
    resources = seed_resources(state, scenario, users, workspaces)
    if resources:
        notes.append("synthetic clusters, deployments and placements are inserted with SQL; no worker runs these resources")
    return users, workspaces, resources, notes


def seed_resources(state, scenario, users, workspaces):
    resources = {}
    owners = {org["name"]: users[org["owner"]] for org in scenario.get("orgs", [])}
    for resource in scenario.get("resources", []):
        workspace = workspaces[resource["workspace"]]
        owner = owners[resource["workspace"].split("/", 1)[0]]
        regions = {
            deployment["region"]: {"enabled": True, **{key: deployment["spec"][key]
                for key in ("cpu", "memory", "minReplicas", "maxReplicas")}}
            for deployment in resource["deployments"]
        }
        service = {"regions": regions}
        port = resource["deployments"][0]["spec"].get("port")
        if port is not None:
            service["routing"] = {"port": port}
        created = rpc(state["api_url"], owner["token"], "loco.resource.v1.ResourceService/CreateResource", {
            "workspaceId": workspace, "name": resource["name"], "type": "RESOURCE_TYPE_SERVICE", "spec": {"service": service},
        })
        resource_id = created["resourceId"]
        resources[f"{resource['workspace']}/{resource['name']}"] = resource_id
        for deployment in resource["deployments"]:
            env = json.loads(psql(state["container"],
                "SELECT json_build_object('id', e.id, 'type', e.environment_type, 'org', w.org_id, 'slug', r.slug) "
                "FROM environments e JOIN workspaces w ON w.id=e.workspace_id JOIN resources r ON r.workspace_id=w.id "
                f"WHERE r.id={sql_literal(resource_id)} AND e.name={sql_literal(deployment['environment'])};"))
            cluster_id, deployment_id = str(uuid.uuid4()), str(uuid.uuid4())
            runtime = deployment["spec"]
            app = {
                "type": "SERVICE", "resourceId": resource_id, "slug": env["slug"], "workspaceId": workspace,
                "organizationId": env["org"], "environmentId": env["id"], "environmentName": deployment["environment"],
                "deploymentId": deployment_id, "region": deployment["region"],
                "serviceSpec": {
                    "deployment": {"image": runtime["build"]["image"], "observability": runtime["observability"], **({"port": runtime["port"]} if "port" in runtime else {})},
                    "resources": {"cpu": runtime["cpu"], "memory": runtime["memory"],
                        "replicas": {"min": runtime["minReplicas"], "max": runtime["maxReplicas"]}},
                },
            }
            q = sql_literal
            psql(state["container"], f"""BEGIN;
INSERT INTO clusters(id,name,region,provider,is_active,is_default,health_status,last_heartbeat,tier)
VALUES({q(cluster_id)},{q('evidence-' + cluster_id)},{q(deployment['region'])},'fixture',true,false,'healthy',NOW(),{q(env['type'])});
INSERT INTO deployments(id,resource_id,cluster_id,region,replicas,status,is_active,message,environment_id,spec,spec_version,actor_type,actor_id,method,started_at)
VALUES({q(deployment_id)},{q(resource_id)},{q(cluster_id)},{q(deployment['region'])},{runtime['minReplicas']},{q(deployment['status'])},true,'',{q(env['id'])},{q(json.dumps(runtime))},1,'user',{q(owner['id'])},'web',NOW());
INSERT INTO placements(resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,applied_revision,observed_revision,ready,ready_replicas,status_phase,status_updated_at)
VALUES({q(resource_id)},{q(cluster_id)},{q(deployment['region'])},{q(deployment_id)},{q(env['id'])},{q(json.dumps({'app_spec': app}))},1,1,{str(deployment['phase'] == 'Ready').lower()},{deployment['ready_replicas']},{q(deployment['phase'])},NOW());
COMMIT;""")
    return resources


def repo_root():
    return Path(run(["git", "rev-parse", "--show-toplevel"], capture=True).strip())


def cmd_build(args):
    build(repo_root(), dict(os.environ))


def cmd_openbao(args):
    print(start_openbao(repo_root(), args.project, dict(os.environ)))


def check_ref_worktree(repo, ref):
    commit = run(["git", "rev-parse", f"{ref}^{{commit}}"], cwd=repo, capture=True).strip()
    head = run(["git", "rev-parse", "HEAD"], cwd=repo, capture=True).strip()
    if commit == head:
        dirty = run(["git", "status", "--porcelain", "--untracked-files=all"], cwd=repo, capture=True).strip()
        if dirty:
            raise EvidenceError(f"ref {ref} excludes uncommitted files; commit them before capture:\n{dirty}")


def renew(state):
    state["expires_at"] = time.time() + LEASE_S
    write_json(Path(state["root"]) / "state.json", state)


def pid_alive(pid):
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def reap():
    """Stops runs whose lease expired and Docker leftovers no live run or test run owns."""
    now = time.time()
    for path in BASE_DIR.glob("*/state.json"):
        try:
            state = load_state(path)
        except (OSError, ValueError):
            continue
        if state.get("expires_at", path.stat().st_mtime + LEASE_S) < now:
            log(f"reaping {state.get('id', path.parent.name)}: unused for {LEASE_S // 3600}h")
            stop_state(state)
    listed = subprocess.run(
        ["docker", "ps", "-a", "--format", '{{.Names}}\t{{.Label "com.docker.compose.project"}}'],
        capture_output=True, text=True,
    ).stdout.splitlines()
    for line in listed:
        name, _, project = line.partition("\t")
        owner = project or name
        if not owner.startswith(LABEL + "-"):
            continue
        if owner.startswith(OPENBAO_PROJECT):
            pid = owner.removeprefix(OPENBAO_PROJECT)
            if pid.isdigit() and pid_alive(int(pid)):
                continue
        elif (BASE_DIR / owner.removeprefix(LABEL + "-") / "state.json").exists():
            continue
        log(f"reaping {owner}: no run owns it")
        if project:
            stop_compose(project)
        else:
            subprocess.run(["docker", "rm", "-f", "-v", name], capture_output=True)


def cmd_up(args):
    reap()
    repo = repo_root()
    openbao = os.environ.get(OPENBAO_ENV)
    if not openbao:
        check_ref_worktree(repo, args.ref)
    scenario = json.loads(Path(args.scenario).read_text())
    run_id = f"{args.name}-{secrets.token_hex(4)}"
    root = BASE_DIR / run_id
    root.mkdir(parents=True)
    state_path = root / "state.json"
    state = {"id": run_id, "name": args.name, "ref": args.ref, "root": str(root), "pids": [], "expires_at": time.time() + LEASE_S}
    if openbao:
        worktree = repo
    else:
        worktree = root / "src"
        state["worktree"] = str(worktree)
    write_json(state_path, state)
    try:
        if not openbao:
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
        if not openbao:
            build(worktree, env)
        dist = worktree / "web" / "dist"

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
        wait_postgres(container)

        dev_env = read_api_env((worktree / "dev" / "api.env").read_text())
        if not openbao:
            state["compose"] = container
            write_json(state_path, state)
            openbao = start_openbao(worktree, container, env)
        dev_env["LOCO_SECRETS_TRANSIT_ADDR"] = openbao
        api_log = root / "api.log"
        api_cmd = [str(worktree / "api" / "bin" / "loco-api")]
        state["pids"].append(spawn(api_cmd, worktree, dict(env, **api_env(dev_env, db_port, api_port, ui_port)), api_log))
        ui_log = root / "ui.log"
        ui_cmd = [
            sys.executable, str(Path(__file__).resolve()), "serve-ui",
            "--root", str(dist), "--port", str(ui_port), "--api-url", state["api_url"],
        ]
        state["pids"].append(spawn(ui_cmd, worktree, env, ui_log))
        write_json(state_path, state)
        wait_http(f"{state['api_url']}/health", api_log)
        wait_http(state["ui_url"] + "/", ui_log)

        users, workspaces, resources, notes = seed(state, scenario)
        state.update({"users": users, "workspaces": workspaces, "resources": resources, "notes": notes})
        write_json(state_path, state)
    except BaseException:
        for name in ("api.log", "ui.log"):
            if (root / name).exists():
                log(f"{name}:\n{log_tail(root / name)}")
        stop_state(state)
        raise
    for note in notes:
        log(f"scenario note: {note}")
    print(state_path)


def build(checkout, env):
    run(["mise", "trust", "--quiet", str(checkout / "mise.toml")])
    run(["mise", "exec", "--", "bun", "install", "--frozen-lockfile"], cwd=checkout, env=env)
    run(["mise", "exec", "--", "go", "build", "-o", "./bin/loco-api", "."], cwd=checkout / "api", env=env)
    # The API address reaches the UI through runtime-config.js, as the chart
    # serves it in production, so one build serves every API.
    run(["mise", "exec", "--", "bun", "run", "build"], cwd=checkout / "web", env=env)


def runtime_config(api_url):
    return f"window.locoConfig = {json.dumps({'apiURL': api_url})};\n"


class UIHandler(http.server.SimpleHTTPRequestHandler):
    """Serve web/dist like the UI image: the discovery document and runtime config
    this run's addresses need, then files, then index.html for app routes."""

    def __init__(self, *args, api_url, **kwargs):
        self.api_url = api_url
        super().__init__(*args, **kwargs)

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path == DISCOVERY_PATH:
            host, port = self.server.server_address[:2]
            return self.send_body("application/json", json.dumps(discovery_document(f"http://{host}:{port}/dex")))
        if path == RUNTIME_CONFIG_PATH:
            return self.send_body("text/javascript", runtime_config(self.api_url))
        return super().do_GET()

    def send_body(self, content_type, text):
        body = text.encode()
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def send_head(self):
        path = self.path.split("?", 1)[0]
        target = Path(self.directory) / path.lstrip("/")
        if path == "/" or not target.is_file():
            self.path = "/index.html"
        return super().send_head()

    def log_message(self, format, *args):
        return


class UIServer(http.server.ThreadingHTTPServer):
    request_queue_size = UI_BACKLOG


def cmd_serve_ui(args):
    handler = lambda *a, **kw: UIHandler(*a, directory=args.root, api_url=args.api_url, **kw)
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


def settle(page, *, wait_for_network=True):
    if wait_for_network:
        page.wait_for_load_state("networkidle")
    if not page.evaluate(SETTLE_SCRIPT, SETTLE_TIMEOUT_MS):
        raise EvidenceError("animations did not finish")


def capture(browser, state, shot, theme, narrow, target):
    from playwright.sync_api import TimeoutError as PlaywrightTimeout

    user = state["users"].get(shot["user"]) if "user" in shot else None
    if "user" in shot and user is None:
        raise EvidenceError(f"state has no user {shot['user']!r}")
    viewport = dict(NARROW if narrow else DESKTOP)
    context = browser.new_context(viewport=viewport, device_scale_factor=SCALE, color_scheme=theme)
    script = f"localStorage.setItem({json.dumps(MODE_KEY)}, {json.dumps(theme)});"
    if user is not None:
        stored = json.dumps({"idToken": user["token"], "refreshToken": None, "expiresAt": SESSION_EXPIRY_MS})
        script += f"localStorage.setItem({json.dumps(AUTH_KEY)}, {json.dumps(stored)});"
    if "color" in shot:
        script += f"localStorage.setItem({json.dumps(COLOR_KEY)}, {json.dumps(shot['color'])});"
    context.add_init_script(script)
    def responder(response):
        return lambda route: route.fulfill(
            status=response.get("status", 200), headers=response.get("headers"), json=response["json"])
    for response in shot.get("respond", []):
        context.route(response["url"], responder(response))
    for pattern in shot.get("hold_requests", []):
        context.route(pattern, lambda route: None)
    problems = []
    page = context.new_page()
    page.on("console", lambda m: problems.append(f"console {m.type}: {m.text}") if m.type == "error" else None)
    page.on("requestfailed", lambda r: problems.append(f"request failed: {r.url} {r.failure}"))
    ready_selector = shot.get("ready_selector")
    wait_for_network = ready_selector is None
    steps = shot.get("steps", [])
    if ready_selector is not None:
        steps = [*steps, {"wait": ready_selector}]
    try:
        page.goto(state["ui_url"] + shot["path"], wait_until="load" if wait_for_network else "domcontentloaded")
        settle(page, wait_for_network=wait_for_network)
        for step in steps:
            action, value = next(iter(step.items()))
            selector = value["selector"] if isinstance(value, dict) else value
            locator = page.locator(selector)
            try:
                locator.first.wait_for(state="visible", timeout=STEP_TIMEOUT_MS)
            except PlaywrightTimeout:
                raise EvidenceError(f"{shot['name']}: selector {selector!r} never became visible") from None
            if action == "click":
                locator.first.click()
            elif action == "type":
                locator.first.fill(value["text"])
            elif action == "hover":
                locator.first.hover()
            elif action == "press":
                locator.first.press(value["key"])
            elif action == "drag":
                box = locator.first.bounding_box()
                at = lambda f: (box["x"] + f[0] * box["width"], box["y"] + f[1] * box["height"])
                page.mouse.move(*at(value["from"]))
                page.mouse.down()
                page.mouse.move(*at(value["to"]), steps=8)
                page.mouse.up()
            settle(page, wait_for_network=wait_for_network)
        applied = f"""() => {{
          const root = document.documentElement;
          const scheme = getComputedStyle(root).colorScheme;
          return scheme === "normal"
            ? root.classList.contains("dark") === {json.dumps(theme == 'dark')}
            : scheme === {json.dumps(theme)};
        }}"""
        try:
            page.wait_for_function(applied, timeout=STEP_TIMEOUT_MS)
        except PlaywrightTimeout:
            raise EvidenceError(f"{shot['name']}: the page did not apply the {theme} theme") from None
        settle(page, wait_for_network=wait_for_network)
        crop = shot.get("crop", "viewport")
        if crop == "full" or isinstance(crop, dict) and "selector" in crop:
            viewport["height"] = max(viewport["height"], page.evaluate("document.documentElement.scrollHeight"))
            page.set_viewport_size(viewport)
            page.evaluate("window.scrollTo(0, 0)")
            settle(page, wait_for_network=wait_for_network)
        tmp = target.with_suffix(".partial.png")
        if crop in ("full", "viewport"):
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

    reap()
    state = load_state(args.state)
    renew(state)
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
    worktree = state.get("worktree")
    if worktree and Path(worktree).exists():
        repo = run(["git", "-C", worktree, "rev-parse", "--path-format=absolute", "--git-common-dir"], capture=True)
        run(["git", "--git-dir", repo.strip(), "worktree", "remove", "--force", worktree])
    shutil.rmtree(state["root"], ignore_errors=True)
    log(f"stopped {state['id']}")


def cmd_down(args):
    reap()
    states = [Path(args.state)] if args.state else sorted(BASE_DIR.glob("*/state.json"))
    for path in states:
        if path.exists():
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
    serve.add_argument("--api-url", required=True)
    serve.set_defaults(func=cmd_serve_ui)
    prebuild = sub.add_parser("build", help="build the API and UI in this checkout")
    prebuild.set_defaults(func=cmd_build)
    bao = sub.add_parser("openbao", help=f"start one seeded OpenBao for ${OPENBAO_ENV} and print its address")
    bao.add_argument("--project", required=True)
    bao.set_defaults(func=cmd_openbao)
    args = parser.parse_args()
    try:
        args.func(args)
    except EvidenceError as err:
        log(f"error: {err}")
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
