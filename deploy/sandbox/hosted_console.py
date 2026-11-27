#!/usr/bin/env python3
"""Open a short-lived session on the hosted admin console."""
import argparse
import http.client
import json
import os
import re
import webbrowser

from console import _gcloud, admin_tokens, service_url


def login_url(project, account, region):
    url = service_url(project, account, region)
    hosted = _gcloud(project, account, "run", "services", "describe", "mlc-console",
                     f"--region={region}", "--format=value(status.url)")
    if not re.fullmatch(r"https://[a-z0-9.-]+\.run\.app", hosted):
        raise RuntimeError("The hosted service has no valid Cloud Run URL.")
    iam, proof = admin_tokens(project, account, region)
    conn = http.client.HTTPSConnection(url.removeprefix("https://"), timeout=30)
    try:
        conn.request("POST", "/api/v1/admin/hosted-ticket", body=b"{}",
                     headers={"X-Serverless-Authorization": "Bearer " + iam,
                              "X-MLC-Admin-Assertion": proof, "X-MLC-Request": "console",
                              "Content-Type": "application/json"})
        response = conn.getresponse()
        if response.status != 201:
            raise RuntimeError(f"The gateway declined hosted login (HTTP {response.status}).")
        ticket = json.loads(response.read(1024))["ticket"]
        if not re.fullmatch(r"[A-Za-z0-9_-]{43}", ticket):
            raise RuntimeError("The gateway returned an invalid ticket.")
        return hosted + "/#" + ticket
    finally:
        conn.close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--print-url", action="store_true", help="print the private single-use URL for a remote browser")
    args = parser.parse_args()
    project, account = os.environ.get("PROJECT_ID"), os.environ.get("OPERATOR_ACCOUNT")
    if not project or not account:
        parser.error("Set PROJECT_ID and OPERATOR_ACCOUNT.")
    address = login_url(project, account, os.getenv("TF_VAR_region", "europe-west1"))
    if args.print_url:
        print(address)
    elif not webbrowser.open(address, new=2):
        raise SystemExit("Could not open a browser. Run again with --print-url.")
