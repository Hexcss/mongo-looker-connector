#!/usr/bin/env python3
"""Provision the first console owner using the sandbox operator's Google identity."""
import argparse
import http.client
import json
import os
from pathlib import Path
import re

from console import admin_tokens, service_url


def bootstrap(project, operator, region, email, output):
    upstream = service_url(project, operator, region)
    iam, proof = admin_tokens(project, operator, region)
    headers = {"X-Serverless-Authorization": "Bearer " + iam, "X-MLC-Admin-Assertion": proof,
               "Content-Type": "application/json", "X-MLC-Request": "console"}

    def request(method, path, body=None):
        conn = http.client.HTTPSConnection(upstream.removeprefix("https://"), timeout=30)
        try:
            conn.request(method, "/api/v1/admin/" + path, body=body, headers=headers)
            response = conn.getresponse()
            if response.status not in (200, 201):
                raise RuntimeError(f"Gateway returned HTTP {response.status}.")
            return json.loads(response.read())
        finally:
            conn.close()

    if any(user.get("primary") for user in request("GET", "users")["users"]):
        return False
    # Reserve a private file before creating a credential that is returned once.
    output = Path(output).expanduser()
    output.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as dest:
            owner = request("POST", "bootstrap-owner", json.dumps({"email": email}))
            password = owner["temporaryPassword"]
            if not isinstance(password, str) or not re.fullmatch(r"[A-Za-z0-9_-]{24,72}", password):
                raise RuntimeError("Invalid gateway credential response.")
            dest.write(password + "\n")
            dest.flush()
            os.fsync(dest.fileno())
    except Exception:
        if output.stat().st_size == 0:
            output.unlink()
        raise
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--password-file", type=Path, help="new file for the generated temporary password")
    parser.add_argument("--email", help="owner's sign-in email, defaults to OPERATOR_ACCOUNT")
    args = parser.parse_args()
    project, operator = os.getenv("PROJECT_ID", ""), os.getenv("OPERATOR_ACCOUNT", "")
    if not re.fullmatch(r"[a-z][a-z0-9-]{4,28}[a-z0-9]", project) or not operator:
        parser.error("Set PROJECT_ID and OPERATOR_ACCOUNT.")
    output = args.password_file or (Path.home() / ".local/state/mongo-looker-connector" / project / "owner-password")
    try:
        created = bootstrap(project, operator, os.getenv("TF_VAR_region", "europe-west1"),
                            args.email or operator, output)
    except (OSError, ValueError, RuntimeError, KeyError) as err:
        parser.exit(1, f"{err}\n")
    if created:
        print(f"Created the primary owner. The temporary password is in {output}.")
    else:
        print("The primary owner already exists. Its password is unchanged.")


if __name__ == "__main__":
    main()
