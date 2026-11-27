# Working on Mongo Looker Connector

The gateway runs in the operator's Google Cloud project. It provides read-only access to explicitly configured MongoDB collections. The public API is versioned independently of the future Looker Studio connector. Start with the [README](README.md) and [roadmap](docs/roadmap/README.md) to see what ships today.

<!-- CODEGRAPH_START -->
## CodeGraph

In repositories indexed by CodeGraph (a `.codegraph/` directory exists at the repo root), reach for it BEFORE grep/find or reading files when you need to understand or locate code:

- **MCP tool** (when available): `codegraph_explore` answers most code questions in one call — the relevant symbols' verbatim source plus the call paths between them, including dynamic-dispatch hops grep can't follow. Name a file or symbol in the query to read its current line-numbered source. If it's listed but deferred, load it by name via tool search.
- **Shell** (always works): `codegraph explore "<symbol names or question>"` prints the same output.

If there is no `.codegraph/` directory, skip CodeGraph entirely — indexing is the user's decision.
<!-- CODEGRAPH_END -->

## Branches and commits

1. Branch from current `develop`. Use `feat/` for features, `fix/` for corrections, `chore/` for tooling or docs, `opti/` for performance, and `sec/` for security changes.
2. Commit coherent changes with an imperative subject. Include the behavior, tests and associated documentation together. Open a pull request against `develop`.
3. Promote `develop` to `staging` by PR. Deploy the candidate image by digest to the sandbox, run the authenticated fixture smoke test and check the Terraform plan for drift.
4. Promote `staging` to `master` by PR after the sandbox checks pass. Tag the released tree as `vX.Y.Z`. Release notes identify the tested source revision and image digest. The three permanent branches accept changes through PRs.

## Before handing off

- Use [SKILLS.md](SKILLS.md) for the task-specific checks. Update public API or deployment examples in the same PR as behavior changes.
- Keep connection strings, tokens, customer data, Terraform state, and personal account identifiers out of Git and logs. Use read-only database identities for the gateway. Apply the same namespace checks to discovery, schema sampling and queries.
- Update [the roadmap](docs/roadmap/README.md) when a milestone changes state. Link the merged PR, test run, deployment verification or release that supports the change. State what remains unverified.
- Keep the next release number in `VERSION` and user-visible changes in [CHANGELOG.md](CHANGELOG.md). Use patch releases for fixes and minor releases for new pre-1.0 milestones. From 1.0 onward, bump major for incompatible public API changes, minor for compatible additions, and patch for fixes.
