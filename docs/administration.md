# Administration console

The console runs inside the gateway image. In the sandbox, Google Cloud IAM authenticates the caller before the request reaches Cloud Run. Console actions additionally require a Google-signed assertion for this deployment. Only the configured administrators receive permission to mint it.

## Open the hosted console

Enable `hosted_console` in the gateway Terraform stack to deploy a separate HTTPS browser service. Its stable address is the `hosted_console_url` output. Bookmark that address to return to an active session. To sign in from your workstation, run the following once with an allowlisted gcloud account.

```sh
PROJECT_ID=your-project-id OPERATOR_ACCOUNT=your-account python3 deploy/sandbox/hosted_console.py
```

The helper opens the hosted site in your browser and exits. To use another browser, add `--print-url` and open the single-use link within five minutes. Treat that link as a credential. The private gateway stores hashes of the ticket and session, validates the session when serving requests, and retains control of administration and reporting. Return to the hosted site root to sign out. If you receive an HTTP 401 or 403, mint a fresh sign-in link using the account in the administrator allowlist.

## Open the console through loopback

Sign in to gcloud with an account listed in the deployment's `admin_members`. From a checkout of the release, run the launcher and open the single-use address it prints.

```sh
export PROJECT_ID='your-project-id'
export OPERATOR_ACCOUNT='your-allowlisted-account'
python3 deploy/sandbox/console.py
```

Keep the launcher running while you work. It binds to loopback, exchanges the initial link for an HTTP-only cookie scoped to a random `.localhost` hostname, and attaches Google credentials server-side. The session lasts one hour. Restart the launcher for a new session. See the [Terraform guide](../deploy/terraform/README.md) to configure the allowlist.

## Manage a connection

Open **Connections** and add a stable connection ID, a read-only MongoDB URI, and the databases and collections to expose. Use TLS for managed connections. The gateway accepts MongoDB SRV connections, SCRAM over TLS, and the Firestore Enterprise workload OIDC connection form. Network routes and database permissions must already allow the gateway to reach the source.

The gateway stores configuration in its dedicated Firestore Native database and credentials in pinned Secret Manager versions. The saved URI never returns to the browser. During an edit, leave the URI blank to keep it. Supplying a replacement invalidates existing token grants for that connection. The old secret version has a 15-minute grace period for in-flight reads. On an active gateway, a background cleanup retires superseded versions; scale-to-zero can delay physical destruction. A concurrent edit returns a conflict so you can refresh before applying changes. Connections provisioned through environment variables or Terraform are visible in the console and edited at their provisioning source.

## Preview a query

Choose a connection, database, and collection in **Query preview**. Enter an aggregation pipeline as a JSON array. Leave the fields blank for schema inference, or choose fields in the order you want to see. Preview returns up to 50 rows using the gateway's read-only aggregation engine. Each operator is checked before execution. [Supported operators and BSON conversions](query.md)

## Issue and revoke reporting tokens

In **Access tokens**, give the token a label and select the connection, database, and collection for every grant. Copy the secret when it is issued. The gateway persists its hash and verifies the token and current namespace grants on each reporting request. Revocation takes effect on the next request, including on another gateway instance. Recreating a managed connection with a previously used ID does not revive old grants.

In an admin-enabled private deployment, a reporting client needs **both** a Cloud Run invoker identity and a scoped reporting token. Send the Google ID token in `X-Serverless-Authorization` and the reporting credential in `Authorization`. Supply both through your client’s secret store. This example expects the two credentials to be available in the calling shell.

```sh
curl --fail-with-body \
  --header "X-Serverless-Authorization: Bearer ${CLOUD_RUN_ID_TOKEN}" \
  --header "Authorization: Bearer ${REPORTING_TOKEN}" \
  "${GATEWAY_URL}/api/v1/connections"
```

With administration enabled, IAM alone grants access to health and version information. Reporting endpoints require an administrator assertion or a valid reporting token. A token cannot access administration endpoints. Deployments that omit the administration configuration keep the v0.1 reporting interface.

## HTTP operations

All admin API routes use `/api/v1/admin`. For writes and preview, send JSON and the header `X-MLC-Request: console`.

| Method | Path | Request or result |
| --- | --- | --- |
| GET | `/connections` | List IDs, namespaces, management source, and revision |
| POST | `/connections` | Create with `id`, `databases`, and `uri` |
| PUT | `/connections/{id}` | Supply the current `revision`, updated `databases`, and optionally a replacement `uri` |
| DELETE | `/connections/{id}` | Supply the current `revision` |
| POST | `/preview` | Use the [query request](api.md) with optional output fields |
| GET | `/tokens` | List public metadata, concrete grants, and revocation status |
| POST | `/tokens` | Supply `label` and `grants` with `connectionId`, `database`, and `collection`; the response includes the one-time secret |
| DELETE | `/tokens/{id}` | Revoke the token |

The synthetic [sandbox verification](../deploy/sandbox/smoke.py) covers creation, two kinds of edits, preview, issuance, use, rejection outside scope, revocation, and deletion. Run it with `VERIFY_ADMIN=1` on the default sandbox fixture.
