# Roadmap

The first milestone proves that a company can query its own MongoDB data through a small gateway. Each subsequent milestone adds a step toward creating a Looker Studio report without changing code.

| Milestone | Status | Acceptance criterion |
| --- | --- | --- |
| **v0.1 · Core gateway** | [v0.1.0 release record](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.1.0) | A Docker deployment discovers collections, infers schema, and returns expected aggregation results against both MongoDB and the Firestore Enterprise sandbox. |
| **v0.2 · Administration console** | [v0.2.0 release record](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.2.0) | An allowlisted administrator can configure connections, manage secrets, preview queries, and issue or revoke scoped tokens. |
| **v0.3 · Looker Studio** | Planned | A user authenticates through the generic Apps Script connector, browses available collections, and builds a report with a date range. |
| **v0.4 · Managed datasets** | Planned | An administrator publishes a virtual dataset with a reusable pipeline, stable schema, field restrictions, filters, and permission-aware caching. |
| **v1.0 · Self-hosted release** | Planned | A new operator can install and upgrade the gateway, configure supported providers and use Looker Studio from the web console without source edits. |
| **v1.x · Advanced analytics** | Planned | Prioritize the visual aggregation builder, expanded filter pushdown, explain plans, shared caching, monitoring, and private networking against operator needs. |

## v0.1 delivery record

| Work | Evidence |
| --- | --- |
| Three protected branches, Apache 2.0, and contribution workflow | [Foundation PR](https://github.com/Hexcss/mongo-looker-connector/pull/1) |
| Bounded aggregation and schema conversion | [Engine PR and race-enabled Go checks](https://github.com/Hexcss/mongo-looker-connector/pull/3) |
| State, registry, database, and read-only runtime identity | [Terraform PR](https://github.com/Hexcss/mongo-looker-connector/pull/4) |
| Named connections, scoped discovery, and versioned HTTP interfaces | [Gateway API PR](https://github.com/Hexcss/mongo-looker-connector/pull/5) |
| Local reporting path | The Docker smoke discovers `analytics.orders`, checks the inferred amount type, reads the known order as `[42,"order-001"]`, and verifies the collection boundary. Run `./tests/smoke-local.sh` to reproduce. |
| Staged image and cloud verification | [Staging promotion, checks, and sandbox evidence](https://github.com/Hexcss/mongo-looker-connector/pull/2) |
| Published version | [v0.1.0 release notes and verified image digest](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.1.0) |

## v0.2 delivery record

| Work | Evidence |
| --- | --- |
| Private browser console, durable managed connections, preview, and token lifecycle | [Implementation PR and CI](https://github.com/Hexcss/mongo-looker-connector/pull/9) |
| Live sandbox verification, immutable image digest, and released source | [v0.2.0 release evidence](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.2.0) |
| Reproduce the workflows | [Administrator guide](../administration.md) and [sandbox smoke script](../../deploy/sandbox/smoke.py) |

## Next milestone

For **v0.3**, implement the generic Apps Script connector, stepped discovery, schema and data retrieval, and date ranges. Verify a Looker Studio report against a company-owned gateway and document its deployment options. Live Atlas verification remains a separate provider milestone.
