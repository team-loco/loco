import threading
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from helpers import post_otlp, run_collector, wait_for

EXPORTS = 3
OTLP_CONTENT_TYPE = "application/x-protobuf"
INGEST_HOST = "host.docker.internal"


@contextmanager
def ingest_server():
    """Accept OTLP exports on keep-alive connections, recording the client address of each."""
    connections = []

    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def do_POST(self):
            self.rfile.read(int(self.headers["Content-Length"]))
            connections.append(self.client_address)
            self.send_response(200)
            self.send_header("Content-Type", OTLP_CONTENT_TYPE)
            self.send_header("Content-Length", "0")
            self.end_headers()

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("0.0.0.0", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server.server_address[1], connections
    finally:
        server.shutdown()
        server.server_close()


def export_pipeline(relays, port):
    """The node collector's exporter, sending to port on the host, fed by an OTLP receiver."""
    config = relays["otel-col-daemon-agent"]
    exporter = dict(config["exporters"]["otlp_http"])
    exporter["endpoint"] = f"http://{INGEST_HOST}:{port}"
    exporter["sending_queue"] = {
        key: value
        for key, value in exporter["sending_queue"].items()
        if key != "storage"
    }
    return {
        "receivers": {"otlp": {"protocols": {"http": {"endpoint": "0.0.0.0:4318"}}}},
        "exporters": {"otlp_http": exporter},
        "service": {
            "pipelines": {
                "logs": {"receivers": ["otlp"], "exporters": ["otlp_http"]},
            }
        },
    }


def log_export(index):
    record = {"body": {"stringValue": f"export {index}"}}
    return {"resourceLogs": [{"scopeLogs": [{"logRecords": [record]}]}]}


def test_each_export_opens_a_new_connection(worker_relays, tmp_path):
    """A connection kept open would keep sending to the ingest address resolved when it opened."""
    with ingest_server() as (port, connections):
        config = export_pipeline(worker_relays, port)
        with run_collector(
            tmp_path,
            config,
            "otel-col-daemon",
            "--add-host",
            f"{INGEST_HOST}:host-gateway",
        ) as collector:
            for index in range(EXPORTS):
                post_otlp(collector, "logs", log_export(index))
                wait_for(collector, lambda count=index + 1: len(connections) >= count)
        assert len(set(connections)) == EXPORTS, connections
