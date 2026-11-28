#!/usr/bin/env python3
"""Verify named sign-in and permission changes on the hosted sandbox console."""
from http import client, cookies
import json
import os
import re
import secrets

from console import _gcloud, admin_tokens, service_url
from report_smoke import request


def verify():
    project, operator = os.environ["PROJECT_ID"], os.environ["OPERATOR_ACCOUNT"]
    region = os.getenv("TF_VAR_region", "europe-west1")
    gateway = service_url(project, operator, region).removeprefix("https://")
    origin = _gcloud(project, operator, "run", "services", "describe", "mlc-console",
                     f"--region={region}", "--format=value(status.url)")
    if not re.fullmatch(r"https://[a-z0-9-]+(?:\.[a-z0-9-]+)*\.run\.app", origin):
        raise RuntimeError("Cloud Run returned an invalid console URL.")
    public = origin.removeprefix("https://")
    iam, proof = admin_tokens(project, operator, region)
    admin_headers = {"X-Serverless-Authorization": "Bearer " + iam, "X-MLC-Admin-Assertion": proof,
                     "X-MLC-Request": "console"}
    base = "/api/v1/admin/"
    email = "smoke-" + secrets.token_hex(10) + "@example.invalid"

    def login(password, expected=204):
        conn = client.HTTPSConnection(public, timeout=30)
        try:
            conn.request("POST", "/session/login", body=json.dumps({"email": email, "password": password}),
                         headers={"Origin": origin, "Content-Type": "application/json"})
            response = conn.getresponse()
            if response.status != expected:
                raise RuntimeError(f"Password sign-in returned HTTP {response.status}, expected {expected}.")
            if expected == 204:
                jar = cookies.SimpleCookie(response.getheader("Set-Cookie", ""))
                ticket = jar["__Host-mlc-session"]
                if not (ticket["httponly"] and ticket["secure"] and ticket["samesite"].lower() == "strict"):
                    raise RuntimeError("Invalid session cookie attributes.")
                return ticket.key + "=" + ticket.value
        finally:
            conn.close()

    def console(session, method, path, body=None, code=200):
        return request(public, method, base + path,
                       {"Origin": origin, "X-MLC-Request": "console", "Cookie": session}, body, code)

    created = request(gateway, "POST", base + "users", admin_headers, {"email": email, "role": "viewer"}, 201)
    member = base + "users/" + created["user"]["id"]
    temporary = created["temporaryPassword"]
    password = secrets.token_urlsafe(24)
    try:
        session = login(temporary)
        assert console(session, "GET", "me")["mustChangePassword"]
        console(session, "GET", "datasets", code=403)
        console(session, "POST", "password", {"currentPassword": temporary, "newPassword": password}, 204)
        console(session, "GET", "me", code=401)
        login(temporary, 401)
        session = login(password)
        assert not console(session, "GET", "me")["mustChangePassword"]
        assert all(set(d) == {"id"} for d in console(session, "GET", "datasets")["datasets"])
        for path in ["users", "connections", "tokens"]:
            console(session, "GET", path, code=403)
        request(gateway, "PUT", member, admin_headers, {"role": "editor", "disabled": False})
        console(session, "GET", "me", code=401)
        session = login(password)
        assert console(session, "GET", "me")["role"] == "editor"
        assert any(c["id"] == "demo" for c in console(session, "GET", "connections")["connections"])
        result = console(session, "POST", "workspace/preview", {
            "connectionId": "demo", "database": "mlc-sandbox", "collection": "orders",
            "pipeline": [{"$sort": {"amount": -1, "_id": 1}}, {"$limit": 1}, {"$project": {"_id": 0, "amount": 1}}]})
        assert result["rows"] == [[42]]
        console(session, "POST", "tokens", {}, 403)
        reset = request(gateway, "POST", member + "/reset-password", admin_headers, {})
        console(session, "GET", "me", code=401)
        login(password, 401)
        session = login(reset["temporaryPassword"])
        assert console(session, "GET", "me")["mustChangePassword"]
    finally:
        request(gateway, "PUT", member, admin_headers, {"role": "viewer", "disabled": True})
    console(session, "GET", "me", code=401)
    login(reset["temporaryPassword"], 401)
    print("Verified temporary-password change, viewer restrictions, editor preview, password reset, disablement and session revocation.")


if __name__ == "__main__":
    verify()
