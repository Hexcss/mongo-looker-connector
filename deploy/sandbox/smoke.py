#!/usr/bin/env python3
"""Exercise the private gateway as a Cloud Run invoker using the sandbox fixture."""
import json
import os
import subprocess
import urllib.error
import urllib.request

project = os.environ["PROJECT_ID"]
account = os.environ["OPERATOR_ACCOUNT"]
region = os.environ.get("TF_VAR_region", "europe-west1")
database = "mlc-sandbox"


def gcloud(*args):
    return subprocess.check_output(
        ["gcloud", *args, f"--project={project}", f"--account={account}"], text=True).strip()


url = gcloud("run", "services", "describe", "mlc-sandbox", f"--region={region}", "--format=value(status.url)")
if not url.startswith("https://") or not url.endswith(".run.app"):
    raise SystemExit("Cloud Run did not return the service's HTTPS run.app URL.")
try:
    urllib.request.urlopen(url + "/health", timeout=30)
except urllib.error.HTTPError as err:
    if err.code not in (401, 403):
        raise
else:
    raise SystemExit("Cloud Run accepted an anonymous request.")

token = gcloud("auth", "print-identity-token")


def fetch(path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url + path, data=data, headers={
        "Authorization": "Bearer " + token, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=65) as response:
        return json.load(response)


def check(actual, expected, label):
    if actual != expected:
        raise SystemExit(f"{label}: expected {expected!r}, received {actual!r}")


check(fetch("/health"), {"status": "ok"}, "health")
version = fetch("/version")
if not version.get("version") or not version.get("revision"):
    raise SystemExit("Missing build metadata.")
for key in ("version", "revision"):
    expected = os.environ.get("EXPECTED_" + key.upper())
    if expected:
        check(version[key], expected, key)
check(fetch("/api/v1/connections"), {"connections": ["demo"]}, "connections")
check(fetch("/api/v1/connections/demo/databases"), {"databases": [database]}, "databases")
check(fetch(f"/api/v1/collections?connectionId=demo&database={database}"),
      {"collections": ["orders"]}, "collections")
report = {"connectionId": "demo", "database": database, "collection": "orders",
          "pipeline": [{"$sort": {"_id": 1}}], "fields": ["_id", "amount"]}
schema = fetch("/api/v1/schema", report)["fields"]
check({field["name"]: field["type"] for field in schema}, {"_id": "TEXT", "amount": "NUMBER"}, "schema")
check(fetch("/api/v1/query", report), {
    "fields": ["_id", "amount"], "rows": [["order-001", 42], ["order-002", 17]]}, "report")
print(f"Verified private service, version {version['version']} ({version['revision']}), discovery, schema and both report rows.")
