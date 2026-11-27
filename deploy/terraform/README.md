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

The smoke script checks that anonymous access fails, then uses a gcloud user identity token to verify health, build metadata, discovery, schema types, and the two ordered values. The drift check plans all three stacks using the expected region, digest, and invokers. Exit code 0 means no changes, 2 means a plan contains changes, and 1 means the check failed. To roll back the gateway, set the image URI to a previously published digest, plan and apply the gateway stack, then rerun smoke and drift.

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

In a second terminal, check that the connection IDs are `billing` and `sales`, the sales database is `analytics`, and the collection list matches its allowlist.

```bash
base=http://127.0.0.1:8085/api/v1
curl -fsS "$base/connections"
curl -fsS "$base/connections/sales/databases"
curl -fsS "$base/collections?connectionId=sales&database=analytics"
curl -fsS "$base/query" -H 'Content-Type: application/json' \
  -d '{"connectionId":"sales","database":"analytics","collection":"orders","pipeline":[{"$limit":1}],"fields":["_id"]}'
```

Run `deploy/sandbox/drift` with the same image, invokers and connection tfvars to verify the deployment. To test the Terraform configuration locally with Terraform 1.7 or later, initialize the gateway with `-backend=false` and run `terraform -chdir=deploy/terraform/gateway test -test-directory=plan-tests`. These tests use a mocked provider and remote state.

The database and Cloud Run service have deletion protection. The bucket also has Terraform destruction protection. The database, image storage, state object versions, and requests can incur charges while the sandbox exists.
