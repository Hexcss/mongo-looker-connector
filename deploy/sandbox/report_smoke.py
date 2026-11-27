#!/usr/bin/env python3
"""Verify scoped, date-filtered reporting with the real sandbox reporting identity."""
import http.client
import json
import os
import secrets

from console import _gcloud, admin_tokens, service_url


def request(host, method, path, headers, body=None, expected=200):
    conn = http.client.HTTPSConnection(host, timeout=65)
    try:
        conn.request(method, path, body=json.dumps(body) if body is not None else None,
                     headers={**headers, "Content-Type": "application/json"})
        response = conn.getresponse()
        if response.status != expected:
            raise RuntimeError(f"{method} {path} returned HTTP {response.status}, expected {expected}")
        return json.loads(response.read()) if expected in (200, 201) else None
    finally:
        conn.close()


def main():
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
    request(host, "GET", "/api/v1/connections", transport, expected=401)

    operator, proof = admin_tokens(project, account, region)
    admin = {"X-Serverless-Authorization": "Bearer " + operator, "X-MLC-Admin-Assertion": proof,
             "X-MLC-Request": "console"}
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
        request(host, "POST", "/api/v1/query", reporting, {**query, "collection": "ungranted"}, expected=404)
        request(host, "GET", tokens, reporting, expected=403)
    finally:
        request(host, "DELETE", tokens + "/" + issued["token"]["id"], admin, expected=204)
    request(host, "GET", "/api/v1/connections", reporting, expected=401)
    print("Verified reporting identity, single-day order, field order, scope enforcement and revocation.")


if __name__ == "__main__":
    main()
