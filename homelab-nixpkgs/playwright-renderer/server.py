import json
import socket
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from playwright.sync_api import sync_playwright

PORT = 3000


class HTTPServerV6(HTTPServer):
    address_family = socket.AF_INET6  # Binding [::] accepts IPv4 too (via mapped addrs) on Linux


class RenderHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        print(f"[{self.address_string()}] {format % args}", file=sys.stderr, flush=True)

    def send_error_json(self, code, message):
        body = json.dumps({"error": message}).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        if self.path != "/render":
            self.send_error_json(404, "not found - use POST /render")
            return

        content_length = int(self.headers.get("Content-Length", 0))
        if content_length == 0:
            self.send_error_json(400, "missing request body")
            return

        try:
            body = json.loads(self.rfile.read(content_length))
        except json.JSONDecodeError as e:
            self.send_error_json(400, f"invalid JSON: {e}")
            return

        html = body.get("html")
        if not html:
            self.send_error_json(400, "missing 'html' field")
            return

        width = int(body.get("width", 1280))
        height = int(body.get("height", 720))

        try:
            with sync_playwright() as p:
                browser = p.chromium.launch(args=[
                    "--no-sandbox",
                    "--disable-setuid-sandbox",
                    "--disable-gpu",
                    "--headless=new",
                ])
                page = browser.new_page(viewport={"width": width, "height": height})
                page.set_content(html, wait_until="load")
                png = page.screenshot(full_page=False)
                browser.close()
        except Exception as e:
            self.send_error_json(500, f"render failed: {e}")
            return

        self.send_response(200)
        self.send_header("Content-Type", "image/png")
        self.send_header("Content-Length", str(len(png)))
        self.end_headers()
        self.wfile.write(png)

    def do_GET(self):
        if self.path == "/health":
            body = b'{"status":"ok"}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_error_json(404, "not found")


if __name__ == "__main__":
    server = HTTPServerV6(("::", PORT), RenderHandler)
    print(f"playwright-renderer listening on :{PORT}", flush=True)
    server.serve_forever()
