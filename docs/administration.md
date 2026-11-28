# Administration console

The hosted console serves the pipeline workspace and team settings. In the sandbox the browser service invokes the private gateway with its own Cloud Run identity. The gateway authorizes every action using the current account and role.

## Sign in to the hosted console

Open the Terraform `hosted_console_url` and sign in with the email and password supplied by your workspace owner. New accounts must replace their temporary password in **My account**, then sign in again. Console passwords are 12–72 bytes. Sessions expire after one hour. Changing a password or role, resetting a password, or disabling the account invalidates its active sessions on the next request.

### Initialize the sandbox owner

An allowlisted Google Cloud operator can create the primary owner once. Use the operator's email as the initial sign-in, or add `--email` to assign another address.

```sh
PROJECT_ID=your-project-id OPERATOR_ACCOUNT=your-account python3 deploy/sandbox/bootstrap_owner.py
```

The command saves the temporary password in a new, owner-readable file at `~/.local/state/mongo-looker-connector/PROJECT_ID/owner-password`. Choose another destination with `--password-file`. A second run preserves the existing account and password. After choosing a personal password, remove the temporary password file.

For operator recovery or setup from a browser, launch an operator session from the checkout.

```sh
PROJECT_ID=your-project-id OPERATOR_ACCOUNT=your-account python3 deploy/sandbox/hosted_console.py
```

In Cloud Shell, add `--print-url` and open the single-use link within five minutes. The gateway stores hashes of tickets and session identifiers. An expired link returns you to password sign-in. Open **Team members** to create the primary owner or reset a named account. Only an authenticated operator can reset the primary owner through this screen.

### Manage team roles

| Role | Console access |
| --- | --- |
| Owner | Manage connections, team membership, reporting tokens and datasets, including deletion. |
| Editor | Browse configured source namespaces, build and preview pipelines, publish and update datasets, and open them in Looker Studio. |
| Viewer | Browse published dataset IDs and open their Looker Studio setup links. |

In **Team members**, choose **Add member**, enter an email and select a role. Share the generated temporary password privately. To revoke console access, disable the member. The primary owner always retains the owner role. For reports in Looker Studio, each reporting token separately determines which sources and fields the connector can read.

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

## Publish a managed dataset

In **Datasets**, choose **Publish dataset**. Give the dataset a stable ID and select its source connection, database, and collection. Enter the saved pipeline as a JSON array. Use `[]` to report directly on the source documents. Under **Published schema**, add each output field in display order and choose its type. Reports use this schema even when the query returns zero rows.

For the sandbox orders fixture, select `demo / mlc-sandbox / orders`, use `[{"$sort":{"amount":-1}}]`, and publish `_id` as `TEXT` and `amount` as `NUMBER`. Open **Preview rows**, enter `{"amount":42}` under **Equality filters**, and run the dataset preview to see `order-001`. Leave the filter empty for an unfiltered preview.

A reporting token with a dataset grant sees the dataset ID under the `@datasets` connection and `published` database. Editing and republishing the definition invalidates earlier grants for that dataset. Issue replacement grants and update the reporting client. Deleting the dataset immediately removes access to it.

## Issue and revoke reporting tokens

In **Access tokens**, give the token a label and check the published fields its reports need under **Dataset field access**. To grant direct collection access, select a collection under **Source collection access**. That grant exposes every field the collection can return, even if a dataset grant on the same token restricts its published fields. Copy the secret when it is issued. The gateway persists its hash and verifies the token and current grants on each reporting request. Revocation takes effect on the next request, including on another gateway instance. Recreating a managed connection with a previously used ID does not revive old grants.

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
| GET | `/me` | Current role, account email, operator flag and password-change requirement |
| POST | `/bootstrap-owner` | Operator only. Supply `email` to initialize the primary owner and receive its temporary password. |
| GET | `/users` | List team members and their roles and status |
| POST | `/users` | Create a member with `email` and `role`; returns `user` and `temporaryPassword` |
| PUT | `/users/{id}` | Set `role` and `disabled`. Requires an owner. |
| POST | `/users/{id}/reset-password` | Supply `{}` to issue a temporary replacement |
| POST | `/password` | Supply `currentPassword` and `newPassword` for your own account |
| GET | `/connections` | List IDs, namespaces, management source, and revision |
| POST | `/connections` | Create with `id`, `databases`, and `uri` |
| PUT | `/connections/{id}` | Supply the current `revision`, updated `databases`, and optionally a replacement `uri` |
| DELETE | `/connections/{id}` | Supply the current `revision` |
| GET | `/datasets` | List datasets with source, saved pipeline, ordered fields and revision |
| POST | `/datasets` | Publish with `id`, `connectionId`, `database`, `collection`, `pipeline`, and an ordered array of `fields` with `name` and `type` |
| PUT | `/datasets/{id}` | Replace the source, pipeline and schema with the current `revision` |
| DELETE | `/datasets/{id}` | Supply the current `revision` |
| POST | `/preview` | Use the [query request](api.md) with optional output fields |
| GET | `/tokens` | List public metadata, concrete grants, and revocation status |
| POST | `/tokens` | Supply a `label` and `grants`. Dataset grants take `datasetId` and explicit `fields`. Collection grants take `connectionId`, `database` and `collection`. The response includes the one-time secret. |
| DELETE | `/tokens/{id}` | Revoke the token |

The synthetic [sandbox verification](../deploy/sandbox/smoke.py) covers creation, two kinds of edits, preview, issuance, use, rejection outside scope, revocation, and deletion. Run it with `VERIFY_ADMIN=1` on the default sandbox fixture.

To exercise dataset publishing in the hosted console, set `PROJECT_ID`, `OPERATOR_ACCOUNT`, and `HOSTED_CONSOLE_URL`, then run `MLC_SMOKE_DATASETS=1 node frontend/tests/hosted-smoke.js`. It publishes a temporary dataset, checks filtered rows and a field-scoped grant, revokes its token, and deletes the dataset.
