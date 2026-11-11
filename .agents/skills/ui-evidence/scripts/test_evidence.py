import json
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Event, Thread

from playwright.sync_api import sync_playwright

import evidence


PAGE = b"""<!doctype html>
<html>
<button hidden>Show output</button>
<script>
document.documentElement.classList.toggle("dark", localStorage.getItem("loco:mode:v1") === "dark");
const button = document.querySelector("button");
fetch("/stream").then(async response => {
  await response.body.getReader().read();
  button.hidden = false;
});
button.onclick = () => setTimeout(() => {
  const output = document.createElement("p");
  output.id = "output";
  output.textContent = "Live output";
  document.body.append(output);
}, 500);
</script>
</html>
"""


def test_capture_waits_for_selector_with_open_stream(tmp_path):
    stop = Event()

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            if self.path == "/stream":
                self.send_header("Content-Type", "application/octet-stream")
                self.end_headers()
                self.wfile.write(b"live output\n")
                self.wfile.flush()
                stop.wait()
            else:
                self.send_header("Content-Type", "text/html")
                self.end_headers()
                self.wfile.write(PAGE)

    with ThreadingHTTPServer(("127.0.0.1", 0), Handler) as server:
        thread = Thread(target=server.serve_forever)
        thread.start()
        try:
            state = {"ui_url": f"http://127.0.0.1:{server.server_port}", "users": {"user": {"token": "fixture"}}}
            shot = {
                "name": "live-output",
                "user": "user",
                "path": "/",
                "steps": [{"click": "button"}],
                "ready_selector": "#output",
                "crop": {"selector": "#output"},
            }
            target = tmp_path / "live-output.png"
            with sync_playwright() as playwright, playwright.chromium.launch() as browser:
                evidence.capture(browser, state, evidence.validate_spec({"shots": [shot]})[0], "dark", False, target)
            assert target.read_bytes().startswith(b"\x89PNG\r\n\x1a\n")
        finally:
            stop.set()
            server.shutdown()
            thread.join()


def test_serve_ui_serves_this_runs_addresses_from_a_shared_build(tmp_path):
    (tmp_path / "index.html").write_text("app")
    (tmp_path / "runtime-config.js").write_text("window.locoConfig = {};\n")
    handler = lambda *a, **kw: evidence.UIHandler(*a, directory=str(tmp_path), api_url="http://127.0.0.1:9", **kw)
    with evidence.UIServer(("127.0.0.1", 0), handler) as server:
        thread = Thread(target=server.serve_forever)
        thread.start()
        try:
            origin = f"http://127.0.0.1:{server.server_port}"
            fetch = lambda path: urllib.request.urlopen(origin + path, timeout=5)
            with fetch("/runtime-config.js") as response:
                assert response.headers["Content-Type"] == "text/javascript"
                assert response.read() == b'window.locoConfig = {"apiURL": "http://127.0.0.1:9"};\n'
            with fetch(evidence.DISCOVERY_PATH) as response:
                assert json.loads(response.read())["issuer"] == origin + "/dex"
            with fetch("/org/a/wks/b") as response:
                assert response.read() == b"app"
        finally:
            server.shutdown()
            thread.join()
