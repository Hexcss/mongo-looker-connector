"""Loopback integration checks with a fake Cloud Run upstream; requires no Google credentials."""
import base64
import http.client
import json
import threading
import time
import unittest
from unittest.mock import MagicMock, patch

import console


class ConsoleSessionTest(unittest.TestCase):
    def setUp(self):
        payload = base64.urlsafe_b64encode(json.dumps({"exp": int(time.time()) + 3600}).encode()).decode()
        self.proof = "header." + payload + ".signature"
        self.mint = MagicMock(return_value=(self.proof, self.proof))
        self.server = console.ConsoleServer("fixture.run.app", self.mint)
        thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)

    def local(self, path, method="GET", headers=None, body=None):
        conn = http.client.HTTPConnection(*self.server.server_address, timeout=3)
        try:
            conn.request(method, path, body, {"Host": self.server.host, **(headers or {})})
            response = conn.getresponse()
            response.read()
            return response
        finally:
            conn.close()

    def test_single_use_login_and_proxy_boundaries(self):
        self.assertEqual(self.server.server_address[0], "127.0.0.1")
        host, port = self.server.host.split(":")
        self.assertRegex(host, r"^[a-z0-9-]+\.localhost$")
        self.assertEqual(int(port), self.server.server_port)
        with console.ConsoleServer("fixture.run.app", self.mint) as other:
            self.assertNotEqual(host, other.host.split(":")[0])
        entry = self.server.entry_path
        for headers in ({"Host": "other.example.org"}, {"Origin": "https://other.example.org"},
                        {"Sec-Fetch-Site": "cross-site"}):
            self.assertEqual(self.local(entry, headers=headers).status, 403)
        response = self.local(entry)
        self.assertEqual(response.status, 303)
        self.assertEqual(response.getheader("Location"), "/admin/")
        cookie = response.getheader("Set-Cookie")
        self.assertIn("HttpOnly", cookie)
        self.assertIn("SameSite=Strict", cookie)
        self.assertNotIn("domain=", cookie.lower())
        self.assertEqual(self.local(entry).status, 403)
        self.assertEqual(self.local("/api/v1/admin/tokens").status, 403)

        session = {"Cookie": cookie.split(";")[0]}
        mutation = {**session, "Origin": "http://" + self.server.host,
                    "X-MLC-Request": "console", "Content-Type": "application/json"}
        for field, invalid in (("Origin", "null"), ("Sec-Fetch-Site", "same-site"),
                               ("Content-Type", "text/plain"), ("X-MLC-Request", "")):
            self.assertEqual(self.local("/api/v1/admin/tokens", "POST", {**mutation, field: invalid}, "{}").status, 403)
        self.assertEqual(self.local("/api/v1/admin/tokens", "POST", session, "{}").status, 403)
        self.assertEqual(self.local("/unlisted", headers=session).status, 404)
        self.mint.assert_not_called()

        with patch.object(console.http.client, "HTTPSConnection") as remote:
            conn = remote.return_value
            result = conn.getresponse.return_value
            result.status = 200
            result.getheader.return_value = None
            result.read.return_value = b"{}"
            res = self.local("/api/v1/admin/tokens", "POST", {
                **mutation, "Authorization": "local", "X-MLC-Token": "local", "X-MLC-Admin-Assertion": "local",
            }, "{}")
            self.assertEqual(res.status, 200)
            self.assertIsNone(res.getheader("Access-Control-Allow-Origin"))
            self.assertEqual(res.getheader("Cache-Control"), "no-store")
            conn.request.assert_called_once_with("POST", "/api/v1/admin/tokens", body=b"{}", headers={
                "Content-Type": "application/json", "X-MLC-Request": "console",
                "X-Serverless-Authorization": "Bearer " + self.proof, "X-MLC-Admin-Assertion": self.proof,
            })
            for method, path in (("GET", "datasets"), ("POST", "datasets"),
                                 ("PUT", "datasets/sales"), ("DELETE", "datasets/sales")):
                endpoint = "/api/v1/admin/" + path
                self.assertEqual(self.local(endpoint, method, mutation, None if method == "GET" else "{}").status, 200)
                self.assertEqual(conn.request.call_args.args, (method, endpoint))
            result.status = 302
            self.assertEqual(self.local("/admin/", headers=session).status, 502)
            self.mint.assert_called_once()
            self.server.refresh_at = 0
            self.assertEqual(self.local("/admin/", headers=session).status, 502)
            self.assertEqual(self.mint.call_count, 2)
            self.server.expires = 0
            self.assertEqual(self.local("/admin/", headers=session).status, 403)


if __name__ == "__main__":
    unittest.main()
