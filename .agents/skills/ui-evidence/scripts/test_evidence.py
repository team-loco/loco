from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Event, Thread

from playwright.sync_api import sync_playwright

import evidence


PAGE = b"""<!doctype html>
<html>
<button hidden>Show output</button>
<script>
document.documentElement.classList.toggle("dark", localStorage.getItem("loco:theme:v1") === "dark");
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
