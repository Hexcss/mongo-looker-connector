#!/usr/bin/env python3
"""Upsert the two synthetic orders into the named Terraform sandbox."""
import json
import os
import subprocess

from pymongo import MongoClient

project = os.environ["PROJECT_ID"]
account = os.environ["OPERATOR_ACCOUNT"]
region = os.environ.get("TF_VAR_region", "europe-west1")
database = "mlc-sandbox"
flags = [f"--project={project}", f"--account={account}"]


def gcloud(*args):
    return subprocess.check_output(["gcloud", *args, *flags], text=True).strip()


info = json.loads(gcloud("firestore", "databases", "describe", f"--database={database}", "--format=json"))
if (info["name"] != f"projects/{project}/databases/{database}"
        or info["locationId"] != region or info["databaseEdition"] != "ENTERPRISE"
        or info["mongodbCompatibleDataAccessMode"] != "DATA_ACCESS_MODE_ENABLED"):
    raise SystemExit("Selected database does not match the expected sandbox configuration.")

uri = (f"mongodb://{info['uid']}.{region}.firestore.goog:443/{database}"
       "?loadBalanced=true&tls=true&retryWrites=false&authMechanism=PLAIN&authSource=$external")
orders = [{"_id": "order-001", "amount": 42}, {"_id": "order-002", "amount": 17}]
with MongoClient(uri, username="access_token", password=gcloud("auth", "print-access-token"),
                 serverSelectionTimeoutMS=15000, timeoutMS=30000) as client:
    collection = client[database]["orders"]
    for order in orders:
        collection.replace_one({"_id": order["_id"]}, order, upsert=True)
    actual = list(collection.find({"_id": {"$in": [order["_id"] for order in orders]}}).sort("_id", 1))
    if actual != orders:
        raise SystemExit("Synthetic fixture read-back did not match the expected rows.")
print(f"Verified two synthetic orders in {project}/{database}.")
