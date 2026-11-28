# Gateway HTTP API

## Connection configuration

Set `GATEWAY_CONNECTIONS` to a JSON array. Each entry grants access to the named collections. The gateway intersects those grants with the databases and collections the driver discovers. Configure the database identity itself with read-only access to the same namespaces.

```json
[
  {
    "id": "demo",
    "uriEnv": "MONGODB_URI",
    "databases": { "analytics": ["orders"] }
  }
]
```

Supply the complete driver URI through the named environment variable. On Cloud Run, mount a Secret Manager version as that environment variable. Every connection ID must be unique and contain 1–64 ASCII letters, digits, hyphens or underscores. Each connection needs at least one database and each database needs at least one collection. Restart the service after changing its configuration.

Collection discovery asks for names only. If MongoDB returns `Unauthorized` (code 13), the gateway retries with `authorizedCollections: true`. This lets a MongoDB identity with collection-specific read access discover its visible collections. The gateway applies the same configured allowlist to both responses.

The listener uses `PORT` (default `8080`). For local development publish the container port on `127.0.0.1`. In Cloud Run, require authenticated invocations and grant `roles/run.invoker` to the permitted callers. In a legacy deployment, an IAM-authorized caller can read all configured namespaces. When the console is enabled, reporting calls also need a scoped token or admin assertion. See the [administration guide](administration.md). Send a Google-signed ID token with the service URL as its audience in the `Authorization: Bearer` header. [Cloud Run authentication](https://cloud.google.com/run/docs/authenticating/service-to-service)

For Firestore Enterprise with MongoDB compatibility, use a connection per database. The Go driver accepts the Firestore URI with load balancing, TLS, retryable writes disabled, and GCP machine OIDC. The gateway uses the Cloud Run service identity and discovers names with empty filters. [Firestore connection instructions](https://cloud.google.com/firestore/mongodb-compatibility/docs/connect) · [Command compatibility](https://cloud.google.com/firestore/mongodb-compatibility/docs/behavior-differences)

## Endpoints

| Method and path | Description | Example response |
| --- | --- | --- |
| `GET /health` | Service liveness, independent of database availability | `{"status":"ok"}` |
| `GET /version` | Version and source revision embedded at build time | `{"version":"0.1.0","revision":"abc1234"}` |
| `GET /api/v1/connections` | Configured connection IDs in sorted order | `{"connections":["demo"]}` |
| `GET /api/v1/connections/demo/databases` | Available, allowed databases for connection `demo` | `{"databases":["analytics"]}` |
| `GET /api/v1/collections?connectionId=demo&database=analytics` | Available, allowed collections in that database | `{"collections":["orders"]}` |
| `POST /api/v1/schema` | Sample the output of an optional aggregation pipeline | `{"fields":[{"name":"region","type":"TEXT"}]}` |
| `POST /api/v1/query` | Run the pipeline and return values in the requested field order | `{"fields":["region","total"],"rows":[["west",12]]}` |

Both POST routes accept a single JSON object with `Content-Type: application/json`. Supply the connection ID, database and collection exactly as configured. The optional `pipeline` is an array of aggregation stages. For queries, supply a nonempty `fields` array. Schema field names can be passed directly to the query.

### Sample a schema

```sh
curl --fail-with-body http://127.0.0.1:8080/api/v1/schema \
  --json '{"connectionId":"demo","database":"analytics","collection":"orders"}'
```

### Aggregate a report

```sh
curl --fail-with-body http://127.0.0.1:8080/api/v1/query --json '{
  "connectionId": "demo",
  "database": "analytics",
  "collection": "orders",
  "pipeline": [
    {"$group":{"_id":"$region","total":{"$sum":"$amount"}}},
    {"$sort":{"_id":1}}
  ],
  "fields": ["_id","total"]
}'
```

## Limits and errors

The API accepts bodies up to 256 KiB. Discovery runs for up to 15 seconds. Schema and query operations run for up to 10 seconds. A shorter URI `timeoutMS`, an earlier caller deadline or a disconnect ends the database operation earlier. See the pipeline syntax, BSON conversions and result limits in the [query reference](query.md).

A handled error looks like `{"error":{"code":"not_found","message":"Connection or namespace is unavailable."}}`.

| HTTP status | Code | Meaning |
| --- | --- | --- |
| 400 | `invalid_request` | Malformed request, unsupported pipeline or incompatible report fields |
| 404 | `not_found` | Connection, database or collection falls outside the configured grants |
| 413 | `limit_exceeded` | Request, pipeline or result exceeds a gateway limit |
| 415 | `unsupported_media_type` | The POST request requires JSON |
| 502 | `database_error` | The driver could not complete the operation |
| 504 | `timeout` | The database operation exceeded the request deadline |

### Filter by a UTC date range

On `POST /api/v1/query`, add `"dateRange":{"field":"orderedAt","startDate":"2026-11-01","endDate":"2026-11-30"}` to filter a BSON date on the source documents before running the report pipeline. Both calendar dates are inclusive. The gateway uses a native MongoDB range from midnight UTC on the first day to midnight UTC after the last day, so MongoDB can use an index on the date field. Nested paths such as `order.createdAt` are supported. Keys that require escaped dots or backslashes, empty path components, and dollar-prefixed path components are excluded. Omit the range for unfiltered queries.

## Managed datasets

Signed-in operators use `GET /api/v1/admin/datasets` to list definitions and `POST` on the same path to publish one. Update or delete with `PUT` or `DELETE /api/v1/admin/datasets/{id}` and the current `revision`. To grant dataset access, include `{"datasetId":"order-amounts","fields":["orderId","sales"]}` in the token's `grants` array.

Reporting clients discover the virtual connection `@datasets` and database `published` under the usual discovery endpoints. Pass the dataset ID as the collection to `/schema` and `/query`. Dataset queries accept a `filters` object containing exact scalar matches on granted fields. [Examples, field types, revision semantics and cache headers](datasets.md)

## Query workspace

Owners and editors can preview and publish from the console. Send a JSON object with `connectionId`, `database`, `collection` and the literal read-only `pipeline` array to `POST /api/v1/admin/workspace/preview`. The result contains typed `fields` and up to 50 `rows` in field order.

To publish, add a unique `id` to the same request and send it to `POST /api/v1/admin/workspace/open`. The gateway samples the pipeline, validates the declared output and creates a managed dataset. A successful response is HTTP 201 with `datasetId` and `url`. Repeating the same publication returns its link with HTTP 200. Reusing an ID for a different definition returns HTTP 409. All console roles can retrieve a saved link with `GET /api/v1/admin/workspace/looker/{id}`. Opening the link requires a separate reporting grant to read fields and rows.

The public console origin accepts scoped reporting keys in `X-MLC-Token` on the connections, databases, collections, schema and query endpoints. The private gateway checks the key and current grants on each operation. [Team roles and password initialization](administration.md)
