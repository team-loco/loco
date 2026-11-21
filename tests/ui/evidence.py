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
import subprocess
import sys
import time
import tomllib
import urllib.error
import urllib.request
import uuid
from pathlib import Path

LABEL = "loco-ui-evidence"
HOST = "127.0.0.1"
UI_BACKLOG = 128
# The checkout of this script, whose tools/testres owns every run's resources and ports.
TOOLS_ROOT = Path(__file__).resolve().parents[2]
# A run nobody has used for this long is a leak: its agent died or never ran `down`.
LEASE_S = 2 * 60 * 60
DB_USER = "evidence"
DB_PASSWORD = "evidence"
DB_NAME = "evidence"
SESSION_PREFIX = "loco_s_"
MODE_KEY = "loco:mode:v1"
COLOR_KEY = "loco:color:v1"
SESSION_COOKIE = "__Host-loco_session"
REFRESH_COOKIE = "__Host-loco_refresh"
THEMES = ("light", "dark")
DESKTOP = {"width": 1280, "height": 800}
NARROW = {"width": 390, "height": 844}
SCALE = 2
SESSION_TTL = "1 day"
READY_TIMEOUT_S = 120
POSTGRES_READY_TIMEOUT_S = 300
STEP_TIMEOUT_MS = 15000
SETTLE_TIMEOUT_MS = 10000
APP_LOADER = '[data-slot="app-loader"]'
LOG_TAIL_LINES = 40
DISCOVERY_PATH = "/dex/.well-known/openid-configuration"
USER_SCOPES = ("read", "write", "admin")
RUNTIME_CONFIG_PATH = "/runtime-config.js"
# The OpenBao every suite shares; `mise run test:ui` and the ui-evidence skill set it.
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
        raise EvidenceError(
            f"command failed ({result.returncode}): {' '.join(cmd)}\n{detail}"
        )
    return result.stdout if capture else ""


def testres(*args):
    """Runs tools/testres, the registry of test leases every local harness shares."""
    return run(
        ["mise", "exec", "--", "go", "run", "./tools/testres", *map(str, args)],
        cwd=TOOLS_ROOT,
        capture=True,
    ).strip()


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
    files = [
        "-f",
        str(worktree / "dev" / "compose.yaml"),
        "-f",
        str(TOOLS_ROOT / "dev" / "compose.subnet.yaml"),
    ]
    return run(
        ["docker", "compose", *files, "-p", project, *args], env=env, capture=capture
    )


def openbao_env(worktree, env):
    values = worktree / "charts" / "loco-control-plane" / "values.yaml"
    image = run(
        ["mise", "exec", "--", "yq", ".openbao.image", str(values)], capture=True
    ).strip()
    return dict(env, OPENBAO_PORT=f"{HOST}:", OPENBAO_IMAGE=image)


def start_openbao(worktree, project, env):
    compose_env = openbao_env(worktree, env)
    image = compose_env["OPENBAO_IMAGE"]
    run(
        ["mise", "exec", "--", "go", "run", "./tools/pullimage", image],
        cwd=worktree,
        env=env,
    )
    try:
        compose(
            worktree,
            project,
            compose_env,
            "up",
            "-d",
            "--wait",
            "--pull",
            "never",
            OPENBAO_SERVICE,
        )
        compose(
            worktree,
            project,
            compose_env,
            "run",
            "--rm",
            "--pull",
            "never",
            OPENBAO_SEED_SERVICE,
        )
        published = compose(
            worktree,
            project,
            compose_env,
            "port",
            OPENBAO_SERVICE,
            OPENBAO_CONTAINER_PORT,
            capture=True,
        )
        return f"http://{HOST}:{published.strip().rsplit(':', 1)[1]}"
    except EvidenceError:
        for arguments in (
            ("logs", "--no-color", OPENBAO_SERVICE),
            ("ps", "-a", "--format", "json"),
        ):
            try:
                log(compose(worktree, project, compose_env, *arguments, capture=True))
            except EvidenceError as error:
                log(str(error))
        try:
            containers = compose(
                worktree,
                project,
                compose_env,
                "ps",
                "-a",
                "-q",
                OPENBAO_SERVICE,
                capture=True,
            ).split()
            if containers:
                log(
                    run(
                        [
                            "docker",
                            "inspect",
                            "--format",
                            "{{json .State}} restart_count={{.RestartCount}}",
                            *containers,
                        ],
                        capture=True,
                    )
                )
        except EvidenceError as error:
            log(str(error))
        raise


def compose_postgres_image(compose_text):
    match = re.search(
        r"^  postgres:\n(?:    .*\n)*?    image: (\S+)", compose_text, re.MULTILINE
    )
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


def api_env(dev_env, ports, issuer):
    db_port, api_port, ui_port, unused = (
        ports[key] for key in ("db", "api", "ui", "unused")
    )
    env = dict(dev_env)
    env.update(
        {
            "APP_PORT": f":{api_port}",
            "DATABASE_URL": f"postgres://{DB_USER}:{DB_PASSWORD}@{HOST}:{db_port}/{DB_NAME}?sslmode=disable",
            "LOCO_AGENT_HEARTBEAT_INTERVAL": f"{LEASE_S}s",
            "CACHE_TYPE": "in-memory",
            "CACHE_ADDR": "",
            "CORS_ALLOWED_ORIGINS": f"http://{HOST}:{ui_port}",
            "WEB_URL": f"http://{HOST}:{ui_port}",
            "PPROF_ADDR": "",
            "INSTALL_WEBHOOKS": "",
            "LOCO_IMAGE_RETENTION": "0",
            "LOCO_DEFAULT_REGION": "us-ca-1",
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


PLACEHOLDER = re.compile(
    r"\{(org|workspace|resource|environment|now)((?:[:+-][^{}]*)?)\}"
)
NOW_OFFSET = re.compile(r"(?:([+-])(\d+)([smhd]))?(?::(s|ms))?")
UNIT_SECONDS = {"s": 1, "m": 60, "h": 3600, "d": 86400}


def placeholder_ids(state):
    return {
        "org": {name: org["id"] for name, org in state.get("orgs", {}).items()},
        "workspace": state.get("workspaces", {}),
        "resource": state.get("resources", {}),
        "environment": state.get("environments", {}),
    }


def fill_placeholders(value, ids, now):
    """Fills placeholders in every string of value; a string that is one :s or :ms time becomes a number."""
    if isinstance(value, dict):
        return {key: fill_placeholders(v, ids, now) for key, v in value.items()}
    if isinstance(value, list):
        return [fill_placeholders(v, ids, now) for v in value]
    if not isinstance(value, str):
        return value

    def resolve(match):
        kind, rest = match.groups()
        if kind != "now":
            if not rest.startswith(":") or rest[1:] not in ids[kind]:
                raise EvidenceError(
                    f"placeholder {match.group(0)} names no seeded {kind}"
                )
            return ids[kind][rest[1:]]
        offset = NOW_OFFSET.fullmatch(rest)
        if offset is None:
            raise EvidenceError(f"placeholder {match.group(0)} is malformed")
        sign, amount, unit, form = offset.groups()
        at = int(now)
        if amount is not None:
            at += (1 if sign == "+" else -1) * int(amount) * UNIT_SECONDS[unit]
        if form == "s":
            return str(at)
        if form == "ms":
            return str(at * 1000)
        return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(at))

    whole = PLACEHOLDER.fullmatch(value)
    if whole and whole.group(1) == "now" and whole.group(2).endswith((":s", ":ms")):
        return int(resolve(whole))
    return PLACEHOLDER.sub(resolve, value)


def validate_spec(spec):
    shots = spec.get("shots")
    if not isinstance(shots, list) or not shots:
        raise EvidenceError("spec needs a non-empty shots list")
    names = set()
    for shot in shots:
        name = shot.get("name", "")
        if not re.fullmatch(r"[a-z0-9][a-z0-9-]*", name):
            raise EvidenceError(
                f"shot name {name!r} must be lowercase letters, digits and dashes"
            )
        if name in names:
            raise EvidenceError(f"shot name {name!r} is used twice")
        names.add(name)
        if not shot.get("path", "").startswith("/"):
            raise EvidenceError(f"shot {name} needs a path starting with /")
        if "user" in shot and (
            not isinstance(shot["user"], str) or not shot["user"].strip()
        ):
            raise EvidenceError(f"shot {name}: user must name a scenario user")
        selector = shot.get("ready_selector")
        if not isinstance(selector, str) or not selector.strip():
            raise EvidenceError(
                f"shot {name}: ready_selector must name an element shown once the page is ready"
            )
        for field in ("exclude_animations", "permissions"):
            if field in shot and (
                not isinstance(shot[field], list)
                or any(
                    not isinstance(value, str) or not value.strip()
                    for value in shot[field]
                )
            ):
                raise EvidenceError(
                    f"shot {name}: {field} must be a list of non-empty strings"
                )
        if "hold_requests" in shot:
            patterns = shot["hold_requests"]
            if (
                not isinstance(patterns, list)
                or not patterns
                or any(
                    not isinstance(pattern, str) or not pattern.strip()
                    for pattern in patterns
                )
            ):
                raise EvidenceError(
                    f"shot {name}: hold_requests must be a non-empty list of URL patterns"
                )
        themes = shot.get("themes", THEMES)
        if (
            not isinstance(themes, (list, tuple))
            or not themes
            or any(theme not in THEMES for theme in themes)
        ):
            raise EvidenceError(
                f"shot {name}: themes must be a non-empty list of {', '.join(THEMES)}"
            )
        for field in ("before", "signedOut"):
            if field in shot and not isinstance(shot[field], bool):
                raise EvidenceError(f"shot {name}: {field} must be true or false")
        if "color" in shot and not isinstance(shot["color"], str):
            raise EvidenceError(f"shot {name}: color must be a colour theme id")
        crop = shot.get("crop", "viewport")
        valid_crop = crop in ("viewport", "full") or (
            isinstance(crop, dict) and (("selector" in crop) != ("clip" in crop))
        )
        if not valid_crop:
            raise EvidenceError(
                f"shot {name}: crop is viewport, full, {{selector, padding}} or {{clip}}"
            )
        for response in shot.get("respond", []):
            if (
                not isinstance(response, dict)
                or not isinstance(response.get("url"), str)
                or not response["url"].strip()
                or "json" not in response
                or not isinstance(response.get("status", 200), int)
                or not isinstance(response.get("match", {}), dict)
                or set(response) - {"url", "status", "json", "headers", "match"}
            ):
                raise EvidenceError(
                    f"shot {name}: each respond entry is {{url, json, status?, headers?, match?}}"
                )
        for step in shot.get("steps", []):
            if len(step) != 1 or next(iter(step)) not in (
                "click",
                "hover",
                "wait",
                "type",
                "press",
                "drag",
            ):
                raise EvidenceError(
                    f"shot {name}: each step is {{click: selector}}, {{hover: selector}}, {{wait: selector}}, "
                    "{type: {selector, text}}, {press: {selector, key}} or {drag: {selector, from, to}}"
                )
            if "type" in step:
                value = step["type"]
                if (
                    not isinstance(value, dict)
                    or set(value) != {"selector", "text"}
                    or not isinstance(value["selector"], str)
                    or not value["selector"].strip()
                    or not isinstance(value["text"], str)
                ):
                    raise EvidenceError(
                        f"shot {name}: type needs a non-empty selector and string text"
                    )
            if "press" in step:
                value = step["press"]
                if (
                    not isinstance(value, dict)
                    or set(value) != {"selector", "key"}
                    or not isinstance(value["selector"], str)
                    or not value["selector"].strip()
                    or not isinstance(value["key"], str)
                    or not value["key"]
                ):
                    raise EvidenceError(
                        f"shot {name}: press needs a non-empty selector and key"
                    )
            if "drag" in step:
                value = step["drag"]

                def point(v):
                    return (
                        isinstance(v, list)
                        and len(v) == 2
                        and all(
                            isinstance(n, (int, float)) and not isinstance(n, bool)
                            for n in v
                        )
                    )

                if (
                    not isinstance(value, dict)
                    or set(value) != {"selector", "from", "to"}
                    or not isinstance(value["selector"], str)
                    or not value["selector"].strip()
                    or not point(value["from"])
                    or not point(value["to"])
                ):
                    raise EvidenceError(
                        f"shot {name}: drag needs a selector and from/to points as [x, y] fractions of its box"
                    )
    return shots


DEFAULT_ENVIRONMENT = "production"


def connect_methods():
    """Lists the `<package>.<Service>/<Method>` path of every RPC under proto/."""
    methods = set()
    for proto in (TOOLS_ROOT / "proto").rglob("*.proto"):
        text = proto.read_text()
        package = re.search(r"^package ([\w.]+);", text, re.M)
        for service, body in re.findall(
            r"^service (\w+) \{(.*?)^\}", text, re.M | re.S
        ):
            for method in re.findall(r"^\s*rpc (\w+)\(", body, re.M):
                methods.add(f"{package.group(1)}.{service}/{method}")
    return methods


def scenario_ids(scenario):
    """Maps every placeholder key a scenario seeds to a stand-in ID."""
    workspaces = [
        f"{org['name']}/{workspace}"
        for org in scenario.get("orgs", [])
        for workspace in org.get("workspaces", [])
    ]
    return {
        "org": {org["name"]: "id" for org in scenario.get("orgs", [])},
        "workspace": dict.fromkeys(workspaces, "id"),
        "resource": {
            f"{r['workspace']}/{r['name']}": "id" for r in scenario.get("resources", [])
        },
        "environment": {f"{w}/{DEFAULT_ENVIRONMENT}": "id" for w in workspaces},
    }


def check_spec(spec, scenario, methods):
    """Returns the mistakes in a shot spec and its scenario that would fail or mislead a capture."""
    try:
        shots = validate_spec(spec)
    except EvidenceError as err:
        return [str(err)]
    ids, errors = scenario_ids(scenario), []
    for resource in scenario.get("resources", []):
        try:
            fill_placeholders(resource.get("deployments", []), ids, 0)
        except EvidenceError as err:
            errors.append(f"scenario resource {resource['name']}: {err}")
    for index, event in enumerate(scenario.get("events", []), 1):
        if "resource" in event and event["resource"] not in ids["resource"]:
            errors.append(
                f"scenario event {index}: resource {event['resource']!r} is not seeded"
            )
        try:
            fill_placeholders(event, ids, 0)
        except EvidenceError as err:
            errors.append(f"scenario event {index}: {err}")
    for shot in shots:
        name = shot["name"]
        if "user" in shot and shot["user"] not in scenario.get("users", {}):
            errors.append(f"shot {name}: user {shot['user']!r} is not a scenario user")
        if "user" not in shot and not shot.get("signedOut"):
            errors.append(
                f'shot {name}: user is missing; set "signedOut": true for a signed-out page'
            )
        responses = shot.get("respond", [])
        patterns = [("respond", r["url"]) for r in responses]
        patterns += [("hold_requests", p) for p in shot.get("hold_requests", [])]
        for field, pattern in patterns:
            if not pattern.startswith("**/") or pattern[3:] not in methods:
                errors.append(
                    f"shot {name}: {field} {pattern!r} matches no RPC; "
                    "write **/<package>.<Service>/<Method>"
                )
        for index, response in enumerate(responses):
            match = response.get("match", {}).items()
            for later, other in enumerate(responses[index + 1 :], index + 2):
                if (
                    other["url"] == response["url"]
                    and match <= other.get("match", {}).items()
                ):
                    errors.append(
                        f"shot {name}: respond entry {index + 1} for {response['url']} answers "
                        f"every request entry {later} matches; list the narrower entry first"
                    )
        for field in ("path", "respond"):
            try:
                fill_placeholders(shot.get(field, ""), ids, 0)
            except EvidenceError as err:
                errors.append(f"shot {name}: {field}: {err}")
    return errors


SUITE_END = re.compile(r"(\S+) (passed|failed) in \d+s")
ERROR_LINE = re.compile(r"Error|error:|assert|Traceback|FAILED")
FAILURE_TAIL = 20


def failure_section(title, lines):
    errors = [line for line in lines if ERROR_LINE.search(line)][-30:]
    tail = lines[-FAILURE_TAIL:]
    return "\n".join(
        [
            f"## {title}",
            "",
            "```",
            *errors,
            "```",
            "",
            "Last lines:",
            "",
            "```",
            *tail,
            "```",
        ]
    )


def failure_summary(suites_log, shots_log):
    """Builds failure.md: each failing suite or shot spec with its error lines and log tail."""
    sections, chunk = [], []
    for line in (suites_log or "").splitlines():
        end = SUITE_END.fullmatch(line)
        if end is None:
            if not line.startswith("::"):
                chunk.append(line)
            continue
        if end.group(2) == "failed":
            sections.append(failure_section(f"Suite {end.group(1)}", chunk))
        chunk = []
    if shots_log and ERROR_LINE.search(shots_log):
        sections.append(failure_section("Screenshots", shots_log.splitlines()))
    if not sections and (suites_log or shots_log):
        sections.append(failure_section("Run", (suites_log or shots_log).splitlines()))
    return "# Evidence failures\n\n" + "\n\n".join(sections) + "\n"


def cmd_failure_summary(args):
    logs = [Path(args.dir) / name for name in ("suites.log", "shots.log")]
    texts = [
        path.read_text(errors="replace") if path.exists() else None for path in logs
    ]
    (Path(args.dir) / "failure.md").write_text(failure_summary(*texts))


def cmd_check(args):
    scenario_path = args.scenario or (
        TOOLS_ROOT / ".agents/skills/ui-evidence/scenario.example.json"
    )
    errors = check_spec(
        json.loads(Path(args.spec).read_text()),
        json.loads(Path(scenario_path).read_text()),
        connect_methods(),
    )
    for error in errors:
        log(error)
    if errors:
        raise SystemExit(1)


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
        themes = [t for t in THEMES if t in {theme for _, theme in cells}]
        rows = [
            f"#### {title}",
            "",
            "| | " + " | ".join(t.capitalize() for t in themes) + " |",
            "| --- |" + " --- |" * len(themes),
        ]
        for side in ("before", "after"):
            if not any((side, theme) in cells for theme in themes):
                continue
            row = [side.capitalize()]
            for theme in themes:
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
            cmd,
            cwd=cwd,
            env=env,
            stdout=out,
            stderr=subprocess.STDOUT,
            start_new_session=True,
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
        except urllib.error.URLError, ConnectionError, TimeoutError:
            pass
        time.sleep(0.5)
    raise EvidenceError(
        f"{url} did not answer within {READY_TIMEOUT_S}s; log tail:\n{log_tail(log_path)}"
    )


def wait_postgres(container):
    deadline = time.monotonic() + POSTGRES_READY_TIMEOUT_S
    while True:
        logs = subprocess.run(
            ["docker", "logs", container], capture_output=True, text=True
        )
        initialized = "PostgreSQL init process complete" in logs.stdout + logs.stderr
        if initialized and (
            subprocess.run(
                [
                    "docker",
                    "exec",
                    container,
                    "psql",
                    "-q",
                    "-h",
                    HOST,
                    "-U",
                    DB_USER,
                    "-d",
                    DB_NAME,
                    "-c",
                    "select 1",
                ],
                capture_output=True,
            ).returncode
            == 0
        ):
            return
        if time.monotonic() > deadline:
            tail = "\n".join((logs.stdout + logs.stderr).splitlines()[-LOG_TAIL_LINES:])
            raise EvidenceError(
                f"postgres did not become ready within {POSTGRES_READY_TIMEOUT_S}s; {tail}"
            )
        time.sleep(0.5)


def postgres_network(port, image):
    """The `docker run` arguments, from the image on, that serve Postgres on HOST:port."""
    if sys.platform != "linux":
        return ["-p", f"{HOST}:{port}:5432", image]
    # A bridged container adds a host interface, and Chromium aborts every open request when one appears.
    return [
        "--network",
        "host",
        "-e",
        f"PGPORT={port}",
        image,
        "-c",
        f"listen_addresses={HOST}",
    ]


def psql(container, sql):
    cmd = [
        "docker",
        "exec",
        "-i",
        container,
        "psql",
        "-q",
        "-v",
        "ON_ERROR_STOP=1",
        "-U",
        DB_USER,
        "-d",
        DB_NAME,
        "-tA",
    ]
    result = subprocess.run(cmd, input=sql, text=True, capture_output=True)
    if result.returncode != 0:
        raise EvidenceError(
            f"$ {' '.join(cmd)} <<SQL\n"
            f"psql exited {result.returncode}: {result.stderr.strip()} {result.stdout.strip()}"
        )
    return result.stdout.strip()


def sql_literal(value):
    return "'" + str(value).replace("'", "''") + "'"


def rpc(api_url, token, path, body):
    request = urllib.request.Request(
        f"{api_url}/{path}",
        data=json.dumps(body).encode(),
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {token}",
        },
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
            detail = {
                "code": f"http_{err.code}",
                "message": payload.decode(errors="replace"),
            }
        raise RPCError(
            path, detail.get("code", f"http_{err.code}"), detail.get("message", "")
        ) from None


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
        scope_rows = ", ".join(
            f"({sql_literal(user_id)}, '{s}', 'user', {sql_literal(user_id)})"
            for s in USER_SCOPES
        )
        psql(
            container,
            f"INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) VALUES {scope_rows};",
        )
        token = session_token()
        psql(
            container,
            "INSERT INTO identities (user_id, issuer, subject, email, email_verified) "
            f"VALUES ({sql_literal(user_id)}, {sql_literal(state['issuer'])}, "
            f"{sql_literal('evidence-' + key)}, {sql_literal(user['email'])}, true);",
        )
        psql(
            container,
            "INSERT INTO session_tokens (id, access_token_hash, refresh_token_hash, user_id, "
            "access_expires_at, refresh_expires_at, kind) VALUES "
            f"({sql_literal(uuid.uuid4())}, {sql_literal(token_hash(token))}, "
            f"{sql_literal(token_hash('refresh-' + token))}, {sql_literal(user_id)}, "
            f"NOW() + INTERVAL '{SESSION_TTL}', NOW() + INTERVAL '{SESSION_TTL}', 'web');",
        )
        users[key] = {"id": user_id, "email": user["email"], "token": token}

    workspaces = {}
    orgs = {}
    for org in scenario.get("orgs", []):
        owner = users[org["owner"]]["token"]
        created = rpc(api_url, owner, RPC_CREATE_ORG, {"name": org["name"]})
        org_id = created["orgId"]
        orgs[org["name"]] = {"id": org_id, "owner": org["owner"]}
        for workspace in org.get("workspaces", []):
            ws = rpc(
                api_url,
                owner,
                RPC_CREATE_WORKSPACE,
                {"orgId": org_id, "name": workspace},
            )
            workspaces[f"{org['name']}/{workspace}"] = ws["workspaceId"]
        member_rows = ", ".join(
            f"({sql_literal(users[member]['id'])}, 'member', 'organization', {sql_literal(org_id)})"
            for member in org.get("members", [])
        )
        if member_rows:
            psql(
                container,
                f"INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) VALUES {member_rows};",
            )

    notes.append(
        "users, sessions and org memberships are inserted with SQL: no API creates a user without an identity "
        "provider, and org membership comes only from an accepted invitation"
    )
    state.update({"users": users, "workspaces": workspaces, "orgs": orgs})
    state["environments"] = {
        f"{key}/{slug}": environment_id
        for key, workspace_id in workspaces.items()
        for slug, environment_id in json.loads(
            psql(
                container,
                "SELECT COALESCE(json_object_agg(slug, id), '{}') FROM environments "
                f"WHERE workspace_id={sql_literal(workspace_id)};",
            )
        ).items()
    }

    for grant in scenario.get("grants", []):
        org_name = grant["workspace"].split("/", 1)[0]
        body = {
            "workspaceId": workspaces[grant["workspace"]],
            "userId": users[grant["user"]]["id"],
            "scopes": grant["scopes"],
        }
        rpc(api_url, users[orgs[org_name]["owner"]]["token"], RPC_CREATE_MEMBER, body)
    state["resources"] = seed_resources(state, scenario.get("resources", []))
    seed_events(state, scenario.get("events", []))
    if state["resources"]:
        notes.append(
            "synthetic clusters, deployments and placements are inserted with SQL; no worker runs these resources"
        )
    state["notes"] = notes


def seed_cluster(
    state, region, *, name=None, domains=(), health="healthy", default=False
):
    cluster_id = str(uuid.uuid4())
    psql(
        state["container"],
        cluster_row(
            cluster_id,
            region,
            name=name,
            domains=domains,
            health=health,
            default=default,
        ),
    )
    return cluster_id


def cluster_row(
    cluster_id, region, *, name=None, domains=(), health="healthy", default=False
):
    q = sql_literal
    domain_array = "ARRAY[" + ",".join(q(domain) for domain in domains) + "]::text[]"
    return (
        "INSERT INTO clusters(id,name,region,provider,platforms,domains,is_active,is_default,health_status,last_heartbeat) "
        f"VALUES({q(cluster_id)},{q(name or 'evidence-' + cluster_id)},{q(region)},'fixture',ARRAY['linux/amd64'],"
        f"{domain_array},true,{str(default).lower()},{q(health)},NOW());"
    )


def seed_resources(state, resources):
    seeded = {}
    for resource in resources:
        org = resource["workspace"].split("/", 1)[0]
        user = state["orgs"][org]["owner"]
        service = resource.get("service")
        if service is None:
            first = resource["deployments"][0]["spec"]
            service = {
                "regions": {
                    region: {"enabled": True, **target}
                    for deployment in resource["deployments"]
                    for region, target in deployment["spec"]["regions"].items()
                }
            }
            if "port" in first:
                service["routing"] = {"port": first["port"]}
        resource_id = rpc(
            state["api_url"],
            state["users"][user]["token"],
            "loco.resource.v1.ResourceService/CreateResource",
            {
                "workspaceId": state["workspaces"][resource["workspace"]],
                "name": resource["name"],
                "type": "RESOURCE_TYPE_SERVICE",
                "spec": {"service": service},
            },
        )["resourceId"]
        seeded[f"{resource['workspace']}/{resource['name']}"] = resource_id
        seed_deployments(state, resource_id, resource.get("deployments", []), user)
        seed_domains(state, resource_id, resource.get("domains", []))
    return seeded


def seed_events(state, events):
    """Inserts workspace events with their resource and environment; `at` takes time placeholders."""
    q = sql_literal
    rows = []
    for event in fill_placeholders(events, placeholder_ids(state), time.time()):
        workspace = event.get("workspace") or event["resource"].rsplit("/", 1)[0]
        org = state["orgs"][workspace.split("/", 1)[0]]["id"]
        resource = (
            state["resources"][event["resource"]] if "resource" in event else None
        )
        environment = (
            state["environments"][f"{workspace}/{event['environment']}"]
            if "environment" in event
            else None
        )
        rows.append(
            "INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,actor_type,data,created_at) "
            f"VALUES({q(event['type'])},{q(org)},{q(state['workspaces'][workspace])},"
            f"{q(resource) if resource else 'NULL'},{q(environment) if environment else 'NULL'},"
            f"{q(event.get('actor_type', 'agent'))},{q(json.dumps(event.get('data', {})))},"
            f"{q(event['at']) if 'at' in event else 'NOW()'});"
        )
    if rows:
        psql(state["container"], "BEGIN;\n" + "\n".join(rows) + "\nCOMMIT;")


def seed_domains(state, resource_id, domains):
    """Inserts each platform domain; the first in an environment is its primary and makes the resource public."""
    q = sql_literal
    rows, primary = [], set()
    for entry in domains:
        label, platform = entry["domain"].split(".", 1)
        environment = entry["environment"]
        rows.append(
            "INSERT INTO resource_domains(resource_id,environment_id,domain,domain_source,subdomain_label,"
            "platform_domain,is_primary) SELECT r.id,e.id,"
            f"{q(entry['domain'])},'platform_provided',{q(label)},{q(platform)},{str(environment not in primary).lower()} "
            f"FROM resources r JOIN environments e ON e.workspace_id=r.workspace_id "
            f"WHERE r.id={q(resource_id)} AND e.slug={q(environment)};"
        )
        primary.add(environment)
    if rows:
        psql(state["container"], "BEGIN;\n" + "\n".join(rows) + "\nCOMMIT;")


def seed_deployments(state, resource_id, deployments, user="owner", then=()):
    """Inserts each deployment with a placement per spec region; a `live` one (the default) runs in them.

    One transaction holds every row, then the `then` statements.
    """
    q = sql_literal
    target = json.loads(
        psql(
            state["container"],
            "SELECT json_build_object('org', w.org_id, 'workspace', w.id, 'slug', r.slug, 'environments', "
            "(SELECT json_object_agg(e.slug, e.id) FROM environments e WHERE e.workspace_id=w.id)) "
            f"FROM resources r JOIN workspaces w ON w.id=r.workspace_id WHERE r.id={q(resource_id)};",
        )
    )
    actor = state["users"][user]["id"]
    ids, rows = [], []
    placeholders, now = placeholder_ids(state), time.time()
    for deployment in deployments:
        deployment_id = deployment.get("id") or str(uuid.uuid4())
        ids.append(deployment_id)
        environment = target["environments"][deployment["environment"]]
        runtime = deployment["spec"]
        status = deployment.get("status", "running")
        live = deployment.get("live", True)
        created = f"NOW()-{q(deployment.get('age', '0 seconds'))}::interval"
        rows.append(
            "INSERT INTO deployments(id,resource_id,status,environment_id,spec,spec_version,trigger,"
            "actor_type,actor_id,method,created_at,started_at) "
            f"VALUES({q(deployment_id)},{q(resource_id)},{q(status)},{q(environment)},{q(json.dumps(runtime))},"
            f"1,{q(deployment.get('trigger', 'deploy'))},'user',{q(actor)},{q(deployment.get('method', 'web'))},"
            f"{created},{created});"
        )
        for region, sizing in runtime["regions"].items():
            placement = deployment.get("placements", {}).get(region, {})
            cluster = placement.get("cluster")
            if cluster is None:
                cluster = str(uuid.uuid4())
                rows.append(cluster_row(cluster, region))
            placement_status = placement.get(
                "status", "pending" if status == "building" else status
            )
            image = "" if placement_status == "pending" else runtime["build"]["image"]
            rows.append(
                "INSERT INTO deployment_placements(deployment_id,cluster_id,region,replicas,status,image,started_at) "
                f"VALUES({q(deployment_id)},{q(cluster)},{q(region)},{sizing['minReplicas']},"
                f"{q(placement_status)},{q(image)},{created});"
            )
            placement_id = str(uuid.uuid4())
            if "replicas" in placement or "problem" in placement:
                snapshot = {
                    "replicas": [
                        {
                            "uid": replica["name"],
                            "deployment_id": deployment_id,
                            "placement_revision": 1,
                            **replica,
                        }
                        for replica in placement.get("replicas", [])
                    ]
                }
                if "problem" in placement:
                    snapshot["problem"] = placement["problem"]
                if live:
                    snapshot["placementId"] = placement_id
                rows.append(
                    "INSERT INTO deployment_rollouts(deployment_id,cluster_id,revision,snapshot) "
                    f"VALUES({q(deployment_id)},{q(cluster)},1,"
                    f"{q(json.dumps(fill_placeholders(snapshot, placeholders, now)))});"
                )
            if not live:
                continue
            app = {
                "type": "SERVICE",
                "resourceId": resource_id,
                "slug": target["slug"],
                "workspaceId": target["workspace"],
                "organizationId": target["org"],
                "environmentId": environment,
                "environmentName": deployment["environment"],
                "deploymentId": deployment_id,
                "region": region,
                "serviceSpec": {
                    "deployment": {
                        "image": runtime["build"]["image"],
                        "observability": runtime.get("observability", {}),
                        **({"port": runtime["port"]} if "port" in runtime else {}),
                    },
                    "resources": {
                        "cpu": sizing["cpu"],
                        "memory": sizing["memory"],
                        "replicas": {
                            "min": sizing["minReplicas"],
                            "max": sizing["maxReplicas"],
                        },
                    },
                },
            }
            rows.append(
                "INSERT INTO placements(id,resource_id,cluster_id,region,deployment_id,environment_id,desired_spec,"
                "applied_revision,observed_revision,ready_replicas,status_updated_at) "
                f"VALUES({q(placement_id)},{q(resource_id)},{q(cluster)},{q(region)},{q(deployment_id)},{q(environment)},"
                f"{q(json.dumps({'app_spec': app}))},1,1,{placement.get('ready_replicas', sizing['minReplicas'])},NOW());"
            )
    rows += then
    if rows:
        psql(state["container"], "BEGIN;\n" + "\n".join(rows) + "\nCOMMIT;")
    return ids


def repo_root():
    return Path(run(["git", "rev-parse", "--show-toplevel"], capture=True).strip())


def cmd_build(args):
    build(repo_root(), dict(os.environ))


def cmd_openbao(args):
    root = Path(
        testres("lease", "-name", f"{LABEL}-shared-openbao", "-ttl", f"{LEASE_S}s")
    )
    state = {"id": root.name, "root": str(root), "compose": root.name}
    try:
        testres("add", root, "compose", state["compose"])
        state["address"] = start_openbao(
            repo_root(), state["compose"], dict(os.environ)
        )
        write_json(root / "state.json", state)
    except BaseException:
        stop_state(state)
        raise
    print(state["address"])


def cmd_stop_openbao(args):
    for state in states():
        if state.get("address") == args.address:
            stop_state(state)


def check_ref_worktree(repo, ref):
    commit = run(
        ["git", "rev-parse", f"{ref}^{{commit}}"], cwd=repo, capture=True
    ).strip()
    head = run(["git", "rev-parse", "HEAD"], cwd=repo, capture=True).strip()
    if commit == head:
        dirty = run(
            ["git", "status", "--porcelain", "--untracked-files=all"],
            cwd=repo,
            capture=True,
        ).strip()
        if dirty:
            raise EvidenceError(
                f"ref {ref} excludes uncommitted files; commit them before capture:\n{dirty}"
            )


def states():
    """Yields the state of every run and shared OpenBao on this machine."""
    for lease in testres("list").splitlines():
        try:
            yield load_state(Path(lease) / "state.json")
        except OSError, ValueError:
            continue


def renew(state):
    """Extends the lease of a run and of the shared OpenBao it uses."""
    address = state.get("openbao")
    shared = [s["root"] for s in states() if address and s.get("address") == address]
    testres("renew", state["root"], *shared)


def reap():
    """Stops runs whose lease expired, and every other harness's leftovers whose owner is gone."""
    try:
        testres("reap")
    except EvidenceError as err:
        log(f"skipped leftovers that could not be stopped: {err}")


def start_state(*, ref, name, scenario, issuer=None):
    repo = repo_root()
    openbao = os.environ.get(OPENBAO_ENV)
    if not openbao:
        check_ref_worktree(repo, ref)
    scenario_path = str(Path(scenario).resolve())
    scenario = json.loads(Path(scenario).read_text())
    root = Path(testres("lease", "-name", f"{LABEL}-{name}", "-ttl", f"{LEASE_S}s"))
    run_id = root.name
    state_path = root / "state.json"
    state = {
        "id": run_id,
        "name": name,
        "ref": ref,
        "scenario": scenario_path,
        "root": str(root),
    }
    if openbao:
        worktree = repo
        state["openbao"] = openbao
    else:
        worktree = root / "src"
        state["worktree"] = str(worktree)
    try:
        renew(state)
        if not openbao:
            testres("add", root, "worktree", worktree)
            run(
                [
                    "git",
                    "-C",
                    str(repo),
                    "worktree",
                    "add",
                    "--detach",
                    str(worktree),
                    ref,
                ]
            )
        state["commit"] = run(
            ["git", "-C", str(worktree), "rev-parse", "HEAD"], capture=True
        ).strip()
        names = ("db", "api", "ui", "unused")
        reserved = map(int, testres("ports", root, len(names)).split())
        ports = dict(zip(names, reserved, strict=True))
        db_port, api_port, ui_port = ports["db"], ports["api"], ports["ui"]
        state.update(
            {
                "ports": ports,
                "api_url": f"http://{HOST}:{api_port}",
                "ui_url": f"http://{HOST}:{ui_port}",
                "issuer": issuer or f"http://{HOST}:{ui_port}/dex",
            }
        )
        write_json(state_path, state)

        env = dict(os.environ)
        if not openbao:
            state["compose"] = run_id
            write_json(state_path, state)
            testres("add", root, "compose", state["compose"])
            run(["mise", "trust", "--quiet", str(worktree / "mise.toml")])
            openbao = start_openbao(worktree, state["compose"], env)
            build(worktree, env)
        dist = worktree / "web" / "dist"

        image = compose_postgres_image((worktree / "dev" / "compose.yaml").read_text())
        container = run_id
        run(
            ["mise", "exec", "--", "go", "run", "./tools/pullimage", image],
            cwd=worktree,
            env=env,
        )
        testres("add", root, "container", container)
        run(
            [
                "docker",
                "run",
                "--pull",
                "never",
                "-d",
                "--name",
                container,
                "-e",
                f"POSTGRES_USER={DB_USER}",
                "-e",
                f"POSTGRES_PASSWORD={DB_PASSWORD}",
                "-e",
                f"POSTGRES_DB={DB_NAME}",
                *postgres_network(db_port, image),
            ],
            capture=True,
        )
        state["container"] = container
        write_json(state_path, state)
        wait_postgres(container)

        dev_env = read_api_env((worktree / "dev" / "api.env").read_text())
        dev_env["LOCO_SECRETS_TRANSIT_ADDR"] = openbao
        api_log = root / "api.log"
        api_cmd = [str(worktree / "api" / "bin" / "loco-api")]
        testres(
            "add",
            root,
            "process",
            spawn(
                api_cmd,
                worktree,
                dict(env, **api_env(dev_env, ports, state["issuer"])),
                api_log,
            ),
        )
        ui_log = root / "ui.log"
        ui_cmd = [
            sys.executable,
            str(Path(__file__).resolve()),
            "serve-ui",
            "--root",
            str(dist),
            "--port",
            str(ui_port),
            "--api-url",
            state["api_url"],
        ]
        testres("add", root, "process", spawn(ui_cmd, worktree, env, ui_log))
        write_json(state_path, state)
        wait_http(f"{state['api_url']}/health", api_log)
        wait_http(state["ui_url"] + "/", ui_log)

        seed(state, scenario)
        write_json(state_path, state)
    except BaseException:
        for name in ("api.log", "ui.log"):
            if (root / name).exists():
                log(f"{name}:\n{log_tail(root / name)}")
        stop_state(state)
        raise
    for note in state["notes"]:
        log(f"scenario note: {note}")
    return state_path


def cmd_up(args):
    print(start_state(ref=args.ref, name=args.name, scenario=args.scenario))


def build(checkout, env):
    run(["mise", "trust", "--quiet", str(checkout / "mise.toml")])
    run(
        ["mise", "exec", "--", "bun", "install", "--frozen-lockfile"],
        cwd=checkout,
        env=env,
    )
    run(
        ["mise", "exec", "--", "go", "build", "-o", "./bin/loco-api", "."],
        cwd=checkout / "api",
        env=env,
    )
    # The API address reaches the UI through runtime-config.js, as the chart
    # serves it in production, so one build serves every API.
    run(["mise", "exec", "--", "bun", "run", "build"], cwd=checkout / "web", env=env)


def runtime_config(api_url):
    return f"window.locoConfig = {json.dumps({'apiURL': api_url})};\n"


class UIHandler(http.server.SimpleHTTPRequestHandler):
    """Serve the production UI with the chart's same-origin /api forwarding."""

    def __init__(self, *args, api_url, csp="", **kwargs):
        self.api_url = api_url
        self.csp = csp
        super().__init__(*args, **kwargs)

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path.startswith("/api/"):
            return self.proxy_api()
        if path == DISCOVERY_PATH:
            host, port = self.server.server_address[:2]
            return self.send_body(
                "application/json",
                json.dumps(discovery_document(f"http://{host}:{port}/dex")),
            )
        if path == RUNTIME_CONFIG_PATH:
            return self.send_body("text/javascript", runtime_config("/api"))
        return super().do_GET()

    def do_POST(self):
        if self.path.startswith("/api/"):
            return self.proxy_api()
        self.send_error(501, "Unsupported method")

    def end_headers(self):
        if self.csp and not self.path.startswith("/api/"):
            self.send_header("Content-Security-Policy", self.csp)
        super().end_headers()

    def proxy_api(self):
        request = urllib.request.Request(
            self.api_url + self.path.removeprefix("/api"),
            data=self.rfile.read(int(self.headers.get("Content-Length", "0")))
            if self.command == "POST"
            else None,
            headers={
                key: value
                for key, value in self.headers.items()
                if key.lower() not in ("host", "connection")
            },
            method=self.command,
        )
        try:
            response = urllib.request.urlopen(request)
        except urllib.error.HTTPError as error:
            response = error
        except urllib.error.URLError:
            self.send_error(502, "API unavailable")
            return
        with response:
            self.send_response(response.status)
            for key, value in response.headers.items():
                if key.lower() not in ("connection", "transfer-encoding"):
                    self.send_header(key, value)
            self.end_headers()
            try:
                while chunk := response.read1(64 * 1024):
                    self.wfile.write(chunk)
                    self.wfile.flush()
            except BrokenPipeError, ConnectionResetError:
                pass  # The page left while a stream was being forwarded.

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
    config = tomllib.loads((Path(args.root).parent / "sws.toml").read_text())
    csp = next(
        header["headers"]["Content-Security-Policy"]
        for header in config["advanced"]["headers"]
        if header["source"] == "**"
    )

    def handler(*a, **kw):
        return UIHandler(*a, directory=args.root, api_url=args.api_url, csp=csp, **kw)

    with UIServer((HOST, args.port), handler) as server:
        server.serve_forever()


SETTLE_SCRIPT = """async ({timeout, excluded}) => {
  const finite = () => document.getAnimations().filter((a) => {
    const end = a.effect && a.effect.getComputedTiming().endTime;
    return a.playState !== "finished" && Number.isFinite(end)
      && !excluded.some((selector) => a.effect?.target?.matches(selector));
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


def settle(page, *, exclude_animations=()):
    """Waits for the page to stop navigating, the loading screen to leave and animations to finish."""
    from playwright.sync_api import (
        Error as PlaywrightError,
        TimeoutError as PlaywrightTimeout,
    )

    deadline = time.monotonic() + SETTLE_TIMEOUT_MS / 1000
    while True:
        url = page.url
        try:
            page.wait_for_load_state("load", timeout=STEP_TIMEOUT_MS)
            page.locator(APP_LOADER).wait_for(state="detached", timeout=STEP_TIMEOUT_MS)
            settled = page.evaluate(
                SETTLE_SCRIPT,
                {"timeout": SETTLE_TIMEOUT_MS, "excluded": exclude_animations},
            )
        except PlaywrightTimeout:
            raise EvidenceError("the loading screen did not leave") from None
        except PlaywrightError as err:
            if "Execution context was destroyed" not in str(err):
                raise EvidenceError(f"settling the page failed: {err}") from None
            settled = None
        if settled is not None and page.url == url:
            break
        if time.monotonic() > deadline:
            raise EvidenceError(f"the page kept navigating, last to {page.url}")
    if not settled:
        raise EvidenceError("animations did not finish")


def authenticate(context, state, token):
    # Chromium's cookie injection requires a secure scheme for __Host-
    # cookies. The browser still sends them to the loopback HTTP fixture.
    context.add_cookies(
        [
            {
                "name": SESSION_COOKIE,
                "value": token,
                "url": state["ui_url"].replace("http://", "https://", 1),
                "httpOnly": True,
                "secure": True,
                "sameSite": "Lax",
            }
        ]
    )


def capture(browser, state, shot, theme, narrow, target):
    from playwright.sync_api import (
        Error as PlaywrightError,
        TimeoutError as PlaywrightTimeout,
    )

    ids, now = placeholder_ids(state), time.time()
    shot = {
        **shot,
        "path": fill_placeholders(shot["path"], ids, now),
        "respond": fill_placeholders(shot.get("respond", []), ids, now),
    }
    user = state["users"].get(shot["user"]) if "user" in shot else None
    if "user" in shot and user is None:
        raise EvidenceError(f"state has no user {shot['user']!r}")
    viewport = dict(NARROW if narrow else DESKTOP)
    context = browser.new_context(
        viewport=viewport, device_scale_factor=SCALE, color_scheme=theme
    )
    context.clock.install(time=now)
    if "permissions" in shot:
        context.grant_permissions(shot["permissions"], origin=state["ui_url"])
    script = f"localStorage.setItem({json.dumps(MODE_KEY)}, {json.dumps(theme)});"
    if user is not None:
        authenticate(context, state, user["token"])
    if "color" in shot:
        script += f"localStorage.setItem({json.dumps(COLOR_KEY)}, {json.dumps(shot['color'])});"
    context.add_init_script(script)

    def responder(response):
        def respond(route):
            body = route.request.post_data_json or {}
            if any(
                body.get(key) != value
                for key, value in response.get("match", {}).items()
            ):
                route.fallback()
                return
            route.fulfill(
                status=response.get("status", 200),
                headers=response.get("headers"),
                json=response["json"],
            )

        return respond

    # Playwright tries the newest route first, so the first listed entry wins.
    for response in reversed(shot["respond"]):
        context.route(response["url"], responder(response))
    for pattern in shot.get("hold_requests", []):
        context.route(pattern, lambda route: None)
    problems = []
    page = context.new_page()
    page.on(
        "console",
        lambda m: (
            problems.append(f"console {m.type}: {m.text}")
            if m.type == "error"
            else None
        ),
    )
    page.on(
        "requestfailed",
        lambda r: problems.append(f"request failed: {r.url} {r.failure}"),
    )
    exclude_animations = shot.get("exclude_animations", [])
    steps = [*shot.get("steps", []), {"wait": shot["ready_selector"]}]
    try:
        page.goto(
            state["ui_url"] + shot["path"],
            wait_until="domcontentloaded",
        )
        settle(page, exclude_animations=exclude_animations)
        for step in steps:
            action, value = next(iter(step.items()))
            selector = value["selector"] if isinstance(value, dict) else value
            locator = page.locator(selector)
            try:
                locator.first.wait_for(state="visible", timeout=STEP_TIMEOUT_MS)
            except PlaywrightTimeout:
                raise EvidenceError(
                    f"{shot['name']}: selector {selector!r} never became visible"
                ) from None
            if action == "click":
                locator.first.click()
            elif action == "type":
                locator.first.fill(value["text"])
            elif action == "hover":
                locator.first.hover()
            elif action == "press":
                locator.first.press(value["key"])
            elif action == "drag":
                locator.first.hover(timeout=STEP_TIMEOUT_MS)
                box = locator.first.bounding_box()

                def at(f, box=box):
                    return (
                        box["x"] + f[0] * box["width"],
                        box["y"] + f[1] * box["height"],
                    )

                page.mouse.move(*at(value["from"]))
                page.mouse.down()
                page.mouse.move(*at(value["to"]), steps=8)
                page.mouse.up()
            settle(page, exclude_animations=exclude_animations)
        applied = f"""() => {{
          const root = document.documentElement;
          const scheme = getComputedStyle(root).colorScheme;
          return scheme === "normal"
            ? root.classList.contains("dark") === {json.dumps(theme == "dark")}
            : scheme === {json.dumps(theme)};
        }}"""
        try:
            page.wait_for_function(applied, timeout=STEP_TIMEOUT_MS)
        except PlaywrightTimeout:
            raise EvidenceError(
                f"{shot['name']}: the page did not apply the {theme} theme"
            ) from None
        settle(page, exclude_animations=exclude_animations)
        crop = shot.get("crop", "viewport")
        if crop == "full" or isinstance(crop, dict) and "selector" in crop:
            viewport["height"] = max(
                viewport["height"],
                page.evaluate("document.documentElement.scrollHeight"),
            )
            page.set_viewport_size(viewport)
            page.evaluate("window.scrollTo(0, 0)")
            settle(page, exclude_animations=exclude_animations)
        tmp = target.with_suffix(".partial.png")
        if crop in ("full", "viewport"):
            page.screenshot(path=tmp)
        elif "clip" in crop:
            x, y, width, height = crop["clip"]
            page.screenshot(
                path=tmp, clip={"x": x, "y": y, "width": width, "height": height}
            )
        else:
            locator = page.locator(crop["selector"])
            if locator.count() == 0:
                raise EvidenceError(
                    f"{shot['name']}: crop selector {crop['selector']!r} matches nothing"
                )
            box = locator.first.bounding_box()
            pad = crop.get("padding", 0)
            x, y = max(box["x"] - pad, 0), max(box["y"] - pad, 0)
            width = min(box["x"] + box["width"] + pad, viewport["width"]) - x
            height = min(box["y"] + box["height"] + pad, viewport["height"]) - y
            page.screenshot(
                path=tmp, clip={"x": x, "y": y, "width": width, "height": height}
            )
        tmp.rename(target)
    except (EvidenceError, PlaywrightError) as err:
        failed = target.parent / "failed" / target.name
        failed.parent.mkdir(exist_ok=True)
        try:
            page.screenshot(path=failed, timeout=STEP_TIMEOUT_MS)
        except PlaywrightError as shot_err:
            failed = f"not taken: {shot_err.message}"
        detail = "; ".join(problems[-5:]) or "no console errors or failed requests"
        raise EvidenceError(
            f"{err} (page at {page.url}; {detail}; screenshot {failed})"
        ) from None
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
    manifest_path = out / ".scenarios.json"
    manifest = json.loads(manifest_path.read_text()) if manifest_path.exists() else {}
    run([sys.executable, "-m", "playwright", "install", "chromium"], capture=True)
    written, failures = [], []
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch()
        for shot in shots:
            renew(state)
            if state["name"] == "before" and shot.get("before") is False:
                continue
            for theme in shot.get("themes", THEMES):
                for narrow in (False, True) if shot.get("narrow") else (False,):
                    target = out / shot_filename(
                        shot["name"], state["name"], theme, narrow
                    )
                    owner = manifest.get(target.name, state["scenario"])
                    if target.exists() and owner != state["scenario"]:
                        failures.append(
                            f"{target.name}: written by scenario {owner}; "
                            "shoot this run into another --out"
                        )
                        continue
                    target.unlink(missing_ok=True)
                    try:
                        capture(browser, state, shot, theme, narrow, target)
                        written.append(target)
                        manifest[target.name] = state["scenario"]
                    except EvidenceError as err:
                        failures.append(f"{target.name}: {err}")
        browser.close()
    write_json(manifest_path, manifest)
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
    testres("release", state["root"])
    log(f"stopped {state['id']}")


def cmd_down(args):
    reap()
    if args.all:
        for lease in testres("list").splitlines():
            if Path(lease).name.startswith(LABEL + "-"):
                testres("release", lease)
                log(f"stopped {Path(lease).name}")
        run(["git", "worktree", "prune"])
    elif Path(args.state).exists():
        stop_state(load_state(args.state))


def main():
    parser = argparse.ArgumentParser(
        description="Collect before and after screenshots of the web UI"
    )
    sub = parser.add_subparsers(dest="command", required=True)
    up = sub.add_parser(
        "up", help="build a ref and start a throwaway API, database and UI"
    )
    up.add_argument("--ref", required=True)
    up.add_argument("--name", required=True, choices=("before", "after"))
    up.add_argument("--scenario", required=True)
    up.set_defaults(func=cmd_up)
    shoot = sub.add_parser(
        "shoot", help="capture every shot of a spec in light and dark"
    )
    shoot.add_argument("--state", required=True)
    shoot.add_argument("--spec", required=True)
    shoot.add_argument("--out", required=True)
    shoot.set_defaults(func=cmd_shoot)
    table = sub.add_parser(
        "table", help="print the before and after markdown for a PR body"
    )
    table.add_argument("--out", required=True)
    table.set_defaults(func=cmd_table)
    check = sub.add_parser(
        "check", help="check a shot spec and its scenario without starting anything"
    )
    check.add_argument("--spec", required=True)
    check.add_argument("--scenario")
    check.set_defaults(func=cmd_check)
    summary = sub.add_parser(
        "failure-summary",
        help="write failure.md from the suite and shot logs in a directory",
    )
    summary.add_argument("--dir", required=True)
    summary.set_defaults(func=cmd_failure_summary)
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
    bao = sub.add_parser(
        "openbao",
        help=f"start one seeded OpenBao for ${OPENBAO_ENV} and print its address",
    )
    bao.set_defaults(func=cmd_openbao)
    stop_bao = sub.add_parser(
        "stop-openbao",
        help="remove the seeded OpenBao at an address and its volumes and network",
    )
    stop_bao.add_argument("--address", required=True)
    stop_bao.set_defaults(func=cmd_stop_openbao)
    args = parser.parse_args()
    try:
        args.func(args)
    except EvidenceError as err:
        log(f"error: {err}")
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
