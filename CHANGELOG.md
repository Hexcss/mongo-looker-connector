# Changelog

## [0.8.0](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.8.0)

- Build same-database equality joins with authorized collections in the pipeline editor, including field suggestions and an option to unwind matches into rows.
- Inspect the indexes used, documents examined and index entries examined for a bounded pipeline using the source database's execution statistics.
- Recheck every saved join dependency before returning dataset results, including cached results. The sandbox ships a two-collection indexed fixture.

## 0.7.0

- Build an aggregation in the hosted query workspace with a stage builder, field suggestions and a typed preview. Save it as a dataset and open its preconfigured Looker Studio source.
- Sign in directly, change passwords and manage owner, editor and viewer accounts. New self-hosted installations initialize a MongoDB catalog and generate the primary owner's first-use password.
- Configure the generic Looker Studio connector with a registered gateway URL and a field-scoped reporting key. The private IAM connector remains available for existing reports.

## [0.6.0](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.6.0) · 2026-11-28

- Refresh the connector card, reporting-token help and native Looker Studio setup controls.
- Open published datasets directly, configure collections and custom pipelines in English or Spanish, and review the available output fields before connecting.

## [0.5.0](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.5.0) · 2026-11-27

- Enter a read-only MongoDB aggregation pipeline while configuring a raw collection data source in Looker Studio. The connector discovers fields from the aggregation output and uses the saved pipeline for chart queries.
- Optionally select a source date field to apply the report's UTC date range before aggregation.

## [0.4.0](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.4.0) · 2026-11-27

- Publish virtual MongoDB datasets with reusable pipelines, explicit typed schemas and field-scoped reporting grants.
- Discover published datasets in the existing Looker Studio connector and query them with date ranges or typed equality filters through the gateway.
- Cache authorized dataset queries for up to 60 seconds, with separate entries for each reporting token and request.

## [0.3.1](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.3.1) · 2026-11-27

- Preserve the same-origin information Firefox needs for console sign-in, preview and sign-out.

## [0.3.0](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.3.0) · 2026-11-27

- Serve the admin console at a permanent HTTPS address with short-lived, single-use sign-in and a secure browser session.
- Connect Looker Studio to a private gateway with a scoped reporting credential and a dedicated Google reporting identity.
- Filter reports by inclusive UTC calendar dates on a BSON date field and test the result against dated sandbox orders.

## [0.2.0](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.2.0) · 2026-11-27

- Add a private administration console for managing connections, rotating credentials, and previewing up to 50 aggregation rows.
- Issue one-time reporting credentials scoped to specific connections, databases, and collections. Changes and revocation take effect on subsequent requests.
- Provision a dedicated configuration database, managed secret, and Google-authenticated console login through Terraform.

## [0.1.0](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.1.0) · 2026-11-27

- Discover allowlisted MongoDB connections, databases, and collections with the Go HTTP API.
- Sample aggregation results to infer a tabular schema and run bounded read-only reports with ordered output fields.
- Run the synthetic MongoDB demo with Docker Compose or deploy an IAM-protected Cloud Run gateway and Firestore Enterprise fixture through Terraform.
- Establish the Apache 2.0 project, three-branch release workflow, and [public roadmap](docs/roadmap/README.md).
