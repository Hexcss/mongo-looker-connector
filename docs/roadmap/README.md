# Roadmap

The first milestone proves that a company can query its own MongoDB data through a small gateway. Each subsequent milestone adds a step toward creating a Looker Studio report without changing code.

| Milestone | Status | Acceptance criterion |
| --- | --- | --- |
| **v0.1 · Core gateway** | [v0.1.0 release record](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.1.0) | A Docker deployment discovers collections, infers schema, and returns expected aggregation results against both MongoDB and the Firestore Enterprise sandbox. |
| **v0.2 · Administration console** | [v0.2.0 release record](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.2.0) | An allowlisted administrator can configure connections, manage secrets, preview queries, and issue or revoke scoped tokens. |
| **v0.3 · Looker Studio** | Release candidate · [operator guide](../looker-studio.md) | A user authenticates through the generic Apps Script connector, browses available collections, and builds a report with a date range. |
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

## v0.3 delivery record

- The [hosted console](../administration.md) serves the administration UI over HTTPS and exchanges a five-minute ticket for a browser session. The gateway and reporting endpoints remain private.
- The [Apps Script connector](../../connector/Code.gs) implements token authentication, stepped discovery, schema, field selection and report date ranges. Run `node --test connector/connector.test.mjs` for its Apps Script contract tests.
- The [sandbox reporting smoke](../../deploy/sandbox/report_smoke.py) tests the real reporting identity and checks that a single-day range returns only the second order. The [browser smoke](../../frontend/tests/hosted-smoke.js) exercises hosted sign-in, the largest-order preview, replay rejection and sign-out.
- Release acceptance still requires a deployed Apps Script connector and a dated report opened in Looker Studio. Each operator controls Google authorization for their Apps Script installation.

## Next milestone

After the v0.3 report is verified, v0.4 adds reusable virtual datasets with stable schemas, field restrictions and permission-aware caching. Live Atlas verification remains a separate provider milestone.
