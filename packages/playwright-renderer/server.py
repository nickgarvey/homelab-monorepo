import json
import os
import socket
import ssl
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from playwright.sync_api import sync_playwright

PORT = int(os.environ.get("PORT", "443"))
# tls.crt, tls.key and ca.crt, as cert-manager writes them into a Secret.
TLS_DIR = os.environ.get("TLS_DIR", "/tls")
# SPIFFE IDs whose client certificates may render, comma-separated.
ALLOWED_CLIENTS = {c for c in os.environ.get("ALLOWED_CLIENTS", "").split(",") if c}
HANDSHAKE_TIMEOUT = 10


def tls_context():
    # Built per connection, so certificate renewals apply without a restart.
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.minimum_version = ssl.TLSVersion.TLSv1_2
    ctx.load_cert_chain(f"{TLS_DIR}/tls.crt", f"{TLS_DIR}/tls.key")
    ctx.load_verify_locations(f"{TLS_DIR}/ca.crt")
    ctx.verify_mode = ssl.CERT_REQUIRED
    return ctx


class HTTPServerV6(HTTPServer):
    address_family = socket.AF_INET6  # Binding [::] accepts IPv4 too (via mapped addrs) on Linux

    def get_request(self):
        # Mutual TLS: the client certificate must chain to ca.crt and carry an
        # allowed SPIFFE ID. An OSError here drops the connection unanswered.
        sock, addr = super().get_request()
        sock.settimeout(HANDSHAKE_TIMEOUT)
        # A connection closed before sending anything is the kubelet's TCP
        # probe: drop it without logging.
        try:
            first = sock.recv(1, socket.MSG_PEEK)
        except OSError:
            first = b""
        if not first:
            sock.close()
            raise OSError("closed before the handshake")
        try:
            conn = tls_context().wrap_socket(sock, server_side=True)
        except OSError as e:
            sock.close()
            print(f"[{addr[0]}] TLS handshake failed: {e}", file=sys.stderr, flush=True)
            raise
        ids = [v for k, v in conn.getpeercert().get("subjectAltName", ()) if k == "URI"]
        if not ALLOWED_CLIENTS.intersection(ids):
            conn.close()
            print(f"[{addr[0]}] client {ids} not allowed", file=sys.stderr, flush=True)
            raise OSError("client not allowed")
        return conn, addr


class RenderHandler(BaseHTTPRequestHandler):
    timeout = 30

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
    if not ALLOWED_CLIENTS:
        sys.exit("ALLOWED_CLIENTS is empty, so no client could render")
    server = HTTPServerV6(("::", PORT), RenderHandler)
    print(f"playwright-renderer listening on :{PORT}", flush=True)
    server.serve_forever()
