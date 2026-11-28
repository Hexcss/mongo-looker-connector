# Managed datasets

A managed dataset is a named, read-only view of one configured collection. Its definition includes a MongoDB aggregation pipeline and an ordered list of output fields with fixed types. A report author can select the published fields their token permits. The gateway runs the saved pipeline when it needs data.

## Publish a view

Open **Datasets** in the administration console. Choose a source connection, database and collection. Enter the pipeline and declare the fields its output should have. For the sandbox orders, the following definition publishes a report-friendly name for the order ID.

```json
{
  "id": "order-amounts",
  "connectionId": "demo",
  "database": "mlc-sandbox",
  "collection": "orders",
  "pipeline": [
    {"$project": {"orderId": "$_id", "sales": "$amount", "orderedAt": 1}}
  ],
  "fields": [
    {"name": "orderId", "type": "TEXT"},
    {"name": "sales", "type": "NUMBER"},
    {"name": "orderedAt", "type": "YEAR_MONTH_DAY_SECOND"}
  ]
}
```

The fields and their order stay fixed across empty results, source additions and subsequent reads. A missing value returns null. A value incompatible with its declared type fails the query. Supported types are `TEXT`, `NUMBER`, `BOOLEAN` and `YEAR_MONTH_DAY_SECOND`. Use scalar output fields and normalize any mixed source types in the saved pipeline. Nested output paths use dots. Publication uses the gateway's [read-only pipeline rules](query.md).

## Grant access and connect a report

In **Access tokens**, select the dataset and the fields the recipient may read. For example, grant `orderId` and `sales` to an aggregate report and only `orderId` to a directory report. A dataset grant alone gives no access to the underlying collection. Issue a separate raw collection grant only to users who should query it directly.

In the installed Looker Studio connector, enter the reporting token and select **Published datasets → order-amounts**. Only granted fields appear in the schema. A granted date field also enables the connector's date-range control. Existing raw-collection data sources use their original selections. [Looker Studio setup](looker-studio.md)

For HTTP clients, use the standard schema and query endpoints with the same virtual namespace. This request filters the published output and preserves the requested column order.

```json
{
  "connectionId": "@datasets",
  "database": "published",
  "collection": "order-amounts",
  "fields": ["sales", "orderId"],
  "filters": {"sales": 42}
}
```

With the example definition and a grant for both fields, the response contains `[[42,"order-001"]]`. Equality filters and UTC date ranges operate on published fields after the saved pipeline. Field restrictions also apply to filter and date fields. The reporting client can submit an empty pipeline or the connector's standard ascending `_id` sort. Define transformations in the published pipeline.

Use native BSON string values for text equality and JSON numbers within the range −9,007,199,254,740,991 through 9,007,199,254,740,991 for numeric equality. Date equality accepts an exact UTC timestamp in `YYYYMMDDhhmmss` format. Use `dateRange` to select whole UTC days. Multiple equality filters must all match. For Looker Studio charts that fetch fields in batches, preserve a unique `_id` in the pipeline output so each batch sorts rows consistently. You can omit `_id` from the published schema.

## Edit, republish and revoke

Editing or deleting a dataset requires its latest revision. A concurrent edit returns HTTP 409 so the operator can reload first. Republishing issues a new revision. Issue replacement dataset grants for that revision, including when a deleted ID is reused. Rotating a managed source credential also requires the administrator to review and republish the dataset against the current source before granting access again.

## Cache behavior

The gateway authenticates every request and checks the live grant, dataset revision and source access before looking up a cached result. Each reporting token has its own cache entries. The key distinguishes the selected fields and their order, permitted fields, filters, date range and pipeline options. A denied request cannot reuse an authorized result.

Successful managed dataset queries report `X-MLC-Cache: MISS` or `HIT`. Entries live for up to 60 seconds from insertion. During that interval a report can see an earlier value after an ordinary source-data change. Revocation, dataset changes and removal of source access take effect on the next request even when its rows are cached. The cache lives in the gateway process, with a limit of 64 entries and 8 MiB of result payloads. A cold start, eviction or routing to another instance can produce an earlier miss.

Looker Studio runs its own cache. Revoking a gateway token blocks new gateway reads. Results already fetched by a report can remain visible according to its data freshness settings. To exercise the gateway directly, run the [sandbox cache verification](../deploy/sandbox/dataset_smoke.py).
