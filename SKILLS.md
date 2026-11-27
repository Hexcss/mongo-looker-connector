# Maintainer playbook

| Task | Start here | Evidence to capture |
| --- | --- | --- |
| Change the HTTP API | Trace the route from `cmd/gateway` through connection authorization to the driver. Update the API reference and its request examples. | Run `go test -race ./...` and `go vet ./...`. Check allowed and denied namespaces. |
| Add an aggregation stage or BSON type | Work in `internal/engine`. Define the accepted syntax, output conversion, and limits before extending the validator. | Exercise the behavior against the Docker MongoDB fixture. Record provider-specific behavior separately. |
| Test a new provider | Use a dedicated read-only identity and a small synthetic dataset. Check discovery, inferred fields, field order, and an exact expected query result. | Add the provider and tested capabilities to the README only after the checks pass. |
| Change Google Cloud infrastructure | Use Terraform under `deploy/terraform`. Supply project, region and invoker explicitly. Bootstrap the backend before the registry and database, then deploy an image digest. | Run `terraform fmt -check -recursive deploy/terraform`, validate each root, inspect its plan, and check for drift after apply. |
| Change the administration console | Start with [the administrator guide](docs/administration.md). Rebuild the React bundle from `frontend` with `npm ci && npm run build`. | Run the browser tests, the loopback launcher test, and the admin-enabled sandbox smoke. Exercise token use and revocation as a reporting caller. |
| Promote a release | Follow the branch sequence in [AGENTS.md](AGENTS.md). Use the same image digest tested on staging, check `/version`, and compare the release and staging trees. | Record the merged PR, smoke result and release in [the roadmap](docs/roadmap/README.md). |
