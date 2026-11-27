#!/usr/bin/env python3
"""Prepare Apps Script source for one private Cloud Run reporting gateway."""
import argparse
import json
import re
import shutil
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--gateway-url", required=True)
parser.add_argument("--invoker", required=True)
parser.add_argument("--output", type=Path, required=True)
args = parser.parse_args()
url = args.gateway_url.rstrip("/")
if not re.fullmatch(r"https://(?:[a-z0-9-]+\.)+run\.app", url):
    parser.error("Use the HTTPS run.app service URL from Terraform.")
if not re.fullmatch(r"[a-z][a-z0-9-]+@[a-z0-9-]+\.iam\.gserviceaccount\.com", args.invoker):
    parser.error("Use the reporting service account email from Terraform.")
src = Path(__file__).resolve().parent
out = args.output
out.mkdir(parents=True, exist_ok=True)
if out.resolve() == src:
    parser.error("Use a separate installation directory.")
manifest = json.loads((src / "appsscript.json").read_text())
manifest["urlFetchWhitelist"] = [
    url + "/api/v1/",
    "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/" + args.invoker + ":generateIdToken",
]
(out / "appsscript.json").write_text(json.dumps(manifest, indent=2) + "\n")
(out / "Installation.gs").write_text(
    "const MLC_GATEWAY_URL = " + json.dumps(url) + ";\n" +
    "const MLC_INVOKER = " + json.dumps(args.invoker) + ";\n"
)
shutil.copyfile(src / "Code.gs", out / "Code.gs")
