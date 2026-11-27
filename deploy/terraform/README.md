# Google Cloud sandbox

The sandbox runs the gateway as an IAM-authenticated Cloud Run service in `europe-west1`. Its service account can read one named Firestore Enterprise database through the MongoDB-compatible endpoint. The `demo` connection exposes the `orders` collection. The sandbox makes no changes to the default Firestore database.

Run the following commands from the repository root with Bash, gcloud, Terraform 1.6 or later, Docker, and Python 3. The seed script uses PyMongo 4 (`python3 -m pip install 'pymongo>=4.7,<5'` in a virtual environment). Use a billing-enabled project with the Service Usage and Cloud Storage APIs available. The operator needs permission to manage the listed resources, enable services, update project IAM, use the gateway service account, and publish to the sandbox registry.

```bash
export PROJECT_ID='your-project-id'
export OPERATOR_ACCOUNT='your-authorized-gcloud-account'
export TF_VAR_region='europe-west1'
tf=deploy/terraform/with-account
stacks=deploy/terraform
bucket="${PROJECT_ID}-tfstate"

gcloud projects describe "$PROJECT_ID" --project="$PROJECT_ID" --account="$OPERATOR_ACCOUNT"
```

The wrapper obtains a short-lived token with explicit gcloud account and project flags for each Terraform command. The provider and the GCS backend use that token even when application-default credentials belong to a different account. An automation with workload identity can run Terraform directly using its own ADC and an explicit `TF_VAR_project_id`.

## State bucket

For a **new project with no existing state bucket**, bootstrap locally and migrate the bucket state to its own GCS prefix. Review the bucket name, location, and plan before applying.

```bash
cp "$stacks/bootstrap/local_backend_override.tf.example" "$stacks/bootstrap/local_backend_override.tf"
"$tf" -chdir="$stacks/bootstrap" init
"$tf" -chdir="$stacks/bootstrap" plan -out=bucket.tfplan
"$tf" -chdir="$stacks/bootstrap" apply bucket.tfplan
rm "$stacks/bootstrap/local_backend_override.tf"
"$tf" -chdir="$stacks/bootstrap" init -migrate-state -backend-config="bucket=$bucket"
```

On a fresh checkout of an **existing sandbox**, initialize each existing stack against its remote state. No local override is needed.

```bash
for stack in bootstrap sandbox gateway; do
  "$tf" -chdir="$stacks/$stack" init -lockfile=readonly -backend-config="bucket=$bucket"
done
```

The versioned state bucket enforces public access prevention and uniform bucket-level access. Restrict bucket access to Terraform operators. Each stack has its own state prefix. The repository ignores local state, saved plans, Terraform cache directories, and tfvars.

## Sandbox foundation and synthetic data

Applying the `sandbox` stack opts into the dedicated `mlc-sandbox` database, Docker registry, runtime identity, and project API enablement. The reader binding uses a resource-name condition on the database. The gateway authenticates through Cloud Run's attached service account with MONGODB-OIDC. The generated URI contains the endpoint and authentication settings. [Firestore authentication](https://docs.cloud.google.com/firestore/mongodb-compatibility/docs/connect)

```bash
"$tf" -chdir="$stacks/sandbox" init -lockfile=readonly -backend-config="bucket=$bucket"
"$tf" -chdir="$stacks/sandbox" plan -out=foundation.tfplan
"$tf" -chdir="$stacks/sandbox" apply foundation.tfplan
registry=$("$tf" -chdir="$stacks/sandbox" output -raw registry)
python3 deploy/sandbox/seed.py
```

The seed script uses the operator's short-lived access token to upsert and read back two synthetic orders. The operator needs write access to the fixture database. The runtime account receives only `roles/datastore.viewer` on that database. The expected documents are `{"_id":"order-001","amount":42}` and `{"_id":"order-002","amount":17}`.

## Release the gateway

Build the release image from the repository Dockerfile for `linux/amd64`. Authenticate Docker to the regional registry using the operator's short-lived token. Publish the image and resolve its digest.

```bash
tag=$(git rev-parse --short=12 HEAD)
registry=$("$tf" -chdir="$stacks/sandbox" output -raw registry)
registry_host="${registry%%/*}"
gcloud auth print-access-token --account="$OPERATOR_ACCOUNT" --project="$PROJECT_ID" |
  docker login -u oauth2accesstoken --password-stdin "$registry_host"
docker buildx build --platform linux/amd64 --build-arg "REVISION=$(git rev-parse HEAD)" \
  -t "$registry/gateway:$tag" --push .
digest=$(gcloud artifacts docker images describe "$registry/gateway:$tag" \
  --project="$PROJECT_ID" --account="$OPERATOR_ACCOUNT" --format='value(image_summary.digest)')
export TF_VAR_image_uri="$registry/gateway@$digest"
```

Set `TF_VAR_invokers` to the identities that should be able to call the service. The default is empty. Project-level permissions can also grant invoke access. Cloud Run checks invoker IAM on inbound requests and sets the container's `PORT`. Each revision scales between zero and one instance. An instance handles at most four concurrent requests within its 512 MiB memory limit. Store the expected image digest and invoker list for future plans.

```bash
export TF_VAR_invokers="[\"user:$OPERATOR_ACCOUNT\"]"
"$tf" -chdir="$stacks/gateway" init -lockfile=readonly -backend-config="bucket=$bucket"
"$tf" -chdir="$stacks/gateway" plan -out=gateway.tfplan
"$tf" -chdir="$stacks/gateway" apply gateway.tfplan
"$tf" -chdir="$stacks/gateway" output -raw service_url
EXPECTED_VERSION="$(cat VERSION)" EXPECTED_REVISION="$(git rev-parse HEAD)" python3 deploy/sandbox/smoke.py
deploy/sandbox/drift
```

For the default IAM-only gateway, the smoke script checks that anonymous access fails, then uses a gcloud user identity token to verify health, build metadata, discovery, schema types, and the two ordered values. Follow the console instructions below when you enable administration. The drift check plans all three stacks using the expected region, digest, and invokers. Exit code 0 means no changes, 2 means a plan contains changes, and 1 means the check failed. To roll back, choose a previously verified digest that supports the deployed authentication mode, plan and apply the gateway stack, then rerun smoke and drift. A v0.1 image grants reporting access to every Cloud Run invoker. Keep administration-enabled deployments on a digest that enforces scoped reporting tokens.

## Enable the v0.2 administration console

For a v0.2 image, set `admin_members` on the **gateway** stack to the Google users who manage connections and reporting tokens. The operator applying this change also needs permission to create a project custom role, create a secret, and manage the secret and login service account IAM policies. Retain the intended image digest and reporting invokers. Terraform adds administrators to the service's invoker policy and provisions the following resources.

| Resource | Access |
| --- | --- |
| `mlc-admin-metadata`, a Firestore Native Standard database in the configured region | The runtime identity receives `roles/datastore.user` with a condition matching this database. Its existing read-only role on the fixture is unchanged. |
| `mlc-managed-connections`, one Secret Manager secret | A custom role bound on this secret lets the runtime add, access, list and destroy credential versions. The gateway writes the credential payload at runtime. |
| `mlc-console-login`, a separate service account | Allowlisted users receive `roles/iam.serviceAccountOpenIdTokenCreator` on this identity. The identity receives no project roles. The launcher calls the IAM Credentials API to mint the console assertion. |
| `mlc-sandbox`, the Cloud Run service | Allowlisted administrators receive `run.services.get` on this service so the launcher can discover its HTTPS URL. |

A new deployment with `admin_members` omitted uses IAM-only gateway access. To suspend all console users after enabling administration, set `admin_members = []` and apply. Reporting continues to require scoped tokens, and the metadata and credentials remain protected. Removing a user from the list stops fresh assertion minting once IAM changes propagate. Previously minted assertions can last up to one hour. Review inherited IAM grants, especially project-wide service account token creator permissions, when choosing the project. [Google service account authentication roles](https://cloud.google.com/iam/docs/service-account-permissions)

```bash
export TF_VAR_admin_members="[\"user:$OPERATOR_ACCOUNT\"]"
"$tf" -chdir="$stacks/gateway" plan -out=gateway.tfplan
"$tf" -chdir="$stacks/gateway" apply gateway.tfplan
VERIFY_ADMIN=1 EXPECTED_VERSION="$(cat VERSION)" EXPECTED_REVISION="$(git rev-parse HEAD)" python3 deploy/sandbox/smoke.py
deploy/sandbox/drift
python3 deploy/sandbox/console.py
```

Open the single-use loopback sign-in URL printed in your terminal. It opens `/admin/` and sets an HttpOnly, SameSite=Strict cookie scoped to the session’s random `.localhost` hostname. The launcher listens on a free port on `127.0.0.1`, obtains both ID tokens from your selected account, and refreshes them in memory for the one-hour session. Keep the sign-in URL private. For browser automation, read the first stdout line (`Open <URL>`) from a subprocess pipe and navigate there directly, with URL and trace logging disabled. Stop the launcher with Ctrl+C when finished.

With administration enabled, Cloud Run invoker IAM is the first authorization check. Reporting clients also need the scoped token issued by a console administrator. Send the Cloud Run ID token in `X-Serverless-Authorization: Bearer <cloud-run-id-token>` and the reporting token in `Authorization: Bearer <reporting-token>` or `X-MLC-Token: <reporting-token>`. For administrator requests the launcher supplies `X-MLC-Admin-Assertion` with a Google-signed ID token for the login identity and this deployment's audience. [Cloud Run authentication headers](https://cloud.google.com/run/docs/authenticating/service-to-service) · [Generate an ID token](https://cloud.google.com/iam/docs/reference/credentials/rest/v1/projects.serviceAccounts/generateIdToken)

## Use existing MongoDB connections

To expose your own databases, put each complete, read-only MongoDB URI in an existing Secret Manager secret in the gateway's project. Choose a version that is enabled. Ensure the deployment operator can enable the Secret Manager API and update each secret's IAM policy. The MongoDB endpoint must accept connections from Cloud Run and the database credential must be able to discover and read the selected collections.

Save the following as `deploy/terraform/gateway/connections.auto.tfvars`, substituting your secret IDs, version numbers, and allowed namespaces. An empty `connections` map selects the default demo. A nonempty map configures exactly the named connections and grants the gateway identity accessor access to each referenced secret. Terraform and the Cloud Run service definition contain secret references. Cloud Run retrieves their values at startup. A numerical version pins the secret used by each instance. [Cloud Run secret configuration](https://cloud.google.com/run/docs/configuring/services/secrets)

```hcl
connections = {
  sales = {
    secret_id = "sales-reporting-uri"
    version   = "3"
    databases = { analytics = ["orders", "customers"] }
  }
  billing = {
    secret_id = "billing-reporting-uri"
    version   = "1"
    databases = { ledger = ["invoices"] }
  }
}
```

Apply the gateway stack with the expected image and invokers as in the release steps. For secret rotation, update the version and apply a new revision. The fixture smoke script checks the default demo only. For the example above, verify discovery and run a one-row report as an authorized invoker.

```bash
gcloud run services proxy mlc-sandbox --project="$PROJECT_ID" --account="$OPERATOR_ACCOUNT" \
  --region="$TF_VAR_region" --port=8085
```

For an IAM-only deployment, use a second terminal to check that the connection IDs are `billing` and `sales`, the sales database is `analytics`, and the collection list matches its allowlist.

```bash
base=http://127.0.0.1:8085/api/v1
curl -fsS "$base/connections"
curl -fsS "$base/connections/sales/databases"
curl -fsS "$base/collections?connectionId=sales&database=analytics"
curl -fsS "$base/query" -H 'Content-Type: application/json' \
  -d '{"connectionId":"sales","database":"analytics","collection":"orders","pipeline":[{"$limit":1}],"fields":["_id"]}'
```

Run `deploy/sandbox/drift` with the same image, invokers and connection tfvars to verify the deployment. To test the Terraform configuration locally with the Terraform version pinned in CI (1.17.2), initialize the gateway with `-backend=false` and run `terraform -chdir=deploy/terraform/gateway test -test-directory=plan-tests`. These tests use a mocked provider and remote state. Run `python3 -B deploy/sandbox/console_test.py` for the loopback session and request checks.

Both databases, the managed credential secret and the Cloud Run service have deletion protection. The bucket also has Terraform destruction protection. The database, image storage, state object versions, and requests can incur charges while the sandbox exists.
