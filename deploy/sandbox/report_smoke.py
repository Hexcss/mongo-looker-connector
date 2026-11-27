#!/usr/bin/env python3
"""Verify scoped, date-filtered reporting with the real sandbox reporting identity."""
import http.client
import json
import os
import secrets

from console import _gcloud, admin_tokens, service_url


def request(host, method, path, headers, body=None, expected=200, cache=None):
    conn = http.client.HTTPSConnection(host, timeout=65)
    try:
        conn.request(method, path, body=json.dumps(body) if body is not None else None,
                     headers={**headers, "Content-Type": "application/json"})
        response = conn.getresponse()
        allowed = expected if isinstance(expected, tuple) else (expected,)
        if response.status not in allowed:
            raise RuntimeError(f"{method} {path} returned HTTP {response.status}, expected {expected}")
        disposition = response.getheader("X-MLC-Cache")
        if cache is not None and disposition != cache:
            raise RuntimeError(f"{method} {path} returned cache status {disposition!r}, expected {cache}")
        if response.status >= 400 and disposition == "HIT":
            raise RuntimeError("Rejected request was served from cache")
        return json.loads(response.read()) if response.status in (200, 201) else None
    finally:
        conn.close()


def sandbox_clients():
    project = os.environ["PROJECT_ID"]
    account = os.environ["OPERATOR_ACCOUNT"]
    region = os.environ.get("TF_VAR_region", "europe-west1")
    url = service_url(project, account, region)
    host = url.removeprefix("https://")
    reporting_sa = f"mlc-reporting@{project}.iam.gserviceaccount.com"
    bearer = _gcloud(project, account, "auth", "print-access-token")
    identity = request("iamcredentials.googleapis.com", "POST",
                       f"/v1/projects/-/serviceAccounts/{reporting_sa}:generateIdToken",
                       {"Authorization": "Bearer " + bearer}, {"audience": url, "includeEmail": True})
    transport = {"X-Serverless-Authorization": "Bearer " + identity["token"]}
    operator, proof = admin_tokens(project, account, region)
    admin = {"X-Serverless-Authorization": "Bearer " + operator, "X-MLC-Admin-Assertion": proof,
             "X-MLC-Request": "console"}
    return host, transport, admin


def main():
    host, transport, admin = sandbox_clients()
    request(host, "GET", "/api/v1/connections", transport, expected=401)
    tokens = "/api/v1/admin/tokens"
    grant = {"connectionId": "demo", "database": "mlc-sandbox", "collection": "orders"}
    issued = request(host, "POST", tokens, admin, {
        "label": "verify-report-" + secrets.token_hex(8), "grants": [grant]}, expected=201)
    try:
        reporting = {**transport, "Authorization": "Bearer " + issued["secret"]}
        fields = ["orderedAt", "amount", "_id"]
        query = {**grant, "fields": fields, "pipeline": [],
                 "dateRange": {"field": "orderedAt", "startDate": "2026-11-02", "endDate": "2026-11-02"}}
        rows = request(host, "POST", "/api/v1/query", reporting, query)
        if rows != {"fields": fields, "rows": [["20261102153000", 17, "order-002"]]}:
            raise RuntimeError("Unexpected dated fixture or field order")
        pipeline = [{"$sort": {"amount": -1, "_id": 1}}, {"$limit": 1},
                    {"$project": {"_id": 0, "orderId": "$_id", "sales": "$amount"}}]
        custom = {**grant, "pipeline": pipeline}
        schema = request(host, "POST", "/api/v1/schema", reporting, custom)
        if schema["fields"] != [{"name": "orderId", "type": "TEXT"}, {"name": "sales", "type": "NUMBER"}]:
            raise RuntimeError("Custom pipeline returned an unexpected output schema")
        for dates, expected in (({}, ["order-001", 42]), ({"dateRange": query["dateRange"]}, ["order-002", 17])):
            rows = request(host, "POST", "/api/v1/query", reporting, {**custom, **dates, "fields": ["orderId", "sales"]})
            if rows != {"fields": ["orderId", "sales"], "rows": [expected]}:
                raise RuntimeError("Unexpected largest order from the custom pipeline")
        request(host, "POST", "/api/v1/query", reporting, {**query, "collection": "ungranted"}, expected=404)
        request(host, "GET", tokens, reporting, expected=403)
    finally:
        request(host, "DELETE", tokens + "/" + issued["token"]["id"], admin, expected=204)
    request(host, "GET", "/api/v1/connections", reporting, expected=401)
    print("Verified reporting identity, single-day order, field order, scope enforcement and revocation.")
    print("Verified custom aggregation output fields and largest order with and without a source date range.")


if __name__ == "__main__":
    main()
