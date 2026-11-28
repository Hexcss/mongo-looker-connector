#!/usr/bin/env python3
"""Verify saved workspace queries across the private gateway and public reporting gateway."""
import json
import os
import secrets
from contextlib import ExitStack
from urllib.parse import parse_qs, quote, urlsplit

from console import _gcloud
from report_smoke import request, sandbox_clients


def verify():
    host, _, admin = sandbox_clients()
    origin = _gcloud(os.environ["PROJECT_ID"], os.environ["OPERATOR_ACCOUNT"], "run", "services",
                     "describe", "mlc-console", "--region=" + os.getenv("TF_VAR_region", "europe-west1"),
                     "--format=value(status.url)")
    public = urlsplit(origin).netloc
    root = "/api/v1/admin/"
    name = "workbench-" + secrets.token_hex(6)
    fields = [{"name": "order", "type": "TEXT"}, {"name": "total", "type": "NUMBER"}]
    query = {"connectionId": "demo", "database": "mlc-sandbox", "collection": "orders", "pipeline": [
        {"$sort": {"amount": -1, "_id": 1}}, {"$limit": 1},
        {"$project": {"_id": 0, "order": "$_id", "total": "$amount"}}]}

    def call(method, path, body=None, code=200):
        return request(host, method, root + path, admin, body, code)

    with ExitStack() as cleanup:
        assert call("POST", "workspace/preview", query) == {"fields": fields, "rows": [["order-001", 42]]}
        result = call("POST", "workspace/open", dict(query, id=name), 201)
        record = next(d for d in call("GET", "datasets")["datasets"] if d["id"] == name)
        cleanup.callback(call, "DELETE", "datasets/" + name, {"revision": record["revision"]}, 204)
        assert record["fields"] == fields and record["pipeline"] == query["pipeline"]
        assert call("GET", "workspace/looker/" + name) == result
        assert result["datasetId"] == name
        dest = urlsplit(result["url"])
        params = parse_qs(dest.query, strict_parsing=True)
        assert dest.scheme == "https" and dest.netloc == "lookerstudio.google.com" and dest.path == "/datasources/create"
        assert set(params) == {"connectorId", "connectorConfig"}
        assert params["connectorId"] == [os.environ["EXPECTED_CONNECTOR_ID"]]
        assert json.loads(params["connectorConfig"][0]) == {
            "collection": name, "connectionId": "%40datasets", "gatewayOrigin": quote(origin, safe="")}
        call("POST", "workspace/open", dict(query, id=name), 409)
        issued = call("POST", "tokens", {"label": name, "grants": [{"datasetId": name, "fields": ["total"]}]}, 201)
        token_path = "tokens/" + issued["token"]["id"]
        revoke = cleanup.callback(call, "DELETE", token_path, None, 204)
        headers = {"X-MLC-Token": issued["secret"]}
        namespace = {"connectionId": "@datasets", "database": "published", "collection": name}
        request(public, "GET", "/api/v1/connections", {}, expected=401)
        assert request(public, "GET", "/api/v1/connections", headers) == {"connections": ["@datasets"]}
        assert request(public, "POST", "/api/v1/schema", headers, namespace) == {"fields": fields[1:]}
        body = dict(namespace, fields=["total"], pipeline=[{"$sort": {"_id": 1}}])
        for cache in ("MISS", "HIT"):
            assert request(public, "POST", "/api/v1/query", headers, body, cache=cache)["rows"] == [[42]]
        request(public, "POST", "/api/v1/query", headers, dict(body, fields=["order"]), 400)
        revoke("DELETE", token_path, None, 204)
        request(public, "POST", "/api/v1/query", headers, body, 401)
    print("Verified typed preview, durable publication, preconfigured Looker URL, scoped public reporting, cache and revocation.")


if __name__ == "__main__":
    verify()
