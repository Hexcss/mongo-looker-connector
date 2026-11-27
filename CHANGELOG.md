# Changelog

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
