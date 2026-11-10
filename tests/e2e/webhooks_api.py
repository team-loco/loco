import argparse
import atexit
import base64
import contextlib
import hashlib
import hmac
import http.server
import importlib.util
import io
import json
import os
import queue
import threading
from pathlib import Path

repo = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(description="Send real webhook deliveries from a throwaway API to a local receiver")
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
os.chdir(repo)
spec = importlib.util.spec_from_file_location("evidence", repo / ".agents/skills/ui-evidence/scripts/evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
if args.state is None:
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        evidence.cmd_up(argparse.Namespace(ref="HEAD", name="webhooks", scenario=str(repo / "tests/e2e/fixtures/webhooks/scenario.json")))
    args.state = output.getvalue().strip().splitlines()[-1]
    print("Preview state:", args.state, flush=True)
    if not args.keep:
        atexit.register(lambda: evidence.stop_state(evidence.load_state(args.state)))
state = evidence.load_state(args.state)
token = state["users"]["owner"]["token"]
workspace = state["workspaces"]["acme/shared"]
DELIVERY_TIMEOUT_S = 30


def call(method, body):
    return evidence.rpc(state["api_url"], token, method, body)


class Receiver(http.server.BaseHTTPRequestHandler):
    received = queue.Queue()

    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        self.received.put({"path": self.path, "headers": dict(self.headers), "body": body})
        self.send_response(204)
        self.end_headers()

    def log_message(self, format, *args):
        return


server = http.server.ThreadingHTTPServer((evidence.HOST, 0), Receiver)
threading.Thread(target=server.serve_forever, daemon=True).start()
base_url = f"http://{evidence.HOST}:{server.server_address[1]}"


def receive(path):
    request = Receiver.received.get(timeout=DELIVERY_TIMEOUT_S)
    assert request["path"] == path, (request["path"], path)
    return request


def signatures(secret, request):
    key = base64.b64decode(secret.removeprefix("whsec_"))
    headers = {name.lower(): value for name, value in request["headers"].items()}
    signed = f"{headers['webhook-id']}.{headers['webhook-timestamp']}.".encode() + request["body"]
    expected = "v1," + base64.b64encode(hmac.new(key, signed, hashlib.sha256).digest()).decode()
    return expected, headers["webhook-signature"].split(" ")


def assert_signed(secret, request):
    expected, sent = signatures(secret, request)
    assert expected in sent, ("signature does not verify", sent)


def touch_workspace(description):
    call("loco.workspace.v1.WorkspaceService/UpdateWorkspace", {"workspaceId": workspace, "description": description})


created = call("loco.webhook.v1.WebhookService/CreateWebhook", {"workspaceId": workspace, "url": f"{base_url}/first", "eventTypes": ["workspace.updated"]})
hook = created["webhook"]["id"]
secret = created["secret"]
touch_workspace("first")
first = receive("/first")
assert json.loads(first["body"])["type"] == "workspace.updated"
assert_signed(secret, first)

updated = call("loco.webhook.v1.WebhookService/UpdateWebhook", {"workspaceId": workspace, "webhookId": hook, "url": f"{base_url}/second", "eventTypes": ["workspace.updated"]})
assert updated["webhook"]["url"] == f"{base_url}/second"
touch_workspace("second")
assert_signed(secret, receive("/second"))

server.shutdown()
print("PASS: webhook deliveries", flush=True)
