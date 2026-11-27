# Administration console

The React and TypeScript console ships inside the Go binary at `/admin/`. Vite emits self-contained production assets into `internal/console/static`. The gateway and Docker builds work from a fresh checkout with only Go. The browser uses the same-origin administration endpoints through the private console launcher.

## Change the interface

Use Node 22.12 or newer and run these commands from the repository root.

```sh
npm --prefix frontend ci
npm --prefix frontend run build
npm --prefix frontend exec -- playwright install chromium
npm --prefix frontend test
go test ./internal/console
```

Commit the rebuilt assets alongside the source. After committing or on CI, rebuild and check for a clean output directory.

```sh
npm --prefix frontend run build
git diff --exit-code -- internal/console/static
```

The Chromium tests serve the production bundle on a temporary loopback port and supply a synthetic administration API. They exercise connection creation, credential rotation, multi-database allowlists, editing, deletion, preview validation, numeric precision, mobile layout, token scopes and revocation. No cloud or database credentials are needed.

## Verify a running gateway

Sign in with an allowlisted gcloud account as described in the [administration guide](../docs/administration.md). The test starts a private loopback launcher, consumes the single-use link in a fresh browser session, and stops the launcher when finished. It reads the connection directory and token inventory, then runs a one-row query. It prints the check outcome without logging session links or returned data. The connection, database and collection settings are optional and default to the first available namespace.

```sh
export PROJECT_ID='your-project-id'
export OPERATOR_ACCOUNT='your-allowlisted-account'
MLC_SMOKE_CONNECTION=demo \
MLC_SMOKE_DATABASE=mlc-sandbox \
MLC_SMOKE_COLLECTION=orders \
npm --prefix frontend run smoke
```

To exercise a local fixture that already serves the console and administration API, supply its loopback `/admin/` address in `MLC_CONSOLE_URL`.
