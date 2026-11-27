#!/usr/bin/env python3
"""Open a browser session to the IAM-private sandbox using the selected gcloud user."""
import base64
import hmac
import http.client
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import re
import secrets
import subprocess
import threading
import time


def _gcloud(project, account, *args):
    return subprocess.run(
        ["gcloud", *args, f"--account={account}", f"--project={project}", "--quiet"],
        check=True, stdout=subprocess.PIPE, text=True, timeout=60,
    ).stdout.strip()


def service_url(project, account, region="europe-west1"):
    url = _gcloud(project, account, "run", "services", "describe", "mlc-sandbox",
                  f"--region={region}", "--format=value(status.url)")
    if not re.fullmatch(r"https://[a-z0-9-]+(?:\.[a-z0-9-]+)*\.run\.app", url):
        raise ValueError("Cloud Run did not return an HTTPS run.app service URL.")
    return url


def admin_tokens(project, account, region="europe-west1"):
    """Return (Cloud Run user ID token, signed console assertion). Never log either."""
    if not re.fullmatch(r"[a-z][a-z0-9-]{4,28}[a-z0-9]", project):
        raise ValueError("Provide the Google Cloud project ID.")
    user_id_token = _gcloud(project, account, "auth", "print-identity-token")
    access_token = _gcloud(project, account, "auth", "print-access-token")
    email = f"mlc-console-login@{project}.iam.gserviceaccount.com"
    audience = f"https://mlc-admin.invalid/projects/{project}/locations/{region}/services/mlc-sandbox"
    conn = http.client.HTTPSConnection("iamcredentials.googleapis.com", timeout=30)
    try:
        conn.request("POST", f"/v1/projects/-/serviceAccounts/{email}:generateIdToken",
                     body=json.dumps({"audience": audience, "includeEmail": True}),
                     headers={"Authorization": "Bearer " + access_token, "Content-Type": "application/json"})
        response = conn.getresponse()
        if response.status != HTTPStatus.OK:
            raise RuntimeError(f"Google declined console login (HTTP {response.status}). Check admin_members and the selected account.")
        return user_id_token, json.loads(response.read(65536))["token"]
    finally:
        conn.close()


def _expiry(token):
    # Used only for refresh timing. Google and the gateway independently authenticate the tokens.
    payload = token.split(".")[1]
    return int(json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))["exp"])


class ConsoleServer(ThreadingHTTPServer):
    def __init__(self, upstream, mint):
        super().__init__(("127.0.0.1", 0), ConsoleHandler)
        self.upstream = upstream
        self.mint = mint
        self.lock = threading.Lock()
        self.refresh_at = 0
        self.entry_path = "/open/" + secrets.token_urlsafe(32)
        self.cookie_name = "mlc_console_" + secrets.token_hex(8)
        self.cookie_value = secrets.token_urlsafe(32)
        self.expires = time.monotonic() + 3600
        # Cookies are scoped to hostnames across all ports. Give each session its own host.
        self.host = f"mlc-{secrets.token_hex(16)}.localhost:{self.server_port}"

    def tokens(self):
        with self.lock:
            if time.time() >= self.refresh_at:
                self.tokens_cached = self.mint()
                self.refresh_at = min(_expiry(t) for t in self.tokens_cached) - 60
            return self.tokens_cached

    def consume_entry(self, path):
        with self.lock:
            if self.entry_path and self.entry_path == path:
                self.entry_path = None
                return True
            return False


# Accept only the embedded UI and known administration/report routes. Paths are forwarded verbatim.
ID = r"[A-Za-z0-9_-]{1,128}"
ROUTES = {
    "GET": re.compile(r"/(?:health|version|admin/(?:console\.(?:css|js)|icons\.svg)?|api/v1/(?:admin/(?:connections|datasets|tokens)|connections(?:/" + ID + r"/databases)?|collections))"),
    "POST": re.compile(r"/api/v1/(?:admin/(?:connections|datasets|tokens|preview)|schema|query)"),
    "PUT": re.compile(r"/api/v1/admin/(?:connections|datasets)/" + ID),
    "DELETE": re.compile(r"/api/v1/admin/(?:connections|datasets|tokens)/" + ID),
}


class ConsoleHandler(BaseHTTPRequestHandler):
    timeout = 15

    def log_message(self, *args):
        # HTTP request paths can contain a single-use login link.
        pass

    def end_headers(self):
        self.send_header("Cache-Control", "no-store")
        self.send_header("Referrer-Policy", "no-referrer")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("X-Frame-Options", "DENY")
        self.send_header("Cross-Origin-Resource-Policy", "same-origin")
        super().end_headers()

    def serve(self):
        server = self.server
        origins = self.headers.get_all("Origin", [])
        sites = self.headers.get_all("Sec-Fetch-Site", [])
        origin = "http://" + server.host
        if (self.headers.get_all("Host") != [server.host] or
                origins not in ([], [origin]) or sites not in ([], ["same-origin"], ["none"])):
            return self.send_error(403)
        if time.monotonic() >= server.expires:
            return self.send_error(403, "Console session expired")
        if self.command == "GET" and server.consume_entry(self.path):
            self.send_response(303)
            self.send_header("Set-Cookie", f"{server.cookie_name}={server.cookie_value}; Path=/; HttpOnly; SameSite=Strict; Max-Age=3600")
            self.send_header("Location", "/admin/")
            self.end_headers()
            return
        cookies = [part.strip() for value in self.headers.get_all("Cookie", []) for part in value.split(";")]
        if not any(hmac.compare_digest(part.encode(), f"{server.cookie_name}={server.cookie_value}".encode()) for part in cookies):
            return self.send_error(403, "Open the login link from the launcher")
        path, sep, _query = self.path.partition("?")
        if not ROUTES[self.command].fullmatch(path) or (sep and path != "/api/v1/collections"):
            return self.send_error(404)
        body = None
        headers = {}
        if self.headers.get_all("Transfer-Encoding"):
            return self.send_error(400)
        lengths = self.headers.get_all("Content-Length", [])
        if len(lengths) > 1 or (lengths and not re.fullmatch(r"[0-9]{1,9}", lengths[0])):
            return self.send_error(400)
        size = int(lengths[0]) if lengths else 0
        if size > 256 * 1024:
            return self.send_error(413)
        if self.command != "GET":
            if (origins != [origin] or self.headers.get_all("X-MLC-Request") != ["console"] or
                    self.headers.get_all("Content-Type") != ["application/json"]):
                return self.send_error(403)
            body = self.rfile.read(size)
            if len(body) != size:
                return self.send_error(400)
            headers.update({"Content-Type": "application/json", "X-MLC-Request": "console"})
        elif size:
            return self.send_error(400)

        upstream = http.client.HTTPSConnection(server.upstream, timeout=65)
        try:
            user_token, assertion = server.tokens()
            headers.update({"X-Serverless-Authorization": "Bearer " + user_token,
                            "X-MLC-Admin-Assertion": assertion})
            upstream.request(self.command, self.path, body=body, headers=headers)
            result = upstream.getresponse()
            if 300 <= result.status < 400:
                return self.send_error(502, "Unexpected gateway redirect")
            response_body = result.read(8 * 1024 * 1024 + 1)
            if len(response_body) > 8 * 1024 * 1024:
                return self.send_error(502, "Gateway response too large")
            self.send_response(result.status)
            for key in ("Content-Type", "Content-Security-Policy"):
                if result.getheader(key):
                    self.send_header(key, result.getheader(key))
            self.send_header("Content-Length", str(len(response_body)))
            self.end_headers()
            self.wfile.write(response_body)
        except (OSError, http.client.HTTPException, subprocess.SubprocessError, RuntimeError, ValueError, KeyError, IndexError):
            self.send_error(502, "Could not authenticate or reach the private gateway")
        finally:
            upstream.close()

    do_GET = do_POST = do_PUT = do_DELETE = serve


def main():
    project = os.environ["PROJECT_ID"]
    account = os.environ["OPERATOR_ACCOUNT"]
    region = os.environ.get("TF_VAR_region", "europe-west1")
    upstream = service_url(project, account, region).removeprefix("https://")
    with ConsoleServer(upstream, lambda: admin_tokens(project, account, region)) as server:
        server.tokens()
        print(f"Open http://{server.host}{server.entry_path}", flush=True)
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            pass


if __name__ == "__main__":
    main()
