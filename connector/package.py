#!/usr/bin/env python3
"""Prepare a Looker Studio connector for registered gateway URLs or private Cloud Run."""
import argparse
import json
import re
import shutil
from pathlib import Path
from urllib.parse import urlsplit

parser = argparse.ArgumentParser(description=__doc__)
mode = parser.add_mutually_exclusive_group(required=True)
mode.add_argument("--gateway-origin", action="append", help="Registered public HTTPS gateway, repeatable")
mode.add_argument("--gateway-url", help="Private Cloud Run origin for the IAM connector")
parser.add_argument("--invoker", help="Reporting service account for the private IAM connector")
parser.add_argument("--output", type=Path, required=True)
args = parser.parse_args()
src = Path(__file__).resolve().parent
out = args.output
if out.resolve() == src:
    parser.error("Use a separate installation directory.")
manifest = json.loads((src / "appsscript.json").read_text())
if args.gateway_origin:
    if args.invoker:
        parser.error("--invoker only applies to --gateway-url.")
    origins = []
    for value in args.gateway_origin:
        origin = value.removesuffix("/").lower()
        if not re.fullmatch(r"https://(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?::[1-9][0-9]{0,4})?", origin):
            parser.error("Register an HTTPS origin with no path, credentials, query, or fragment.")
        try:
            urlsplit(origin).port
        except ValueError:
            parser.error("Invalid gateway port.")
        if origin not in origins:
            origins.append(origin)
    manifest["urlFetchWhitelist"] = [origin + "/api/v1/" for origin in origins]
    manifest["oauthScopes"] = ["https://www.googleapis.com/auth/script.external_request"]
    manifest["dataStudio"]["authType"] = ["PATH_KEY"]
    manifest["dataStudio"]["description"] = "Connect to a reporting gateway with its HTTPS URL and a scoped reporting token. Open a saved dataset from the workspace, explore collections, or write an aggregation."
    installation = "const MLC_GATEWAY_ORIGINS = Object.freeze(" + json.dumps(origins) + ");\n"
else:
    url = args.gateway_url.rstrip("/")
    if not re.fullmatch(r"https://(?:[a-z0-9-]+\.)+run\.app", url):
        parser.error("Use the HTTPS run.app service URL from Terraform.")
    if not args.invoker or not re.fullmatch(r"[a-z][a-z0-9-]+@[a-z0-9-]+\.iam\.gserviceaccount\.com", args.invoker):
        parser.error("Use the reporting service account email from Terraform.")
    manifest["urlFetchWhitelist"] = [
        url + "/api/v1/",
        "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/" + args.invoker + ":generateIdToken",
    ]
    installation = ("const MLC_GATEWAY_URL = " + json.dumps(url) + ";\n" +
                    "const MLC_INVOKER = " + json.dumps(args.invoker) + ";\n")
out.mkdir(parents=True, exist_ok=True)
(out / "appsscript.json").write_text(json.dumps(manifest, indent=2) + "\n")
(out / "Installation.gs").write_text(installation)
shutil.copyfile(src / "Code.gs", out / "Code.gs")
