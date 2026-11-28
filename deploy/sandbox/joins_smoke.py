#!/usr/bin/env python3
"""Exercise an authorized indexed join and its cached dataset on the seeded sandbox."""
import json
import os
import secrets
from contextlib import ExitStack

from console import _gcloud
from report_smoke import request, sandbox_clients


def verify():
    host, transport, admin = sandbox_clients()
    project, account = os.environ["PROJECT_ID"], os.environ["OPERATOR_ACCOUNT"]
    region = os.getenv("TF_VAR_region", "europe-west1")
    database, foreign = "mlc-sandbox", "shipping_zones"
    info = json.loads(_gcloud(project, account, "firestore", "databases", "describe",
                              "--database=" + database, "--format=json"))
    uri = (f"mongodb://{info['uid']}.{region}.firestore.goog:443/{database}"
           "?loadBalanced=true&tls=true&retryWrites=false&authMechanism=MONGODB-OIDC"
           "&authMechanismProperties=ENVIRONMENT:gcp,TOKEN_RESOURCE:FIRESTORE")
    name = "join-check-" + secrets.token_hex(6)
    source = {"connectionId": name, "database": database, "collection": "orders"}
    pipeline = [{"$match": {"amount": 42}},
                {"$lookup": {"from": foreign, "localField": "_id", "foreignField": "_id", "as": "zone"}},
                {"$unwind": "$zone"},
                {"$project": {"_id": 0, "amount": 1, "region": "$zone.region"}}]
    body = dict(source, pipeline=pipeline)
    fields = [{"name": "amount", "type": "NUMBER"}, {"name": "region", "type": "TEXT"}]

    def operate(method, path, body=None, status=200):
        return request(host, method, "/api/v1/admin/" + path, admin, body, status)

    with ExitStack() as cleanup:
        record = operate("POST", "connections", {"id": name, "uri": uri, "databases": {database: ["orders", foreign]}}, 201)
        cleanup.callback(lambda: operate("DELETE", "connections/" + name, {"revision": record["revision"]}, 204))

        def issue(grants):
            token = operate("POST", "tokens", {"label": name, "grants": grants}, 201)
            cleanup.callback(operate, "DELETE", "tokens/" + token["token"]["id"], None, 204)
            return {**transport, "X-MLC-Token": token["secret"]}

        both = issue([source, dict(source, collection=foreign)])
        only_orders = issue([source])
        assert operate("POST", "workspace/preview", body) == {"fields": fields, "rows": [[42, "EU-South"]]}
        plan = operate("POST", "workspace/explain", body)
        assert type(plan["documentsExamined"]) is int and plan["documentsExamined"] > 0, plan
        assert plan["indexesUsed"], plan
        assert plan["rowLimit"] == 50, plan
        print("Joined order-001 to EU-South for 42. Provider execution metrics:", json.dumps(plan), flush=True)
        for endpoint in ("schema", "query"):
            raw = body if endpoint == "schema" else dict(body, fields=["amount", "region"])
            result = request(host, "POST", "/api/v1/" + endpoint, both, raw)
            assert result == ({"fields": fields} if endpoint == "schema" else {"fields": ["amount", "region"], "rows": [[42, "EU-South"]]})
            request(host, "POST", "/api/v1/" + endpoint, only_orders, raw, expected=(403, 404))
        request(host, "POST", "/api/v1/admin/workspace/explain", both, body, expected=403)

        published = operate("POST", "workspace/open", dict(body, id=name), 201)
        dataset = next(d for d in operate("GET", "datasets")["datasets"] if d["id"] == name)
        cleanup.callback(operate, "DELETE", "datasets/" + name, {"revision": dataset["revision"]}, 204)
        assert published["datasetId"] == name and dataset["fields"] == fields
        reader = issue([{"datasetId": name, "fields": ["region"]}])
        ns = {"connectionId": "@datasets", "database": "published", "collection": name}
        restricted = dict(ns, fields=["region"])
        for cache in ("MISS", "HIT"):
            assert request(host, "POST", "/api/v1/query", reader, restricted, cache=cache)["rows"] == [["EU-South"]]
        request(host, "POST", "/api/v1/query", reader, dict(ns, fields=["amount"]), 400)

        record = operate("PUT", "connections/" + name, {"revision": record["revision"], "databases": {database: ["orders"]}})
        for path in ("workspace/preview", "workspace/explain"):
            operate("POST", path, body, (403, 404))
        request(host, "POST", "/api/v1/query", both, dict(body, fields=["region"]), expected=(403, 404))
        for endpoint, query in (("schema", ns), ("query", restricted)):
            request(host, "POST", "/api/v1/" + endpoint, reader, query, expected=(403, 404))
    print("Verified raw join grants, field-restricted publication, cache isolation and immediate denial after the joined collection was removed.")


if __name__ == "__main__":
    verify()
