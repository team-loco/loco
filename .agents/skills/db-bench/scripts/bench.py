# /// script
# requires-python = ">=3.14,<3.15"
# dependencies = ["psycopg[binary]==3.3.9"]
# ///
import argparse
import datetime
import json
import math
import os
import platform
import re
import signal
import statistics
import subprocess
import sys
import tempfile
import threading
import time
from dataclasses import dataclass
from pathlib import Path

import psycopg
from psycopg import pq

LABEL = "loco-dbbench"
REPO = Path(__file__).resolve().parents[4]
CPUS = "2"
MEMORY = "2g"
WARMUPS = 3
RUNS = 20
SIZES = (1, 10, 100, 1_000, 10_000, 100_000, 1_000_000)
# Other tenants hold ten times the measured tenant's rows, up to this many, so the 1M size seeds in minutes.
NOISE_CAP = 2_000_000
# Noise rows hang off this many other organizations, workspaces, environments and resources.
OWNERS = 100
# A rough seeding rate for the runtime estimate, measured with the 2-CPU limit.
SEED_ROWS_PER_S = 150_000
SETUP_S = 3
ISSUER = "https://idp.bench.test"
CANCELED = "57014"


class BenchError(Exception):
    pass


def log(message):
    print(message, file=sys.stderr, flush=True)


def u(kind, g):
    """The id the seed SQL's pg_temp.u(kind, g) gives row g of a table."""
    return f"{kind:08x}-0000-7000-8000-{g:012x}"


def noise(n):
    return min(10 * n, NOISE_CAP)


# --- Sources: the migrations, queries and Go constants at a git ref -----------------------------


class Source:
    def __init__(self, ref):
        self.ref = ref
        if ref is None:
            dirty = git("status", "--porcelain", "--", "api").strip()
            self.label = "working tree"
            self.commit = git("rev-parse", "HEAD").strip() + ("-dirty" if dirty else "")
        else:
            self.label = ref
            self.commit = git("rev-parse", f"{ref}^{{commit}}").strip()

    def read(self, path):
        if self.ref is None:
            return (REPO / path).read_text()
        return git("show", f"{self.commit}:{path}")

    def ls(self, directory):
        if self.ref is None:
            return sorted(p.name for p in (REPO / directory).iterdir())
        return sorted(Path(p).name for p in git("ls-tree", "--name-only", f"{self.commit}:{directory}").split())

    def migrations(self):
        return [self.read(f"api/migrations/{name}") for name in self.ls("api/migrations") if name.endswith(".sql")]

    def queries(self):
        out = {}
        for name in self.ls("api/queries"):
            if name.endswith(".sql"):
                out.update(parse_queries(self.read(f"api/queries/{name}")))
        return out

    def go_const(self, path, name):
        return go_const(self.read(path), name)


def git(*args):
    result = subprocess.run(["git", "-C", str(REPO), *args], text=True, capture_output=True)
    if result.returncode != 0:
        raise BenchError(f"git {' '.join(args)}: {result.stderr.strip()}")
    return result.stdout


def goose_up(text):
    match = re.search(r"^-- \+goose Up\s*$(.*?)(?=^-- \+goose Down|\Z)", text, re.M | re.S)
    if not match:
        raise BenchError("migration has no -- +goose Up section")
    return match.group(1)


NAME_RE = re.compile(r"^-- name: (\w+) :\w+\s*$", re.M)
ARG_RE = re.compile(r"sqlc\.n?arg\(\s*'?(\w+)'?\s*\)")
EMBED_RE = re.compile(r"sqlc\.embed\((\w+)\)")
POSITIONAL_RE = re.compile(r"\$(\d+)")


def parse_queries(text):
    """Split a sqlc query file into {name: sql}."""
    parts = NAME_RE.split(text)
    return {parts[i]: parts[i + 1].strip().rstrip(";").strip() for i in range(1, len(parts), 2)}


def bind(sql):
    """Rewrite sqlc macros to plain Postgres: named args become $n after the highest positional one,
    numbered in order of first use as sqlc does. Returns the SQL and each $n's key: the int n for a
    positional parameter, the name for a named one."""
    sql = EMBED_RE.sub(r"\1.*", sql)
    positional = max((int(n) for n in POSITIONAL_RE.findall(sql)), default=0)
    keys = {n: n for n in range(1, positional + 1)}
    numbers = {}

    def number(match):
        name = match.group(1)
        if name not in numbers:
            numbers[name] = positional + len(numbers) + 1
            keys[numbers[name]] = name
        return f"${numbers[name]}"

    sql = ARG_RE.sub(number, sql)
    return sql, [keys[n] for n in sorted(keys)]


def go_const(text, name):
    """An int or a time.Duration (as seconds) constant from Go source."""
    match = re.search(rf"^\s*{name}\s*=\s*(\d+)(?:\s*\*\s*time\.(Millisecond|Second|Minute))?\s*$", text, re.M)
    if not match:
        raise BenchError(f"no Go constant {name}")
    value = int(match.group(1))
    unit = {None: 1, "Millisecond": 0.001, "Second": 1, "Minute": 60}[match.group(2)]
    return value * unit


def encode(value):
    if value is None:
        return None
    if isinstance(value, bool):
        return b"true" if value else b"false"
    if isinstance(value, (list, tuple)):
        return ("{" + ",".join('"' + str(v).replace("\\", "\\\\").replace('"', '\\"') + '"' for v in value) + "}").encode()
    if isinstance(value, datetime.datetime):
        return value.isoformat().encode()
    return str(value).encode()


# --- Postgres container -------------------------------------------------------------------------


def postgres_image():
    """The image CI's API tests run against."""
    match = re.search(r"^\s+image: (postgres:\S+)$", (REPO / ".github/workflows/test.yml").read_text(), re.M)
    if not match:
        raise BenchError(".github/workflows/test.yml has no postgres service image")
    return match.group(1)


class Container:
    def __init__(self, image):
        self.name = f"{LABEL}-{os.getpid()}"
        self.image = image
        self.peak = 0
        self.cpu = None
        self.memory = None

    def start(self):
        docker(
            "run", "-d", "--name", self.name, "--label", LABEL,
            "--cpus", CPUS, "--memory", MEMORY, "--shm-size", "512m",
            "-e", "POSTGRES_PASSWORD=bench", "-p", "127.0.0.1::5432",
            self.image,
            "-c", "shared_preload_libraries=pg_stat_statements",
            "-c", "pg_stat_statements.track_planning=on",
            "-c", "pg_stat_statements.track_utility=off",
            "-c", "max_connections=200",
        )
        port = docker("port", self.name, "5432").splitlines()[0].rsplit(":", 1)[1]
        self.dsn = f"postgresql://postgres:bench@127.0.0.1:{port}"
        deadline = time.monotonic() + 60
        # The entrypoint's init server listens only on the socket, so a TCP check waits for the real one.
        while subprocess.run(["docker", "exec", self.name, "pg_isready", "-h", "127.0.0.1"], capture_output=True).returncode:
            if time.monotonic() > deadline:
                raise BenchError("postgres did not become ready")
            time.sleep(0.5)
        # Long-lived shells that read the cgroup files with builtins only: a `docker exec cat` per
        # reading costs the container about 5 ms of CPU, more than a fast query uses in 20 runs.
        cpu = "while read _; do while read -r k v; do [ \"$k\" = usage_usec ] && echo \"$v\"; done < /sys/fs/cgroup/cpu.stat; done"
        memory = "while :; do while read -r k v; do [ \"$k\" = anon ] && echo \"$v\"; done < /sys/fs/cgroup/memory.stat; read -t 0.1 _; done"
        self.cpu = self._shell(cpu)
        self.memory = self._shell(memory)
        threading.Thread(target=self._sample, daemon=True).start()

    def _shell(self, loop):
        return subprocess.Popen(
            ["docker", "exec", "-i", self.name, "sh", "-c", loop], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True
        )

    def _sample(self):
        for line in self.memory.stdout:
            self.peak = max(self.peak, int(line))

    def reset_peak(self):
        self.peak = 0

    def anon_mb(self):
        """The most anonymous memory, the processes' own and not page cache or shared buffers,
        sampled every 100 ms since the last reset."""
        time.sleep(0.2)
        return round(self.peak / 1024**2)

    def cpu_usec(self):
        """CPU time every process in the container used, from its cgroup."""
        self.cpu.stdin.write("\n")
        self.cpu.stdin.flush()
        line = self.cpu.stdout.readline()
        if not line:
            raise BenchError("the CPU probe exited")
        return int(line)

    def stop(self):
        for process in (self.cpu, self.memory):
            if process:
                process.kill()
        subprocess.run(["docker", "rm", "-f", "-v", self.name], capture_output=True)


def docker(*args):
    result = subprocess.run(["docker", *args], text=True, capture_output=True)
    if result.returncode != 0:
        raise BenchError(f"docker {args[0]}: {result.stderr.strip()}")
    return result.stdout


def machine(container, version):
    if sys.platform == "darwin":
        cpu = subprocess.run(["sysctl", "-n", "machdep.cpu.brand_string"], capture_output=True, text=True).stdout.strip()
        ram = int(subprocess.run(["sysctl", "-n", "hw.memsize"], capture_output=True, text=True).stdout)
    else:
        cpuinfo = Path("/proc/cpuinfo").read_text()
        cpu = re.search(r"^model name\s*: (.*)$", cpuinfo, re.M).group(1)
        ram = int(re.search(r"^MemTotal:\s+(\d+) kB", Path("/proc/meminfo").read_text(), re.M).group(1)) * 1024
    return {
        "cpu": cpu or platform.processor(),
        "cores": os.cpu_count(),
        "ram_gb": round(ram / 1024**3),
        "os": f"{platform.system()} {platform.release()}",
        "docker_limits": f"--cpus {CPUS} --memory {MEMORY}",
        "image": container.image,
        "postgres": version,
    }


# --- Scenarios ------------------------------------------------------------------------------------

BASE_SEED = f"""
CREATE FUNCTION pg_temp.u(k int, g bigint) RETURNS uuid IMMUTABLE LANGUAGE sql AS
$$ SELECT (lpad(to_hex(k), 8, '0') || '-0000-7000-8000-' || lpad(to_hex(g), 12, '0'))::uuid $$;
-- user 1 is the tenant's owner, user 2 owns every other tenant, user 3 is a second tenant member.
INSERT INTO users (id, email, name) VALUES
    (pg_temp.u(1, 1), 'owner@tenant.test', 'Owner'),
    (pg_temp.u(1, 2), 'owner@noise.test', 'Noise'),
    (pg_temp.u(1, 3), 'member@tenant.test', 'Member');
INSERT INTO organizations (id, name, created_by)
SELECT pg_temp.u(2, g), 'org-' || g, pg_temp.u(1, CASE WHEN g = 1 THEN 1 ELSE 2 END) FROM generate_series(1, {OWNERS + 1}) g;
INSERT INTO workspaces (id, org_id, name, created_by)
SELECT pg_temp.u(3, g), pg_temp.u(2, g), 'ws-' || g, pg_temp.u(1, CASE WHEN g = 1 THEN 1 ELSE 2 END) FROM generate_series(1, {OWNERS + 1}) g;
INSERT INTO environments (id, workspace_id, name, created_by)
SELECT pg_temp.u(4, g), pg_temp.u(3, g), 'production', pg_temp.u(1, 1) FROM generate_series(1, {OWNERS + 1}) g;
INSERT INTO resources (id, workspace_id, name, slug, type, description, spec, spec_version)
SELECT pg_temp.u(5, g), pg_temp.u(3, g), 'web', 'web', 'service', '', '{{}}', 1 FROM generate_series(1, {OWNERS + 1}) g;
INSERT INTO clusters (id, name, region, provider, is_active, is_default)
VALUES (pg_temp.u(6, 1), 'apps', 'local', 'bench', true, true);
"""

# Spreads n tenant rows evenly through g = 1..total, so tenant and noise rows interleave in time and seq.
TENANT = "(g::bigint * {n} / {total}) <> ((g - 1)::bigint * {n} / {total})"


@dataclass
class Scenario:
    name: str
    issue: int
    size: str
    seed: object
    cases: object
    rows: object
    sizes: tuple = SIZES
    # Writes are measured inside a transaction that rolls back, so every run sees the seeded rows.
    commit: bool = False
    concurrency: object = None
    # Repeat each statement until it affects no rows, as a batched delete loop does.
    until_empty: bool = False
    derive: object = None
    note: str = ""


def seed_retention(n):
    total = n + noise(n)
    webhooks = max(1, min(20_000, total // 50))
    return f"""
INSERT INTO webhooks (id, kind, workspace_id, url, provider, kek_id, wrapped_dek, secret)
SELECT pg_temp.u(7, g), 'workspace', pg_temp.u(3, 1 + g % {OWNERS + 1}), 'https://hook.test/' || g, 'bench', 'kek', '\\x00', '\\x00'
FROM generate_series(1, {webhooks}) g;
INSERT INTO events (seq, type, org_id, workspace_id, actor_type, created_at)
SELECT g, 'deployment.succeeded', pg_temp.u(2, 1 + g % {OWNERS + 1}), pg_temp.u(3, 1 + g % {OWNERS + 1}), 'system',
       CASE WHEN g <= {n} THEN now() - interval '60 days' + g * interval '1 ms'
            ELSE now() - interval '1 day' + (g - {n}) * interval '1 ms' END
FROM generate_series(1, {total}) g;
SELECT setval('events_seq_seq', {total});
INSERT INTO webhook_deliveries (webhook_id, event_seq, status, attempts, delivered_at)
SELECT pg_temp.u(7, 1 + g % {webhooks}), g, 'succeeded', 1, now() FROM generate_series(1, {total}) g;
"""


def cases_retention(n, src):
    cutoff = datetime.datetime.now(datetime.UTC) - datetime.timedelta(days=30)
    return {"expired": [("DeleteEventsBefore", {1: cutoff})]}


EVENT_TYPES = "ARRAY['deployment.created','deployment.succeeded','build.started','build.succeeded','resource.updated','secret.set','domain.added','member.added']"


def seed_list_events(n):
    total = n + noise(n)
    tenant = TENANT.format(n=n, total=total)
    return f"""
INSERT INTO events (seq, type, org_id, workspace_id, environment_id, actor_type, actor_id, created_at)
SELECT g, ({EVENT_TYPES})[1 + g % 8],
       CASE WHEN {tenant} THEN pg_temp.u(2, 1) ELSE pg_temp.u(2, 2 + g % {OWNERS}) END,
       CASE WHEN {tenant} THEN pg_temp.u(3, g % 2) ELSE pg_temp.u(3, 2 + g % {OWNERS}) END,
       CASE WHEN {tenant} THEN pg_temp.u(4, 1000 + g % 20) ELSE pg_temp.u(4, 2 + g % {OWNERS}) END,
       CASE WHEN {tenant} THEN 'user' ELSE 'system' END,
       CASE WHEN {tenant} THEN pg_temp.u(1, 1) END,
       now() - interval '30 days' + (g::float8 / {total}) * interval '30 days'
FROM generate_series(1, {total}) g;
SELECT setval('events_seq_seq', {total});
-- The rare type is the tenant's oldest event, so a page of it has to look through every other one.
UPDATE events SET type = 'secret.deleted' WHERE seq = (SELECT min(seq) FROM events WHERE org_id = pg_temp.u(2, 1));
"""


def cases_list_events(n, src):
    rows = src.go_const("api/service/event.go", "defaultEventPage") + 1
    now = datetime.datetime.now(datetime.UTC)
    base = {
        "org_id": u(2, 1), "environment_id": None, "start_time": None, "end_time": None, "before_seq": None,
        "types": [], "actor_ids": None, "actor_types": None, "request_id": "", "max_rows": rows,
    }
    window = now - datetime.timedelta(days=10)
    return {
        "org rare type": [("ListOrgAuditEvents", base | {"types": ["secret.deleted"]})],
        "org environment": [("ListOrgAuditEvents", base | {"environment_id": u(4, 1007)})],
        "org 1h window": [("ListOrgAuditEvents", base | {"start_time": window, "end_time": window + datetime.timedelta(hours=1)})],
        "workspace absent type": [("ListWorkspaceAuditEvents", base | {"workspace_id": u(3, 1), "types": ["bench.absent"]})],
    }


def seed_webhook_deliveries(n):
    total = n + noise(n)
    tenant = TENANT.format(n=n, total=total)
    return f"""
INSERT INTO webhooks (id, kind, workspace_id, url, provider, kek_id, wrapped_dek, secret)
SELECT pg_temp.u(7, g), 'workspace', pg_temp.u(3, g), 'https://hook.test/' || g, 'bench', 'kek', '\\x00', '\\x00'
FROM generate_series(1, {OWNERS + 1}) g;
INSERT INTO events (seq, type, org_id, workspace_id, actor_type)
SELECT g, 'deployment.succeeded', pg_temp.u(2, 1), pg_temp.u(3, 1), 'system' FROM generate_series(1, {total}) g;
SELECT setval('events_seq_seq', {total});
-- The tenant's five oldest deliveries failed; one in a hundred of everyone else's did.
INSERT INTO webhook_deliveries (webhook_id, event_seq, status, attempts, created_at)
SELECT CASE WHEN {tenant} THEN pg_temp.u(7, 1) ELSE pg_temp.u(7, 2 + g % {OWNERS}) END, g,
       CASE WHEN {tenant} THEN CASE WHEN g::bigint * {n} / {total} <= 5 THEN 'failed' ELSE 'succeeded' END
            WHEN g % 100 = 0 THEN 'failed' ELSE 'succeeded' END,
       1, now() - interval '30 days' + (g::float8 / {total}) * interval '30 days'
FROM generate_series(1, {total}) g;
"""


def cases_webhook_deliveries(n, src):
    rows = src.go_const("api/service/position_cursor.go", "defaultPageSize")
    params = {"webhook_id": u(7, 1), "status": "failed", "page_token": None, "page_created_at": None, "max_rows": rows}
    return {"failed": [("ListWebhookDeliveries", params)]}


def seed_user_workspaces(n):
    other = noise(n)
    return f"""
INSERT INTO users (id, email) SELECT pg_temp.u(1, 100 + g), 'member' || g || '@noise.test' FROM generate_series(1, {OWNERS}) g;
INSERT INTO workspaces (id, org_id, name, created_by, created_at)
SELECT pg_temp.u(30, g), pg_temp.u(2, 1), 'tenant-' || g, pg_temp.u(1, 1), now() - random() * interval '365 days'
FROM generate_series(1, {n}) g;
INSERT INTO workspaces (id, org_id, name, created_by, created_at)
SELECT pg_temp.u(31, g), pg_temp.u(2, 2 + g % {OWNERS}), 'noise-' || g, pg_temp.u(1, 2), now() - random() * interval '365 days'
FROM generate_series(1, {other}) g;
INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) VALUES
    (pg_temp.u(1, 1), 'read', 'organization', pg_temp.u(2, 1)),
    (pg_temp.u(1, 3), 'read', 'workspace', pg_temp.u(30, 1));
INSERT INTO user_scopes (user_id, scope, entity_type, entity_id)
SELECT pg_temp.u(1, 101 + g % {OWNERS}), 'read', 'workspace', pg_temp.u(31, g) FROM generate_series(1, {other}) g;
"""


def cases_user_workspaces(n, src):
    rows = src.go_const("api/service/position_cursor.go", "defaultPageSize")
    params = {2: rows, "page_token": None, "page_created_at": None}
    return {
        "org member": [("ListWorkspacesForUser", params | {1: u(1, 1)})],
        "one-workspace member": [("ListWorkspacesForUser", params | {1: u(1, 3)})],
    }


RESOLVE_NOISE = 10_000


def seed_resolve_identity(n):
    return f"""
INSERT INTO identities (user_id, issuer, subject, email, email_verified)
VALUES (pg_temp.u(1, 1), '{ISSUER}', 'owner', 'owner@tenant.test', true);
INSERT INTO users (id, email) SELECT pg_temp.u(1, 100 + g), 'user' || g || '@noise.test' FROM generate_series(1, {RESOLVE_NOISE}) g;
INSERT INTO identities (user_id, issuer, subject, email, email_verified)
SELECT pg_temp.u(1, 100 + g), '{ISSUER}', 'user' || g, 'user' || g || '@noise.test', true FROM generate_series(1, {RESOLVE_NOISE}) g;
INSERT INTO invitations (org_id, email, invited_by, expires_at)
SELECT pg_temp.u(2, 2 + g % {OWNERS}), 'invitee' || g || '@noise.test', pg_temp.u(1, 2), now() + interval '7 days'
FROM generate_series(1, {RESOLVE_NOISE}) g;
INSERT INTO user_scopes (user_id, scope, entity_type, entity_id)
SELECT pg_temp.u(1, 1), s, 'organization', pg_temp.u(2, 1) FROM unnest(ARRAY['read', 'write', 'admin']) s;
INSERT INTO user_scopes (user_id, scope, entity_type, entity_id)
SELECT pg_temp.u(1, 100 + g), 'read', 'organization', pg_temp.u(2, 2 + g % {OWNERS}) FROM generate_series(1, {RESOLVE_NOISE}) g;
"""


def cases_resolve_identity(n, src):
    email = "owner@tenant.test"
    return {
        "request": [
            ("GetUserByIdentity", {1: ISSUER, 2: "owner"}),
            ("TouchIdentity", {1: ISSUER, 2: "owner", 3: email, 4: True}),
            ("ListOpenInvitationsForEmail", {"email": email}),
            ("GetUserScopes", {1: u(1, 1)}),
        ]
    }


def seed_scope_checks(n):
    other = noise(n)
    return f"""
INSERT INTO resources (id, workspace_id, name, slug, type, description, spec, spec_version)
SELECT pg_temp.u(50, g), pg_temp.u(3, 1), 'r' || g, 'r' || g, 'service', '', '{{}}', 1 FROM generate_series(1, {n}) g;
INSERT INTO resources (id, workspace_id, name, slug, type, description, spec, spec_version)
SELECT pg_temp.u(51, g), pg_temp.u(3, 2 + g % {OWNERS}), 'r' || g, 'r' || g, 'service', '', '{{}}', 1 FROM generate_series(1, {other}) g;
"""


def cases_scope_checks(n, src):
    return {"resource scopes": [("GetWorkspaceOrganizationIDByResourceID", {1: u(50, g)}) for g in range(1, n + 1)]}


def seed_delete_cascade(n):
    other = noise(n)
    return f"""
INSERT INTO placement_windows (resource_id, cluster_id, workspace_id, placed_at, removed_at)
SELECT pg_temp.u(60, g), pg_temp.u(6, 1), CASE WHEN g <= {n} THEN pg_temp.u(3, 1) ELSE pg_temp.u(3, 2 + g % {OWNERS}) END, now(), now()
FROM generate_series(1, {n + other}) g;
INSERT INTO registry_credentials (kind, cluster_id, workspace_id, secret_hash, expires_at)
SELECT 'pull', pg_temp.u(6, 1), CASE WHEN g <= {n} THEN pg_temp.u(3, 1) ELSE pg_temp.u(3, 2 + g % {OWNERS}) END,
       decode(md5(g::text), 'hex'), now() + interval '1 day'
FROM generate_series(1, {n + other}) g;
INSERT INTO session_tokens (access_token_hash, refresh_token_hash, user_id, access_expires_at, refresh_expires_at)
SELECT 'a' || g, 'r' || g, pg_temp.u(1, 3), now() + interval '1 hour', now() + interval '30 days' FROM generate_series(1, {n}) g;
INSERT INTO invitations (org_id, email, invited_by, expires_at)
SELECT pg_temp.u(2, 2 + g % {OWNERS}), 'invitee' || g || '@noise.test', pg_temp.u(1, 2), now() + interval '7 days'
FROM generate_series(1, {other}) g;
"""


def cases_delete_cascade(n, src):
    return {
        "workspace": [("RemoveWorkspace", {1: u(3, 1)})],
        "user": [("DeleteUser", {1: u(1, 3)})],
    }


def seed_watch_deployment(n):
    total = n + noise(n)
    return f"""
INSERT INTO deployments (id, resource_id, cluster_id, region, replicas, status, is_active, message, environment_id,
                         spec, spec_version, actor_type, actor_id, method, started_at)
SELECT pg_temp.u(80, g), pg_temp.u(5, CASE WHEN g <= {n} THEN 1 ELSE 2 + g % {OWNERS} END), pg_temp.u(6, 1), 'local', 1,
       'deploying', false, '', pg_temp.u(4, CASE WHEN g <= {n} THEN 1 ELSE 2 + g % {OWNERS} END),
       '{{}}', 1, 'user', pg_temp.u(1, 1), 'cli', now()
FROM generate_series(1, {total}) g;
"""


def cases_watch_deployment(n, src):
    return {"poll": [("GetDeploymentStatus", {1: u(80, max(1, n // 2))})]}


def derive_watch_deployment(n, src, result):
    interval = src.go_const("api/service/deployment.go", "watchDeploymentPollInterval")
    polls = n / interval
    return {"polls/s": round(polls, 1), "DB cores": round(polls * result["cpu_ms"] / 1000, 3)}


def seed_delete_secrets(n):
    other = noise(n)
    return f"""
INSERT INTO secrets (resource_id, environment_id, name, nonce, ciphertext, updated_by_type, updated_by_id, created_by_type, created_by_id)
SELECT pg_temp.u(5, 1), pg_temp.u(4, 1), 'S_' || g, '\\x00', '\\x00', 'user', pg_temp.u(1, 1), 'user', pg_temp.u(1, 1)
FROM generate_series(1, {n}) g;
INSERT INTO secrets (resource_id, environment_id, name, nonce, ciphertext, updated_by_type, updated_by_id, created_by_type, created_by_id)
SELECT pg_temp.u(5, 2 + g % {OWNERS}), pg_temp.u(4, 2 + g % {OWNERS}), 'S_' || g, '\\x00', '\\x00', 'user', pg_temp.u(1, 2), 'user', pg_temp.u(1, 2)
FROM generate_series(1, {other}) g;
"""


def cases_delete_secrets(n, src):
    return {"names": [("DeleteSecrets", {1: u(5, 1), 2: u(4, 1), "names": [f"S_{g}" for g in range(1, n + 1)]})]}


SCENARIOS = {
    s.name: s
    for s in (
        Scenario(
            "retention", 1821, "expired events, each with a delivery; webhooks scale with all deliveries up to 20k",
            seed_retention, cases_retention, lambda n: 3 * (n + noise(n)), until_empty=True,
        ),
        Scenario(
            "list-events", 1822, "the organization's events; the rare type is its oldest event",
            seed_list_events, cases_list_events, lambda n: n + noise(n),
        ),
        Scenario(
            "webhook-deliveries", 1823, "the webhook's deliveries, five of them failed",
            seed_webhook_deliveries, cases_webhook_deliveries, lambda n: 2 * (n + noise(n)),
        ),
        Scenario(
            "user-workspaces", 1824, "workspaces in the caller's organization",
            seed_user_workspaces, cases_user_workspaces, lambda n: n + 2 * noise(n),
        ),
        Scenario(
            "resolve-identity", 1825, "concurrent requests from one user, each committing",
            seed_resolve_identity, cases_resolve_identity, lambda n: 5 * RESOLVE_NOISE,
            sizes=(1, 4, 16, 64), commit=True, concurrency=lambda n: n,
            note="A rate problem, not a size problem: the sizes are concurrent requests, and WAL records show the write each request makes.",
        ),
        Scenario(
            "scope-checks", 1826, "resource scopes in the request, each held through the organization",
            seed_scope_checks, cases_scope_checks, lambda n: n + noise(n),
            sizes=SIZES[:5],
            note="Latency is per request, the sum of one owner lookup per scope. 100k scopes exceed the API's 4 MiB message limit.",
        ),
        Scenario(
            "delete-cascade", 1827,
            "workspace: its placement windows and registry credentials; user: their sessions, with 10x invitations by others",
            seed_delete_cascade, cases_delete_cascade, lambda n: 3 * n + 3 * noise(n),
        ),
        Scenario(
            "watch-deployment", 1828, "open watch streams, each polling its own deployment",
            seed_watch_deployment, cases_watch_deployment, lambda n: n + noise(n), derive=derive_watch_deployment,
            note="A rate problem: each row measures one poll, and polls/s and DB cores extrapolate to N streams at the API's poll interval.",
        ),
        Scenario(
            "delete-secrets", 1829, "secret names in one DeleteSecrets request, all present",
            seed_delete_secrets, cases_delete_secrets, lambda n: n + noise(n),
        ),
    )
}


# --- Measurement ----------------------------------------------------------------------------------


@dataclass
class Prepared:
    name: str
    sql: str
    keys: list


def connect(dsn, db):
    return psycopg.connect(f"{dsn}/{db}", autocommit=True)


def exec_checked(pg, result):
    if result.status not in (pq.ExecStatus.COMMAND_OK, pq.ExecStatus.TUPLES_OK):
        state = (result.error_field(pq.DiagnosticField.SQLSTATE) or b"").decode()
        message = (result.error_message or b"").decode().strip()
        raise QueryError(state, message)
    return result


class QueryError(BenchError):
    def __init__(self, state, message):
        super().__init__(f"{state}: {message}")
        self.state = state


def run_statements(conn, statements, prepared, until_empty, commit):
    """One request: every statement in order. Returns the client-side seconds it took."""
    pg = conn.pgconn
    if not commit:
        exec_checked(pg, pg.exec_(b"BEGIN"))
    start = time.perf_counter()
    try:
        for query, params in statements:
            p = prepared[query]
            values = [encode(params[k]) for k in p.keys]
            while True:
                result = exec_checked(pg, pg.exec_prepared(p.name.encode(), values))
                if not until_empty or result.command_tuples == 0:
                    break
        return time.perf_counter() - start
    finally:
        if not commit:
            pg.exec_(b"ROLLBACK")


def plan_summary(explain):
    root = explain[0]
    triggers = root.get("Triggers", [])
    if triggers:
        slowest = max(triggers, key=lambda t: t["Time"])
        if sum(t["Time"] for t in triggers) > root["Execution Time"] / 2:
            return f"{slowest.get('Constraint Name') or slowest['Trigger Name']} ({slowest['Calls']} calls)"
    scans = []

    def walk(node):
        if "Scan" in node["Node Type"] and "Relation Name" in node:
            examined = (node.get("Actual Rows", 0) + node.get("Rows Removed by Filter", 0)) * node.get("Actual Loops", 1)
            scans.append((examined, node))
        for child in node.get("Plans", []):
            walk(child)

    walk(root["Plan"])
    if not scans:
        return root["Plan"]["Node Type"]
    _, node = max(scans, key=lambda s: s[0])
    index = f" ({node['Index Name']})" if "Index Name" in node else ""
    return f"{node['Node Type']} on {node['Relation Name']}{index}"


def literal(value):
    return "NULL" if value is None else "'" + value.decode().replace("'", "''") + "'"


def explain(conn, statement, prepared):
    query, params = statement
    p = prepared[query]
    # EXPLAIN takes no bind parameters; untyped literals take the prepared statement's parameter types.
    args = ", ".join(literal(encode(params[k])) for k in p.keys)
    sql = f"EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) EXECUTE {p.name}" + (f"({args})" if args else "")
    pg = conn.pgconn
    exec_checked(pg, pg.exec_(b"BEGIN"))
    try:
        result = exec_checked(pg, pg.exec_(sql.encode()))
        return plan_summary(json.loads(result.get_value(0, 0)))
    finally:
        pg.exec_(b"ROLLBACK")


def percentile(values, q):
    ordered = sorted(values)
    return ordered[max(0, math.ceil(q * len(ordered)) - 1)]


def measure_case(container, src, scenario, statements, prepared_sql, timeout_s, n):
    clients = scenario.concurrency(n) if scenario.concurrency else 1
    conns = [connect(container.dsn, "bench") for _ in range(clients)]
    try:
        prepared = {}
        for i, query in enumerate(dict.fromkeys(q for q, _ in statements)):
            sql, keys = prepared_sql[query]
            prepared[query] = Prepared(f"q{i}", sql, keys)
        for conn in conns:
            conn.execute(f"SET statement_timeout = '{int(timeout_s * 1000)}ms'")
            for p in prepared.values():
                exec_checked(conn.pgconn, conn.pgconn.prepare(p.name.encode(), p.sql.encode()))
        missing = {k for q, params in statements for k in prepared[q].keys if k not in params}
        if missing:
            raise BenchError(f"{scenario.name}: no value for parameters {sorted(map(str, missing))}")

        def burst():
            latencies = [0.0] * clients
            errors = []

            def client(i):
                try:
                    latencies[i] = run_statements(conns[i], statements, prepared, scenario.until_empty, scenario.commit)
                except QueryError as e:
                    errors.append(e)

            threads = [threading.Thread(target=client, args=(i,)) for i in range(clients)]
            for t in threads:
                t.start()
            for t in threads:
                t.join()
            if errors:
                raise errors[0]
            if max(latencies) > timeout_s:
                raise QueryError(CANCELED, "request passed the statement timeout")
            return latencies

        try:
            for _ in range(WARMUPS):
                burst()
            conns[0].execute("SELECT pg_stat_statements_reset()")
            container.reset_peak()
            cpu0 = container.cpu_usec()
            latencies = []
            for _ in range(RUNS):
                latencies += burst()
            cpu1 = container.cpu_usec()
        except QueryError as e:
            if e.state == CANCELED:
                return {"status": "timeout"}
            raise
        requests = RUNS * clients
        stats = conns[0].execute(
            """SELECT sum(shared_blks_hit), sum(shared_blks_read), sum(temp_blks_read + temp_blks_written),
                      sum(wal_records), sum(total_plan_time)
               FROM pg_stat_statements WHERE toplevel AND query = ANY(%s)""",
            [[p.sql for p in prepared.values()]],
        ).fetchone()
        hit, read, temp, wal, plan_ms = (float(v or 0) for v in stats)
        result = {
            "status": "ok",
            "p50_ms": round(statistics.median(latencies) * 1000, 3),
            "p95_ms": round(percentile(latencies, 0.95) * 1000, 3),
            "plan_ms": round(plan_ms / requests, 3),
            "cpu_ms": round((cpu1 - cpu0) / 1000 / requests, 3),
            "shared_hit": round(hit / requests),
            "shared_read": round(read / requests),
            "temp_blocks": round(temp / requests),
            "wal_records": round(wal / requests),
            "anon_mb": container.anon_mb(),
            "plan": explain(conns[0], statements[0], prepared),
        }
        if scenario.derive:
            result["derived"] = scenario.derive(n, src, result)
        return result
    finally:
        for conn in conns:
            conn.close()


def run_size(container, src, scenario, n, admin):
    admin.execute("DROP DATABASE IF EXISTS bench WITH (FORCE)")
    admin.execute("CREATE DATABASE bench")
    with connect(container.dsn, "bench") as db:
        with db.transaction():
            for migration in src.migrations():
                db.execute(goose_up(migration))
            db.execute("CREATE EXTENSION pg_stat_statements")
        started = time.monotonic()
        with db.transaction():
            # Seeding writes rows in bulk and consistent by construction; replica mode skips per-row triggers.
            db.execute("SET LOCAL session_replication_role = replica")
            db.execute(BASE_SEED)
            db.execute(scenario.seed(n))
        db.execute("VACUUM (ANALYZE)")
        db.execute("CHECKPOINT")
        seed_s = time.monotonic() - started
        cases = scenario.cases(n, src)
    queries = src.queries()
    timeout_s = src.go_const("api/config.go", "defaultDatabaseStatementTimeout")
    results = []
    for case, statements in cases.items():
        prepared_sql = {q: bind(queries[q]) for q, _ in statements if q in queries}
        unknown = {q for q, _ in statements} - prepared_sql.keys()
        if unknown:
            raise BenchError(f"{scenario.name}: {src.label} has no query {sorted(unknown)}")
        result = measure_case(container, src, scenario, statements, prepared_sql, timeout_s, n)
        results.append({"ref": src.label, "commit": src.commit, "size": n, "case": case, "seed_s": round(seed_s, 1)} | result)
        summary = f"{result['p50_ms']} ms p50, {result['plan']}" if result["status"] == "ok" else result["status"]
        log(f"  {src.label} {scenario.name} n={n} {case}: {summary} (seeded in {seed_s:.0f} s)")
    return results


# --- Output ---------------------------------------------------------------------------------------

COLUMNS = (
    ("p50_ms", "p50 ms"), ("p95_ms", "p95 ms"), ("plan_ms", "plan ms"), ("cpu_ms", "CPU ms"),
    ("shared_hit", "hit"), ("shared_read", "read"), ("temp_blocks", "temp"), ("wal_records", "WAL"),
    ("anon_mb", "anon MB"), ("plan", "plan"),
)


def fmt(value):
    if isinstance(value, float):
        return f"{value:.3g}" if abs(value) < 100 else f"{value:,.0f}"
    if isinstance(value, int):
        return f"{value:,}"
    return str(value)


def size_label(n):
    for unit, div in (("M", 1_000_000), ("k", 1_000)):
        if n >= div and n % div == 0:
            return f"{n // div}{unit}"
    return str(n)


def markdown(scenario, results, refs):
    """One table: a row per size and case, each cell `before → after` when comparing two refs."""
    by_key = {}
    for r in results:
        by_key.setdefault((r["size"], r["case"]), {})[r["ref"]] = r
    derived = list(dict.fromkeys(k for r in results for k in r.get("derived", {})))
    header = ["N", "case", *(title for _, title in COLUMNS), *derived]
    lines = [
        f"#### {scenario.name} (#{scenario.issue})",
        "",
        f"N: {scenario.size}. " + (f"{scenario.note} " if scenario.note else "") + "Ref: " + " → ".join(f"`{r}`" for r in refs) + ".",
        "",
        "| " + " | ".join(header) + " |",
        "|" + "---|" * len(header),
    ]
    for (size, case), per_ref in sorted(by_key.items(), key=lambda kv: kv[0][0]):
        cells = [size_label(size), case]
        for key in [k for k, _ in COLUMNS] + derived:
            values = []
            for ref in refs:
                r = per_ref.get(ref)
                if r is None:
                    values.append("–")
                elif r["status"] != "ok":
                    values.append(r["status"])
                else:
                    values.append(fmt(r.get("derived", {}).get(key, r.get(key))))
            cells.append(" → ".join(values) if len(set(values)) > 1 or key == "plan" and len(refs) > 1 else values[0])
        lines.append("| " + " | ".join(cells) + " |")
    return "\n".join(lines)


def machine_markdown(m):
    return (
        f"Machine: {m['cpu']}, {m['cores']} cores, {m['ram_gb']} GB RAM, {m['os']}. "
        f"Postgres `{m['image'].split('@')[0]}` in Docker with `{m['docker_limits']}`. "
        f"{WARMUPS} warm-ups and {RUNS} runs per row; hit, read and temp are 8 KiB blocks per request."
    )


# --- CLI ------------------------------------------------------------------------------------------


def parse_sizes(text):
    try:
        sizes = tuple(int(s.replace("_", "")) for s in text.split(","))
    except ValueError:
        raise argparse.ArgumentTypeError(f"not a comma-separated list of integers: {text}")
    if any(s < 1 for s in sizes):
        raise argparse.ArgumentTypeError("sizes must be positive")
    return sizes


def estimate_s(scenarios, sizes, refs):
    total = 0
    for s in scenarios:
        for n in sizes or s.sizes:
            total += SETUP_S + s.rows(n) / SEED_ROWS_PER_S
    return total * refs


def main(argv=None):
    parser = argparse.ArgumentParser(description="Measure the cost of the API's Postgres queries as rows grow.")
    parser.add_argument("scenario", help="a scenario name, `all`, or `list` to print them")
    refs = parser.add_mutually_exclusive_group()
    refs.add_argument("--ref", help="read migrations and queries at this git ref (default: the working tree)")
    refs.add_argument("--compare", nargs=2, metavar=("BEFORE", "AFTER"), help="run both refs and show them side by side")
    parser.add_argument("--sizes", type=parse_sizes, help="comma-separated row counts (default: per scenario)")
    args = parser.parse_args(argv)

    if args.scenario == "list":
        for s in SCENARIOS.values():
            print(f"{s.name:20} #{s.issue}  N = {s.size}")
        return 0
    if args.scenario == "all":
        scenarios = list(SCENARIOS.values())
    elif args.scenario in SCENARIOS:
        scenarios = [SCENARIOS[args.scenario]]
    else:
        parser.error(f"unknown scenario {args.scenario}; run `list`")
    sources = [Source(r) for r in args.compare] if args.compare else [Source(args.ref)]
    labels = [s.label for s in sources]
    if len(set(labels)) != len(labels):
        parser.error("--compare needs two different refs")

    estimate = estimate_s(scenarios, args.sizes, len(sources))
    log(f"estimated runtime: {estimate / 60:.0f} min for seeding, plus query time; a size that times out adds up to {WARMUPS + RUNS} x the statement timeout")

    out = Path(tempfile.gettempdir()) / LABEL / datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    out.mkdir(parents=True)
    container = Container(postgres_image())
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(143))
    started = time.monotonic()
    try:
        container.start()
        with connect(container.dsn, "postgres") as admin:
            info = machine(container, admin.execute("SHOW server_version").fetchone()[0])
            report = {"machine": info, "refs": {s.label: s.commit for s in sources}, "scenarios": {}}
            for scenario in scenarios:
                results = []
                for src in sources:
                    for n in args.sizes or scenario.sizes:
                        size_results = run_size(container, src, scenario, n, admin)
                        results += size_results
                        if any(r["status"] == "timeout" for r in size_results):
                            log(f"  {src.label} {scenario.name}: timed out at n={n}; skipping larger sizes")
                            break
                report["scenarios"][scenario.name] = {
                    "issue": scenario.issue, "size": scenario.size, "note": scenario.note, "results": results,
                }
                (out / "results.json").write_text(json.dumps(report, indent=2, default=str))
    finally:
        container.stop()
    report["runtime_s"] = round(time.monotonic() - started)
    (out / "results.json").write_text(json.dumps(report, indent=2, default=str))
    tables = [machine_markdown(info)] + [
        markdown(SCENARIOS[name], s["results"], labels) for name, s in report["scenarios"].items()
    ]
    text = "\n\n".join(tables) + "\n"
    (out / "results.md").write_text(text)
    print(text)
    log(f"took {report['runtime_s'] / 60:.1f} min; wrote {out / 'results.md'} and {out / 'results.json'}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except BenchError as e:
        log(f"error: {e}")
        sys.exit(1)
