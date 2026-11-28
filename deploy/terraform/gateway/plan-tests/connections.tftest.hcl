mock_provider "google" {}

override_data {
  target = data.terraform_remote_state.sandbox
  values = {
    outputs = {
      database        = "mlc-sandbox"
      runtime_account = "gateway@example-project.iam.gserviceaccount.com"
      mongodb_uri     = "mongodb://fixture.example/mlc-sandbox"
      registry        = "europe-west1-docker.pkg.dev/example-project/mlc-sandbox"
    }
  }
}

variables {
  project_id = "example-project"
  image_uri  = "europe-west1-docker.pkg.dev/example-project/mlc-sandbox/gateway@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}

run "demo_without_secrets" {
  command = plan
  assert {
    condition = (
      length(google_project_service.secret_manager) == 0 &&
      length(google_project_iam_custom_role.report_quota) == 0 &&
      length(google_project_iam_binding.report_quota) == 0 &&
      length(google_secret_manager_secret_iam_member.reader) == 0 &&
      { for e in google_cloud_run_v2_service.gateway.template[0].containers[0].env : e.name => e.value } == {
        MONGODB_URI         = "mongodb://fixture.example/mlc-sandbox"
        GATEWAY_CONNECTIONS = jsonencode([{ id = "demo", uriEnv = "MONGODB_URI", databases = { mlc-sandbox = ["orders", "shipping_zones"] } }])
      }
    )
    error_message = "The default service must retain the demo URI and allowlist without enabling Secret Manager."
  }
}

run "named_connections_with_shared_secret" {
  command = plan
  variables {
    connections = {
      "sales-eu" = { secret_id = "report-reader", version = "3", databases = { europe = ["orders"] } }
      "sales_eu" = { secret_id = "report-reader", version = "4", databases = { history = ["ledger"] } }
    }
  }
  assert {
    condition = (
      length(google_project_service.secret_manager) == 1 &&
      length(google_secret_manager_secret_iam_member.reader) == 1 &&
      google_secret_manager_secret_iam_member.reader["report-reader"].secret_id == "report-reader" &&
      google_secret_manager_secret_iam_member.reader["report-reader"].role == "roles/secretmanager.secretAccessor" &&
      google_secret_manager_secret_iam_member.reader["report-reader"].member == "serviceAccount:gateway@example-project.iam.gserviceaccount.com"
    )
    error_message = "Grant the runtime identity one accessor binding for the shared secret."
  }
  assert {
    condition = (
      { for e in google_cloud_run_v2_service.gateway.template[0].containers[0].env :
        e.name => [e.value_source[0].secret_key_ref[0].secret, e.value_source[0].secret_key_ref[0].version]
        if e.name != "GATEWAY_CONNECTIONS" } == {
        MONGODB_URI_1 = ["report-reader", "3"]
        MONGODB_URI_2 = ["report-reader", "4"]
      } &&
      one([for e in google_cloud_run_v2_service.gateway.template[0].containers[0].env : e.value if e.name == "GATEWAY_CONNECTIONS"]) == jsonencode([
        { id = "sales-eu", uriEnv = "MONGODB_URI_1", databases = { europe = ["orders"] } },
        { id = "sales_eu", uriEnv = "MONGODB_URI_2", databases = { history = ["ledger"] } }
      ])
    )
    error_message = "Replace the demo with the exact named database allowlists and distinct URI secret versions."
  }
}

run "explicit_console_administrators" {
  command = plan
  variables {
    admin_members = ["user:operator@example.org"]
    invokers      = ["user:reporter@example.org"]
  }
  override_resource {
    target          = google_service_account.console
    override_during = plan
    values = {
      email = "mlc-console-login@example-project.iam.gserviceaccount.com"
      name  = "projects/example-project/serviceAccounts/mlc-console-login@example-project.iam.gserviceaccount.com"
    }
  }
  assert {
    condition = (
      !google_cloud_run_v2_service.gateway.invoker_iam_disabled &&
      toset(keys(google_cloud_run_v2_service_iam_member.invoker)) == setunion(var.invokers, var.admin_members) &&
      google_project_iam_custom_role.console_discovery[0].permissions == toset(["run.services.get"]) &&
      google_cloud_run_v2_service_iam_binding.console_discovery[0].members == var.admin_members &&
      google_cloud_run_v2_service_iam_binding.console_discovery[0].name == google_cloud_run_v2_service.gateway.name &&
      google_service_account_iam_binding.console_login[0].role == "roles/iam.serviceAccountOpenIdTokenCreator" &&
      google_service_account_iam_binding.console_login[0].members == var.admin_members &&
      google_project_service.credentials[0].service == "iamcredentials.googleapis.com"
    )
    error_message = "Keep Cloud Run private and restrict assertion minting to console administrators."
  }
  assert {
    condition = (
      google_firestore_database.metadata[0].database_edition == "STANDARD" &&
      google_firestore_database.metadata[0].type == "FIRESTORE_NATIVE" &&
      google_project_iam_member.metadata_writer[0].role == "roles/datastore.user" &&
      google_project_iam_member.metadata_writer[0].condition[0].expression == "resource.name == 'projects/example-project/databases/mlc-admin-metadata'" &&
      google_project_iam_member.metadata_writer[0].member == "serviceAccount:gateway@example-project.iam.gserviceaccount.com" &&
      google_project_iam_custom_role.managed_versions[0].permissions == toset(["secretmanager.versions.access", "secretmanager.versions.add", "secretmanager.versions.destroy", "secretmanager.versions.list"]) &&
      google_secret_manager_secret_iam_member.managed_versions[0].member == "serviceAccount:gateway@example-project.iam.gserviceaccount.com" &&
      google_project_service.secret_manager[0].service == "secretmanager.googleapis.com"
    )
    error_message = "Bind metadata and managed credential operations to the gateway's runtime identity and dedicated resources."
  }
  assert {
    condition = (
      { for e in google_cloud_run_v2_service.gateway.template[0].containers[0].env : e.name => e.value if startswith(e.name, "MLC_ADMIN_") } == {
        MLC_ADMIN_PROJECT  = "example-project"
        MLC_ADMIN_DATABASE = "mlc-admin-metadata"
        MLC_ADMIN_SECRET   = "mlc-managed-connections"
        MLC_ADMIN_EMAIL    = "mlc-console-login@example-project.iam.gserviceaccount.com"
        MLC_ADMIN_AUDIENCE = "https://mlc-admin.invalid/projects/example-project/locations/europe-west1/services/mlc-sandbox"
      }
    )
    error_message = "Configure all five console settings without storing credential values in Terraform."
  }
}

run "withhold_console_login" {
  command = plan
  variables {
    admin_members = []
  }
  assert {
    condition = (
      length(google_service_account_iam_binding.console_login) == 0 &&
      length(google_cloud_run_v2_service_iam_binding.console_discovery) == 0 &&
      length(google_cloud_run_v2_service_iam_member.invoker) == 0 &&
      length(google_firestore_database.metadata) == 1 &&
      length(local.admin_env) == 5
    )
    error_message = "Revoking all console users must preserve the protected reporting mode and metadata."
  }
}

run "reject_anonymous_admin_members" {
  command = plan
  variables {
    admin_members = ["allUsers"]
  }
  expect_failures = [var.admin_members]
}

run "reporting_transport" {
  command = plan
  variables {
    admin_members  = ["user:operator@example.org"]
    report_members = ["user:analyst@example.org"]
  }
  assert {
    condition = (
      google_service_account.reporting[0].account_id == "mlc-reporting" &&
      google_project_iam_custom_role.report_quota[0].permissions == toset(["serviceusage.services.use"]) &&
      google_project_iam_binding.report_quota[0].members == var.report_members &&
      google_project_iam_binding.report_quota[0].project == var.project_id &&
      google_service_account_iam_binding.report_login[0].members == var.report_members &&
      google_service_account_iam_binding.report_login[0].role == "roles/iam.serviceAccountOpenIdTokenCreator" &&
      google_cloud_run_v2_service_iam_member.report_invoker[0].role == "roles/run.invoker" &&
      google_cloud_run_v2_service_iam_member.report_invoker[0].name == google_cloud_run_v2_service.gateway.name &&
      google_service_account_iam_binding.console_login[0].members == var.admin_members
    )
    error_message = "Report creators can mint only reporting-identity tokens; their service account invokes the private gateway."
  }
}

run "reject_reporting_without_scoped_auth" {
  command = plan
  variables {
    report_members = ["user:analyst@example.org"]
  }
  expect_failures = [google_service_account.reporting]
}

run "hosted_console" {
  command = plan
  variables {
    admin_members  = ["user:operator@example.org"]
    hosted_console = true
  }
  assert {
    condition = (
      google_cloud_run_v2_service.hosted[0].invoker_iam_disabled &&
      !google_cloud_run_v2_service.gateway.invoker_iam_disabled &&
      google_service_account.hosted[0].account_id == "mlc-console-web" &&
      google_cloud_run_v2_service_iam_member.hosted_invoker[0].name == google_cloud_run_v2_service.gateway.name &&
      google_cloud_run_v2_service_iam_member.hosted_invoker[0].role == "roles/run.invoker" &&
      google_cloud_run_v2_service.hosted[0].template[0].containers[0].env[*].name == tolist(["MLC_HOSTED_GATEWAY"]) &&
      contains(google_cloud_run_v2_service.gateway.template[0].containers[0].env[*].name, "MLC_HOSTED_LOGIN_ENABLED") &&
      google_cloud_run_v2_service_iam_binding.hosted_discovery[0].members == var.admin_members
    )
    error_message = "Expose only the separate browser entry point and invoke the private gateway with a dedicated identity."
  }
  assert {
    condition = (
      google_firestore_field.hosted_login_ttl[0].database == google_firestore_database.metadata[0].name &&
      google_firestore_field.hosted_login_ttl[0].collection == "hostedLogins" &&
      google_firestore_field.hosted_login_ttl[0].field == "expiresAt" &&
      length(google_firestore_field.hosted_login_ttl[0].ttl_config) == 1
    )
    error_message = "Remove expired single-use logins from the operator metadata database."
  }
}

run "reject_hosted_console_without_scoped_auth" {
  command = plan
  variables {
    hosted_console = true
  }
  expect_failures = [google_service_account.hosted]
}
