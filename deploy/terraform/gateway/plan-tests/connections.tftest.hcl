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
      length(google_secret_manager_secret_iam_member.reader) == 0 &&
      { for e in google_cloud_run_v2_service.gateway.template[0].containers[0].env : e.name => e.value } == {
        MONGODB_URI         = "mongodb://fixture.example/mlc-sandbox"
        GATEWAY_CONNECTIONS = jsonencode([{ id = "demo", uriEnv = "MONGODB_URI", databases = { mlc-sandbox = ["orders"] } }])
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
