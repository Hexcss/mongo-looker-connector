#!/usr/bin/env python3
"""Verify a private gateway. Enable the full console lifecycle with VERIFY_ADMIN=1."""
import json
import os
import secrets
import subprocess
import urllib.error
import urllib.request

project = os.environ["PROJECT_ID"]
account = os.environ["OPERATOR_ACCOUNT"]
region = os.environ.get("TF_VAR_region", "europe-west1")
database = "mlc-sandbox"
verify_admin = os.environ.get("VERIFY_ADMIN") == "1"


def gcloud(*args):
    return subprocess.check_output(
        ["gcloud", *args, f"--project={project}", f"--account={account}"], text=True).strip()


url = gcloud("run", "services", "describe", "mlc-sandbox", f"--region={region}", "--format=value(status.url)")
if not url.startswith("https://") or not url.endswith(".run.app"):
    raise SystemExit("Cloud Run did not return a run.app service URL.")


def request(path, method="GET", body=None, headers=None, expected=200):
    headers = dict(headers or {})
    data = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(body).encode()
    req = urllib.request.Request(url + path, data=data, method=method, headers=headers)
    try:
        response = urllib.request.urlopen(req, timeout=65)
    except urllib.error.HTTPError as err:
        response = err
    with response:
        if response.status not in (expected if isinstance(expected, tuple) else (expected,)):
            raise RuntimeError(f"{method} {path} returned HTTP {response.status}, expected {expected}")
        payload = response.read()
        return json.loads(payload) if payload and response.headers.get_content_type() == "application/json" else payload


def check(actual, expected, label):
    if actual != expected:
        raise RuntimeError(f"{label} did not match the expected fixture")


request("/health", expected=(401, 403))
if verify_admin:
    from console import admin_tokens
    iam, proof = admin_tokens(project, account, region)
    transport = {"X-Serverless-Authorization": "Bearer " + iam}
    admin = {**transport, "X-MLC-Admin-Assertion": proof, "X-MLC-Request": "console"}
else:
    transport = {"Authorization": "Bearer " + gcloud("auth", "print-identity-token")}
    admin = transport

check(request("/health", headers=transport), {"status": "ok"}, "health")
version = request("/version", headers=transport)
for key in ("version", "revision"):
    if not version.get(key):
        raise RuntimeError("Missing build metadata")
    if os.environ.get("EXPECTED_" + key.upper()):
        check(version[key], os.environ["EXPECTED_" + key.upper()], key)

base = "/api/v1"
fixture = {"connectionId": "demo", "database": database, "collection": "orders",
           "pipeline": [{"$sort": {"_id": 1}}], "fields": ["_id", "amount"]}
expected_rows = {"fields": ["_id", "amount"], "rows": [["order-001", 42], ["order-002", 17]]}
if "demo" not in request(base + "/connections", headers=admin)["connections"]:
    raise RuntimeError("Missing configured fixture")
check(request(base + "/connections/demo/databases", headers=admin), {"databases": [database]}, "databases")
check(request(base + "/collections?connectionId=demo&database=" + database, headers=admin),
      {"collections": ["orders"]}, "collections")
schema = request(base + "/schema", "POST", fixture, admin)["fields"]
check({v["name"]: v["type"] for v in schema}, {"_id": "TEXT", "amount": "NUMBER"}, "schema")
check(request(base + "/query", "POST", fixture, admin), expected_rows, "fixture query")


def verify_console():
    request(base + "/connections", headers=transport, expected=401)
    request(base + "/admin/connections", headers=transport, expected=401)
    html = request("/admin/", headers=admin)
    if b'id="root"' not in html:
        raise RuntimeError("The deployed React shell is unavailable")
    connections = base + "/admin/connections"
    tokens = base + "/admin/tokens"
    info = json.loads(gcloud("firestore", "databases", "describe", "--database=" + database, "--format=json"))
    uri = (f"mongodb://{info['uid']}.{region}.firestore.goog:443/{database}"
           "?loadBalanced=true&tls=true&retryWrites=false&authMechanism=MONGODB-OIDC"
           "&authMechanismProperties=ENVIRONMENT:gcp,TOKEN_RESOURCE:FIRESTORE")
    name = "verify-" + secrets.token_hex(6)
    namespaces = {database: ["orders"]}
    grant = {"connectionId": name, "database": database, "collection": "orders"}
    managed = dict(fixture, connectionId=name)
    record = None
    issued = []

    def issue():
        out = request(tokens, "POST", {"label": name, "grants": [grant]}, admin, 201)
        issued.append(out["token"]["id"])
        return {**transport, "Authorization": "Bearer " + out["secret"]}

    try:
        record = request(connections, "POST", {"id": name, "uri": uri, "databases": namespaces}, admin, 201)
        check(record["managed"], True, "managed flag")
        if set(record) - {"id", "managed", "revision", "databases"}:
            raise RuntimeError("Unexpected fields in connection response")
        path = connections + "/" + name
        request(path, "PUT", {"revision": "stale", "databases": namespaces}, admin, 409)
        preview = dict(managed)
        del preview["fields"]
        check(request(base + "/admin/preview", "POST", preview, admin), expected_rows, "inferred preview")

        scoped = issue()
        check(request(base + "/connections", headers=scoped), {"connections": [name]}, "scoped discovery")
        check(request(base + "/query", "POST", managed, scoped), expected_rows, "scoped query")
        request(base + "/query", "POST", fixture, scoped, 404)
        request(connections, headers=scoped, expected=403)
        record = request(path, "PUT", {"revision": record["revision"], "databases": namespaces}, admin)
        check(request(base + "/query", "POST", managed, scoped), expected_rows, "metadata edit")
        record = request(path, "PUT", {"revision": record["revision"], "uri": uri, "databases": namespaces}, admin)
        request(base + "/query", "POST", managed, scoped, 404)
        replacement = issue()
        check(request(base + "/query", "POST", managed, replacement), expected_rows, "rotated credential")
        request(tokens + "/" + issued[-1], "DELETE", headers=admin, expected=204)
        request(base + "/connections", headers=replacement, expected=401)
        issued.pop()
        request(path, "DELETE", {"revision": record["revision"]}, admin, 204)
        record = None
        if any(item["id"] == name for item in request(connections, headers=admin)["connections"]):
            raise RuntimeError("Deleted connection remains visible")
    finally:
        for token_id in issued:
            request(tokens + "/" + token_id, "DELETE", headers=admin, expected=204)
        if record:
            request(connections + "/" + name, "DELETE", {"revision": record["revision"]}, admin, 204)


if verify_admin:
    verify_console()
    print("Verified console access, managed credential lifecycle, preview, namespace isolation, and token revocation.")
print(f"Verified private gateway {version['version']} ({version['revision']}), discovery, schema and both report rows.")
