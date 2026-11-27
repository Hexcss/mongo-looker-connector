#!/usr/bin/env python3
"""Verify managed datasets and permission-aware cache behavior on the seeded sandbox."""
import json
import os
import secrets
import time
from contextlib import ExitStack

from console import _gcloud
from report_smoke import request, sandbox_clients

API = "/api/v1/"
ADMIN = API + "admin/"


def verify():
    host, transport, admin = sandbox_clients()
    project = os.environ["PROJECT_ID"]
    region = os.environ.get("TF_VAR_region", "europe-west1")
    database = "mlc-sandbox"
    info = json.loads(_gcloud(project, os.environ["OPERATOR_ACCOUNT"], "firestore", "databases",
                              "describe", "--database=" + database, "--format=json"))
    uri = (f"mongodb://{info['uid']}.{region}.firestore.goog:443/{database}"
           "?loadBalanced=true&tls=true&retryWrites=false&authMechanism=MONGODB-OIDC"
           "&authMechanismProperties=ENVIRONMENT:gcp,TOKEN_RESOURCE:FIRESTORE")
    name = "verify-dataset-" + secrets.token_hex(6)
    source = {"connectionId": name, "database": database, "collection": "orders"}
    namespace = {"connectionId": "@datasets", "database": "published", "collection": name}
    fields = [{"name": "orderId", "type": "TEXT"}, {"name": "sales", "type": "NUMBER"},
              {"name": "orderedAt", "type": "YEAR_MONTH_DAY_SECOND"}]
    definition = dict(source, id=name, fields=fields, pipeline=[
        {"$project": {"orderId": "$_id", "sales": "$amount", "orderedAt": 1}},
    ])
    base_query = dict(namespace, fields=["orderId"], pipeline=[{"$sort": {"_id": 1}}])
    all_ids = [["order-001"], ["order-002"]]
    datasets = ADMIN + "datasets"
    connections = ADMIN + "connections"
    tokens = ADMIN + "tokens"

    def admin_call(method, path, body=None, status=200):
        return request(host, method, path, admin, body, status)

    def query(headers, body, rows, cache):
        result = request(host, "POST", API + "query", headers, body, cache=cache)
        if result != {"fields": body["fields"], "rows": rows}:
            raise RuntimeError("Managed query did not match the expected sandbox fixture")

    with ExitStack() as cleanup:
        source_record = admin_call("POST", connections, {"id": name, "uri": uri, "databases": {database: ["orders"]}}, 201)
        cleanup.callback(lambda: admin_call("DELETE", connections + "/" + name, {"revision": source_record["revision"]}, 204))
        dataset = admin_call("POST", datasets, definition, 201)
        cleanup.callback(lambda: admin_call("DELETE", datasets + "/" + name, {"revision": dataset["revision"]}, 204))

        def issue(visible):
            item = admin_call("POST", tokens, {"label": name, "grants": [{"datasetId": name, "fields": visible}]}, 201)
            token_path = tokens + "/" + item["token"]["id"]
            cleanup.callback(admin_call, "DELETE", token_path, None, 204)
            return {**transport, "Authorization": "Bearer " + item["secret"]}, token_path

        wide, wide_path = issue(["orderId", "sales", "orderedAt"])
        limited, _ = issue(["orderId"])
        if request(host, "GET", API + "connections", limited) != {"connections": ["@datasets"]}:
            raise RuntimeError("Dataset-only discovery exposed a raw connection")
        collections = request(host, "GET", API + "collections?connectionId=%40datasets&database=published", limited)
        if collections != {"collections": [name]}:
            raise RuntimeError("Dataset discovery did not respect the token grant")
        for headers, visible in ((wide, fields), (limited, fields[:1])):
            result = request(host, "POST", API + "schema", headers, namespace)
            if result != {"fields": visible}:
                raise RuntimeError("Published schema did not match the token field grants")
        query(wide, base_query, all_ids, "MISS")
        query(wide, base_query, all_ids, "HIT")
        query(limited, base_query, all_ids, "MISS")
        query(limited, base_query, all_ids, "HIT")
        dated = dict(base_query, fields=["sales", "orderId"], dateRange={
            "field": "orderedAt", "startDate": "2026-11-02", "endDate": "2026-11-02"})
        query(wide, dated, [[17, "order-002"]], "MISS")
        query(wide, dated, [[17, "order-002"]], "HIT")
        query(wide, dict(dated, fields=["orderId", "sales"]), [["order-002", 17]], "MISS")
        query(wide, dict(base_query, filters={"sales": 42}), [["order-001"]], "MISS")
        query(wide, dict(base_query, filters={"sales": 17}), [["order-002"]], "MISS")
        for body in (dated, dict(base_query, fields=["sales"]), dict(base_query, filters={"sales": 42}),
                     dict(base_query, pipeline=[{"$project": {"hidden": "$amount"}}])):
            request(host, "POST", API + "query", limited, body, (400, 403, 404))
        request(host, "POST", API + "query", limited, dict(source, fields=["amount"]), 404)
        admin_call("DELETE", wide_path, None, 204)
        request(host, "POST", API + "query", wide, dated, 401)
        print("Verified token-isolated misses and hits, field order, filters, raw-source denial and revocation.", flush=True)

        # The sandbox has one gateway instance. Wait past the 60-second freshness window.
        time.sleep(61)
        query(limited, base_query, all_ids, "MISS")
        query(limited, base_query, all_ids, "HIT")
        admin_call("PUT", datasets + "/" + name, dict(definition, revision="stale"), 409)
        updated = dict(definition, pipeline=[{"$match": {"amount": 17}}, *definition["pipeline"]])
        dataset = admin_call("PUT", datasets + "/" + name, dict(updated, revision=dataset["revision"]))
        request(host, "POST", API + "query", limited, base_query, 404)
        refreshed, _ = issue(["orderId"])
        query(refreshed, base_query, [["order-002"]], "MISS")
        query(refreshed, base_query, [["order-002"]], "HIT")
        source_record = admin_call("PUT", connections + "/" + name, {
            "revision": source_record["revision"], "databases": {database: ["orders"]}, "uri": uri})
        request(host, "POST", API + "query", refreshed, base_query, 404)
        print("Verified TTL expiry, stale edit rejection, republish invalidation and source rotation after a cache hit.")


if __name__ == "__main__":
    verify()
