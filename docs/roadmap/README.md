# Roadmap

The first milestone proves that a company can query its own MongoDB data through a small gateway. Each subsequent milestone adds a step toward creating a Looker Studio report without changing code.

| Milestone | Status | Acceptance criterion |
| --- | --- | --- |
| **v0.1 · Core gateway** | In progress | A Docker deployment discovers collections, infers schema, and returns expected aggregation results against both MongoDB and the Firestore Enterprise sandbox. |
| **v0.2 · Administration console** | Planned | An allowlisted administrator can configure connections, manage secrets, preview queries, and issue or revoke scoped tokens. |
| **v0.3 · Looker Studio** | Planned | A user authenticates through the generic Apps Script connector, browses available collections, and builds a report with a date range. |
| **v0.4 · Managed datasets** | Planned | An administrator publishes a virtual dataset with a reusable pipeline, stable schema, field restrictions, filters, and permission-aware caching. |
| **v1.0 · Self-hosted release** | Planned | A new operator can install and upgrade the gateway, configure supported providers and use Looker Studio from the web console without source edits. |
| **v1.x · Advanced analytics** | Planned | Prioritize the visual aggregation builder, expanded filter pushdown, explain plans, shared caching, monitoring, and private networking against operator needs. |

## v0.1 progress

- The repository started at [the initial commit](https://github.com/Hexcss/mongo-looker-connector/commit/8d252fd7e5d1f13001b7242e24ecb9c670c715ce). The permanent branches are `develop`, `staging`, and `master`.
- Release evidence belongs here as verification completes. The release gate is an authenticated query against a deterministic sandbox fixture, a clean Terraform plan, and a tag on the promoted source tree.
