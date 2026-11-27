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

3. Create a standalone Apps Script project under the account that will maintain the connector. In **Project Settings**, show the manifest file. Add the generated `Code.gs` and `Installation.gs`, then replace `appsscript.json` with the generated manifest. Use your organization's standard Google Cloud project with IAM Credentials API enabled and configure its OAuth consent settings for the intended users. The manifest requests HTTPS fetch and the IAM scope used to mint a short-lived identity token. [Apps Script Cloud projects](https://developers.google.com/apps-script/guides/cloud-platform-projects) · [Google IAM Credentials](https://cloud.google.com/iam/docs/reference/credentials/rest/v1/projects.serviceAccounts/generateIdToken)
4. In Apps Script choose **Deploy → New deployment**, select the Looker Studio connector deployment type, and keep the deployment ID. For an upgrade, edit the existing deployment and select a new version so saved data sources keep their connector. Grant connector users view access to the script project and reserve edit access for trusted maintainers. [Google deployment guide](https://developers.google.com/looker-studio/connector/deploy) · [Sharing connectors](https://developers.google.com/looker-studio/connector/share)

## Create a report

1. In the admin console, issue a reporting token for a single collection and copy it once.
2. Open `https://lookerstudio.google.com/datasources/create?connectorId=DEPLOYMENT_ID` with your deployment ID. Authorize the Google account included in `report_members`, then enter the reporting token in the **Key** prompt.
3. Select a connection, database and collection. If the collection contains BSON dates, choose the field that should control the report's UTC date range. The connector uses the gateway's sampled schema. [Field types and limits](query.md)
4. Click **Connect** and **Create report**. In the sandbox, select `demo → mlc-sandbox → orders` and `orderedAt`. Add a table using dimension `_id` and metric `amount`. Select **November 1–2, 2026** to display both orders. Narrow the range to **November 1** to display only `order-001` with amount **42**.

The gateway pushes the selected dates into MongoDB as an inclusive UTC calendar range. Report filters on other dimensions are applied by Looker Studio. Each gateway query can return up to 1,000 rows. A query that exceeds the limit returns an error so a report author can narrow the date range. [Community Connector API](https://developers.google.com/looker-studio/connector/reference) · [Gateway date filtering](api.md#filter-by-a-utc-date-range)

## Data handling and terms

The script stores the reporting token in per-user Apps Script properties. It sends that token to the configured company gateway and sends the Google OAuth token to Google IAM to obtain the gateway invocation credential. The gateway sends the requested schema and rows through Apps Script to Looker Studio. Data source credential and report sharing settings determine who can view results. Revoking a token in the console prevents subsequent reads from the gateway. Previously fetched report data can remain in Looker Studio caches.

The source is provided under the [Apache 2.0 license](../LICENSE). The organization deploying the connector manages its access policy, retention and user support, and should provide its own service terms and privacy notice to its users.
