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
import time
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
FAILING_PATH = "/failing"


def call(method, body):
    return evidence.rpc(state["api_url"], token, method, body)


class Receiver(http.server.BaseHTTPRequestHandler):
    received = queue.Queue()

    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        self.received.put({"path": self.path, "headers": dict(self.headers), "body": body})
        self.send_response(503 if self.path == FAILING_PATH else 204)
        self.end_headers()

    def log_message(self, format, *args):
        return


server = http.server.ThreadingHTTPServer((evidence.HOST, 0), Receiver)
threading.Thread(target=server.serve_forever, daemon=True).start()
base_url = f"http://{evidence.HOST}:{server.server_address[1]}"


def receive(path, match=lambda request: True):
    deadline = time.monotonic() + DELIVERY_TIMEOUT_S
    while True:
        request = Receiver.received.get(timeout=max(deadline - time.monotonic(), 0.1))
        if request["path"] == path and match(request):
            return request


def header(request, name):
    return {key.lower(): value for key, value in request["headers"].items()}[name]


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

def deliveries(**filters):
    return call("loco.webhook.v1.WebhookService/ListWebhookDeliveries", {"workspaceId": workspace, "webhookId": hook, **filters})


def wait_for_delivery(match):
    deadline = time.monotonic() + DELIVERY_TIMEOUT_S
    while time.monotonic() < deadline:
        found = [d for d in deliveries().get("deliveries", []) if match(d)]
        if found:
            return found[0]
        time.sleep(0.5)
    raise AssertionError("no matching delivery recorded")


call("loco.webhook.v1.WebhookService/UpdateWebhook", {"workspaceId": workspace, "webhookId": hook, "url": base_url + FAILING_PATH, "eventTypes": ["workspace.updated"]})
touch_workspace("failing")
receive(FAILING_PATH)
failing = wait_for_delivery(lambda d: d.get("lastError") == "endpoint returned 503 Service Unavailable")
assert failing["status"] == "pending", failing
pending = deliveries(status="pending")["deliveries"]
assert [d["id"] for d in pending] == [failing["id"]], pending
assert failing["id"] not in [d["id"] for d in deliveries(status="succeeded")["deliveries"]]
first_page = deliveries(pageSize=1)
second_page = deliveries(pageSize=1, pageToken=first_page["nextPageToken"])
assert first_page["deliveries"][0]["id"] == failing["id"] and second_page["deliveries"][0]["id"] != failing["id"]

call("loco.webhook.v1.WebhookService/UpdateWebhook", {"workspaceId": workspace, "webhookId": hook, "url": f"{base_url}/second", "eventTypes": ["workspace.updated"]})
try:
    call("loco.webhook.v1.WebhookService/RedeliverWebhookDelivery", {"workspaceId": workspace, "webhookId": hook, "deliveryId": failing["id"]})
except evidence.RPCError as error:
    assert error.code == "failed_precondition", error.code
else:
    raise AssertionError("a pending delivery was redelivered")
succeeded = deliveries(status="succeeded")["deliveries"][-1]
call("loco.webhook.v1.WebhookService/RedeliverWebhookDelivery", {"workspaceId": workspace, "webhookId": hook, "deliveryId": succeeded["id"]})
redelivered = receive("/second", lambda request: header(request, "webhook-id") == succeeded["id"])
assert_signed(secret, redelivered)
assert json.loads(first["body"]) == json.loads(redelivered["body"])

call("loco.webhook.v1.WebhookService/SendTestEvent", {"workspaceId": workspace, "webhookId": hook})
ping = receive("/second", lambda request: json.loads(request["body"])["type"] == "webhook.test")
assert_signed(secret, ping)
assert json.loads(ping["body"])["subject"] == {"type": "webhook", "id": hook}


def workspace_update(request):
    return json.loads(request["body"])["type"] == "workspace.updated"


rotated = call("loco.webhook.v1.WebhookService/RotateWebhookSecret", {"workspaceId": workspace, "webhookId": hook, "gracePeriodSeconds": 3600})
assert rotated["secret"] != secret and rotated["webhook"]["previousSecretExpiresAt"], rotated
touch_workspace("rotating")
during = receive("/second", workspace_update)
assert_signed(secret, during)
assert_signed(rotated["secret"], during)
final = call("loco.webhook.v1.WebhookService/RotateWebhookSecret", {"workspaceId": workspace, "webhookId": hook})
assert "previousSecretExpiresAt" not in final["webhook"], final
touch_workspace("rotated")
after = receive("/second", workspace_update)
assert_signed(final["secret"], after)
replaced, sent = signatures(rotated["secret"], after)
assert replaced not in sent, sent

server.shutdown()
print("PASS: webhook deliveries", flush=True)
