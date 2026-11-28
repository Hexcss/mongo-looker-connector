# Self-host the gateway and console

On a Linux machine with Python 3, Docker Engine, and Docker Compose v2, run this from the repository root. Supply the email you want to use for the primary owner.

```sh
python3 deploy/selfhost/init.py --owner admin@example.org
```

Open [the console](http://localhost:8091/). Read the initial owner password from the path printed by the installer, sign in, and choose a new password. You can then remove the initial password file. The **Team members** page lets owners create individual accounts, assign roles, and reset member passwords. The installation works without a Google operator account.

Select the **sample** connection and the **analytics.orders** collection in the query workspace. This pipeline returns the largest order in the two-row fixture.

```json
[{"$sort":{"amount":-1}},{"$limit":1}]
```

### Publish to Looker Studio

Apps Script needs an HTTPS URL reachable from Google's servers. Terminate TLS at a reverse proxy and forward the public hostname to the console's loopback port. Preserve the original Host header. For example, this Caddyfile serves both browser sessions and scoped reporting API requests.

```caddyfile
reports.example.org {
    reverse_proxy 127.0.0.1:8091
}
```

Configure the generic Apps Script connector's trusted gateways as described in [the connector guide](../../docs/looker-studio.md#use-a-gateway-url-and-reporting-key). Configure the matching deployment ID and public console origin when starting the stack.

```sh
MLC_LOOKER_CONNECTOR_ID='your-deployment-id' \
MLC_LOOKER_GATEWAY_ORIGIN='https://reports.example.org' \
python3 deploy/selfhost/init.py
```

Set both variables on subsequent starts to keep the handoff enabled. A Looker data source uses a reporting token authorized for the published dataset. The local editor, preview, and user administration work without a public URL.

### Storage and recovery

The installer creates a dedicated **mlc_gateway** MongoDB database with a database-scoped read/write identity for metadata. It stores managed connections, ordered pipeline JSON, published schemas, reporting-token hashes and grants, account password hashes, and expiring browser sessions. Managed source URIs use AES-256-GCM and a host-held key. The separate sample identity has read-only access to **analytics**. MongoDB stays on the Compose network. The browser console and gateway health endpoint bind to loopback ports 8091 and 8092 respectively.

Re-run the installer with the same state directory to restart or update the services. The initial owner, changed passwords, and saved datasets persist. The Docker volume is scoped to the Compose project name printed at setup. Use the same `--state-dir` path on each run. Use `--console-port` and `--gateway-port` to choose other local ports.

The default private state directory is `~/.local/share/mongo-looker-connector`. Back up its credential files and encryption key alongside a consistent MongoDB backup, storing the secrets separately from the database backup. The bootstrap database identity is mounted only where needed for MongoDB provisioning. When restoring on another host, restore the metadata volume and its matching state directory before starting the stack.

For a dedicated existing MongoDB server, configure `MLC_CONTROL_MONGO_DATABASE`, `MLC_CONTROL_MONGO_URI_FILE`, and `MLC_CONTROL_MONGO_KEY_FILE` on the gateway. Give its identity read/write access only to the named control database, including index creation. The key file contains a standard base64-encoded 32-byte random key. To bootstrap, run the gateway binary once with `init-owner` and two additional files, `MLC_OWNER_EMAIL_FILE` and `MLC_OWNER_PASSWORD_FILE`. Configure the console service with `MLC_STANDALONE_UPSTREAM` pointing to the gateway's internal origin. Use TLS for remote database and upstream connections.
