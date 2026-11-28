# Querying a collection

Send the same connection, database, collection and pipeline to `POST /api/v1/schema` and `POST /api/v1/query`. Schema returns the alphabetically sorted fields found in the first 100 pipeline results. Query takes an additional `fields` array and returns one array of values per result, in exactly that field order.

For example, to report totals by region, send this body to the query endpoint.

```json
{
  "connectionId": "demo",
  "database": "analytics",
  "collection": "orders",
  "pipeline": [
    {"$group": {"_id": "$region", "total": {"$sum": "$amount"}}},
    {"$sort": {"total": -1, "_id": 1}}
  ],
  "fields": ["_id", "total"]
}
```

For two regions with totals of 125 and 90, the response is:

```json
{"fields":["_id","total"],"rows":[["north",125],["south",90]]}
```

Omit `fields` when calling schema. An omitted pipeline or `[]` reads collection documents directly. An empty collection returns `{"fields":[]}` for schema and an empty `rows` array for query. Include a deterministic `$sort` with a unique tie-breaker when order or stable sampling matters. MongoDB chooses the document order otherwise.

## Pipeline syntax

Use a JSON array, with one stage per object. The gateway preserves field order within documents, including multi-key sorts. Integer literals must fit a signed 64-bit integer. Literals with a decimal point or exponent become finite BSON doubles. Use strings for exact textual identifiers. Duplicate keys, Extended JSON input wrappers, system variables and other `$$` references are rejected.

| Stages | Accepted arguments |
| --- | --- |
| `$match`, `$project`, `$addFields`, `$set` | A document containing predicates, projections or expressions |
| `$group` | A document with an `_id` grouping expression |
| `$sort` | A document with integer directions `1` and `-1`, in priority order |
| `$skip`, `$limit` | A nonnegative integer for skip or a positive integer for limit |
| `$unwind` | A single field path such as `"$items"` |
| `$unset` | One field name or a nonempty array of field names |
| `$count` | An output field name |
| `$lookup` | An equality join using four literal strings, `from`, `localField`, `foreignField`, and `as`. The foreign collection must be authorized in the same connection and database. |

All embedded objects and arrays are inspected. The following dollar-prefixed names are accepted in expressions or predicates where the database supports them.

| Purpose | Names |
| --- | --- |
| Conditions and comparisons | `$and`, `$or`, `$nor`, `$not`, `$eq`, `$ne`, `$lt`, `$lte`, `$gt`, `$gte`, `$in`, `$nin`, `$exists`, `$elemMatch`, `$expr`, `$cond`, `$ifNull` |
| Aggregation and arithmetic | `$sum`, `$avg`, `$min`, `$max`, `$first`, `$last`, `$add`, `$subtract`, `$multiply`, `$divide`, `$mod`, `$abs`, `$round` |
| Text, dates and arrays | `$concat`, `$toLower`, `$toUpper`, `$year`, `$month`, `$dayOfMonth`, `$dateToString`, `$size`, `$arrayElemAt` |

The gateway rejects nested lookup pipelines, cross-database joins, write stages and server-side JavaScript. The database also validates operator positions, operands and version-specific behavior. Check the [Firestore MongoDB compatibility tables](https://docs.cloud.google.com/firestore/mongodb-compatibility/docs/behavior-differences) for your database version.

## Join two collections and inspect an execution plan

In the sandbox, open **Pipeline workspace**, select `demo → mlc-sandbox → orders`, and choose **Join collections** in the stage builder. Choose `shipping_zones` as the other collection, use `_id` on both sides, and name the output `zone`. **One row per match** adds `$unwind`; unmatched orders then drop out of the result. The plain `$lookup` keeps every input order and attaches an array of matching zones.

To try the indexed fixture, paste this complete pipeline or build it stage by stage.

```json
[
  {"$match": {"amount": 42}},
  {"$lookup": {"from": "shipping_zones", "localField": "_id", "foreignField": "_id", "as": "zone"}},
  {"$unwind": "$zone"},
  {"$project": {"_id": 0, "order": "$_id", "region": "$zone.region", "amount": 1}}
]
```

**Run preview** should show `order-001`, `EU-South`, and `42`. **Explain** executes the same pipeline with a 50-row output limit and shows the database's execution statistics. The number of documents examined can exceed 50, particularly when MongoDB must group or sort many inputs. Index entries examined are a separate measure. The database may omit a metric or use a collection scan. Try removing the first match stage and comparing both plans. A published dataset or a Looker report can have a different plan because its filters, projection and result limit differ.

Raw reporting tokens need collection grants for the base and every lookup target. A dataset-scoped token can read only its declared fields; the saved pipeline and its currently authorized sources determine the rest. If an owner removes a join target from the connection, the gateway rechecks that dependency even for cached results.

## Field names and values

Nested documents flatten to dotted names, such as `customer.address.city`. A literal dot in a key is escaped as `\.`, a backslash as `\\`, and an empty key as `\0`. Thus a top-level key named `customer.city` appears as `customer\.city`, distinct from the nested field `customer.city`. Use the exact field names returned by the schema endpoint.

| BSON input | Looker field type and returned value |
| --- | --- |
| Missing, null or undefined | JSON `null`. A sampled field containing only nulls is `TEXT`. A requested field absent from every row returns null in each row. |
| Boolean | `BOOLEAN` and a JSON boolean |
| Integer within ±9,007,199,254,740,991, finite double | `NUMBER` and a JSON number |
| Larger-magnitude integer, Decimal128 | `TEXT` and a decimal string preserving precision |
| Double NaN or infinity | `TEXT` and `NaN`, `+Inf` or `-Inf` |
| Date in years 1 through 9999 | `YEAR_MONTH_DAY_SECOND` and a UTC string formatted `YYYYMMDDhhmmss`. Subseconds are dropped. |
| String, ObjectId | `TEXT` and the original string or 24-character lowercase hexadecimal ID |
| Array, empty document, out-of-range date or other BSON type | `TEXT` containing the driver's Extended JSON representation. Arrays stay in a single cell unless the pipeline unwinds them. |

Mixed non-null types in the first 100 results resolve to `TEXT`. Query stringifies every non-null value for that column. If a column inferred as numeric, boolean or datetime changes type after the sample, query returns an error. A pipeline projection can normalize heterogeneous values before sampling.

## Limits

| Resource | Limit |
| --- | --- |
| Operation deadline | 10 seconds including cursor reads, or the caller's earlier deadline |
| Pipeline input | 32 KiB, 32 stages, 32 levels below a stage |
| Schema sample | First 100 result documents |
| Query result | 1,000 rows and 2 MiB of serialized JSON |
| Result document | 1 MiB of BSON, 32 nested document levels |
| Fields | 256 flattened leaves per document, 256 fields per sampled schema, 1–256 unique requested fields |
| Field name | 512 bytes after escaping and flattening |

The gateway requests one extra query row to detect overflow. If any limit is exceeded, the request fails without returning partial rows. Filter or group the source data, select fewer fields, or add an explicit `$limit` to fit the report. The aggregation requests disk use disabled. A late `$limit` bounds output rows while preceding sorts or groups can still scan the underlying collection.
