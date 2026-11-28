# Roadmap

The first milestone proves that a company can query its own MongoDB data through a small gateway. Each subsequent milestone adds a step toward creating a Looker Studio report without changing code.

| Milestone | Status | Acceptance criterion |
| --- | --- | --- |
| **v0.1 · Core gateway** | [v0.1.0 release record](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.1.0) | A Docker deployment discovers collections, infers schema, and returns expected aggregation results against both MongoDB and the Firestore Enterprise sandbox. |
| **v0.2 · Administration console** | [v0.2.0 release record](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.2.0) | An allowlisted administrator can configure connections, manage secrets, preview queries, and issue or revoke scoped tokens. |
| **v0.3 · Looker Studio** | [v0.3.0 release record](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.3.0) | A user authenticates through the generic Apps Script connector, browses available collections, and builds a report with a date range. |
| **v0.4 · Managed datasets** | [v0.4.0 release record](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.4.0) | An administrator publishes a virtual dataset with a reusable pipeline, stable schema, field restrictions, filters, and permission-aware caching. |
| **v0.5 · Custom aggregations in Looker Studio** | [v0.5.0 release record](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.5.0) | A report author enters a read-only pipeline for an authorized collection during data source setup and charts fields from its output. |
| **v0.6 · Looker Studio setup** | [v0.6.0 release record](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.6.0) | Authors can configure an authorized source in English or Spanish and review the fields it provides before connecting. |
| **v0.7 · Hosted query workspace and teams** | [Release and sandbox verification](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.7.0) | Team members sign in directly, preview rows and fields, publish a saved pipeline and open its configured Looker Studio data source. A fresh self-hosted install initializes its MongoDB catalog. |
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
- The operator opened the sandbox report with Apps Script deployment version 2 and confirmed that it rendered. The Google-hosted runtime issued nine successful data queries. An independent check verified that November 1 selects the order for 42 and November 1–2 includes the second order for 17. [Implementation](https://github.com/Hexcss/mongo-looker-connector/pull/12) · [Credential and sign-out corrections](https://github.com/Hexcss/mongo-looker-connector/pull/14) · [Sandbox verification](https://github.com/Hexcss/mongo-looker-connector/pull/15)

The [v0.3.1 maintenance release](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.3.1) tracks Firefox compatibility for hosted sign-in and administration requests.

## v0.4 delivery record

- [Managed datasets](../datasets.md) covers publishing an aggregation, declaring stable fields, selecting field grants, and connecting the published view to Looker Studio.
- The [live sandbox verification](../../deploy/sandbox/dataset_smoke.py) exercises distinct token scopes, cached and uncached queries, expiry, revision changes, and source credential rotation. The [release record](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.4.0) links deployment evidence and identifies the tested image.

## v0.5 delivery record

- The [custom aggregation example](../looker-studio.md#enter-a-custom-aggregation) finds the largest sandbox order and demonstrates how a source date selection changes the result.
- The [connector tests](../../connector/connector.test.mjs) cover setup and report requests. The [sandbox reporting check](../../deploy/sandbox/report_smoke.py) verifies the projected field names and exact rows. The [release record](https://github.com/Hexcss/mongo-looker-connector/releases/tag/v0.5.0) identifies the connector version and tested staging image.

## v0.7 verification

The [workspace smoke](../../deploy/sandbox/workspace_smoke.py) checks typed results, publication, handoff parameters, field restrictions and revocation through the public reporting origin. The [self-hosting guide](../../deploy/selfhost/README.md) covers fresh MongoDB installations. Existing Cloud installations can continue using their Firestore catalog and existing connector deployment. An in-place migration of that catalog is a separate upgrade task.

## Next milestone

For **v1.0**, verify a fresh self-hosted install and an upgrade, document provider configuration, and exercise the complete path from the console to a Looker Studio report. Live Atlas verification remains a separate provider milestone.
