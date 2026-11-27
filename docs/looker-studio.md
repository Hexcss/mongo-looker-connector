# Connect Looker Studio to MongoDB

Create one private Apps Script deployment per company gateway. Report authors authenticate with Google and enter a scoped reporting token from the [administration console](administration.md). The connector discovers the connections, databases and collections that token can read.

## Install the connector

1. Enable administration in the gateway Terraform stack. Set `report_members` to the Google users who will authorize the connector. Apply the stack and read its `service_url` and `reporting_service_account` outputs.
2. From the repository root, generate the installation files. Use the **private gateway** service URL. The package pins where the script can send credentials.

   ```sh
   python3 connector/package.py \
     --gateway-url "$(terraform -chdir=deploy/terraform/gateway output -raw service_url)" \
     --invoker "$(terraform -chdir=deploy/terraform/gateway output -raw reporting_service_account)" \
     --output /tmp/mlc-looker-install
   ```

3. Create a standalone Apps Script project under the account that will maintain the connector. In **Project Settings**, show the manifest file. Add the generated `Code.gs` and `Installation.gs`, then replace `appsscript.json` with the generated manifest. Configure OAuth consent for your intended users. The connector sends its IAM requests against the reporting service account's project quota. Terraform enables the IAM Credentials API there and grants report authors the permission to consume that API's quota. The manifest requests HTTPS fetch and the IAM scope used to mint a short-lived identity token. [Apps Script Cloud projects](https://developers.google.com/apps-script/guides/cloud-platform-projects) · [Google IAM Credentials](https://cloud.google.com/iam/docs/reference/credentials/rest/v1/projects.serviceAccounts/generateIdToken)
4. In Apps Script choose **Deploy → New deployment**, select the Looker Studio connector deployment type, and keep the deployment ID. For an upgrade, edit the existing deployment and select a new version so saved data sources keep their connector. Grant connector users view access to the script project and reserve edit access for trusted maintainers. [Google deployment guide](https://developers.google.com/looker-studio/connector/deploy) · [Sharing connectors](https://developers.google.com/looker-studio/connector/share)

## Create a report

1. In the admin console, issue a reporting token for a single collection and copy it once.
2. Open `https://lookerstudio.google.com/datasources/create?connectorId=DEPLOYMENT_ID` with your deployment ID. Authorize the Google account included in `report_members`, then enter the reporting token in the **Key** prompt.
3. Select a connection, database and collection. Choose **Collection** to use its sampled fields. If the collection contains BSON dates, choose the field that should control the report's UTC date range. When there is exactly one eligible date field the connector uses it by default. With multiple date fields it asks for an explicit selection. [Field types and limits](query.md)
4. Click **Connect** and **Create report**. In the sandbox, select `demo → mlc-sandbox → orders` and `orderedAt`. Add a table using dimension `_id` and metric `amount`. Select **November 1–2, 2026** to display both orders. Narrow the range to **November 1** to display only `order-001` with amount **42**.

The gateway pushes the selected dates into MongoDB as an inclusive UTC calendar range. Report filters on other dimensions are applied by Looker Studio. Each gateway query can return up to 1,000 rows. A query that exceeds the limit returns an error so a report author can narrow the date range. [Community Connector API](https://developers.google.com/looker-studio/connector/reference) · [Gateway date filtering](api.md#filter-by-a-utc-date-range)

## Enter a custom aggregation

1. Use a reporting token with a grant for the raw collection you want to query. In the sandbox, select **demo → mlc-sandbox → orders**.
2. Choose **Custom aggregation**. Paste the pipeline below as a JSON array in the editor. It ranks orders by amount and gives the output columns names you can use in a chart.

   ```json
   [
     {"$sort": {"amount": -1, "_id": 1}},
     {"$limit": 1},
     {"$project": {"_id": 0, "orderId": "$_id", "sales": "$amount"}}
   ]
   ```

3. Choose **No date filter** and click **Connect**. Add a table with dimension `orderId` and metric `sales`. On the sandbox fixture the result is `order-001` and `42`. To find the largest order *within* a report date range, select the source date field `orderedAt` during setup. For November 2, 2026, the result is `order-002` and `17`. MongoDB applies the date range before sorting and limiting, even though the pipeline omits the date from its output.

Looker Studio saves the pipeline with the data source. Edit the connection to change it and refresh the fields after changing its output. The gateway samples up to 100 output documents to discover columns, so the pipeline needs to return a sample row during setup. Include a deterministic sort with a unique tie-breaker for charts that fetch fields in batches. The gateway accepts up to 32 read-only stages and enforces the collection grant for schema and data requests. Dataset-only tokens use the administrator's published pipeline. [Supported aggregation stages and limits](query.md) · [Looker Studio setup controls](https://developers.google.com/apps-script/reference/data-studio/text-area)

## Troubleshoot authorization

If Looker Studio reports an error processing credentials, open the failed `setCredentials` execution in the Apps Script project's **Executions** view. A `reporting_auth` warning identifies the service and HTTP status. For `iam` with `USER_PROJECT_DENIED`, check the reporting user's `serviceusage.services.use` grant on the project that owns the reporting identity. For `SERVICE_DISABLED`, check the IAM Credentials API there. For `IAM_PERMISSION_DENIED`, check the reporting identity's token-creator binding. For `FETCH_FAILED`, check the generated deployment manifest and OAuth fetch scope. A `gateway` 401 or 403 means the request reached the gateway endpoint. Verify the scoped reporting token and Cloud Run invoker access. [Google quota project setup](https://cloud.google.com/docs/quotas/set-quota-project) · [Apps Script URL allowlists](https://developers.google.com/apps-script/manifest/allowlist-url)

## Data handling and terms

The script stores the reporting token in per-user Apps Script properties. It sends that token to the configured company gateway and sends the Google OAuth token to Google IAM to obtain the gateway invocation credential. The gateway sends the requested schema and rows through Apps Script to Looker Studio. Data source credential and report sharing settings determine who can view results. Revoking a token in the console prevents subsequent reads from the gateway. Previously fetched report data can remain in Looker Studio caches.

The source is provided under the [Apache 2.0 license](../LICENSE). The organization deploying the connector manages its access policy, retention and user support, and should provide its own service terms and privacy notice to its users.

## Use a published dataset

Issue a token with a dataset grant, then select **@datasets → published → dataset ID** in the existing connector. The gateway returns the published schema restricted to the token's fields. To enable a report date control, grant its date field as well. [Managed dataset example](datasets.md)
