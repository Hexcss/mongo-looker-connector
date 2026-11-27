variable "hosted_console" {
  type        = bool
  default     = false
  nullable    = false
  description = "Deploy the browser-facing console and enable single-use login tickets on the private admin-enabled gateway."
}

resource "google_service_account" "hosted" {
  count        = var.hosted_console ? 1 : 0
  project      = var.project_id
  account_id   = "mlc-console-web"
  display_name = "Mongo Looker Connector web transport"
  lifecycle {
    precondition {
      condition     = local.admin_enabled
      error_message = "Set admin_members to enable scoped access before enabling the hosted console."
    }
  }
}

resource "google_cloud_run_v2_service_iam_member" "hosted_invoker" {
  count    = var.hosted_console ? 1 : 0
  project  = var.project_id
  location = google_cloud_run_v2_service.gateway.location
  name     = google_cloud_run_v2_service.gateway.name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.hosted[0].email}"
}

resource "google_cloud_run_v2_service" "hosted" {
  count                = var.hosted_console ? 1 : 0
  project              = var.project_id
  location             = var.region
  name                 = "mlc-console"
  ingress              = "INGRESS_TRAFFIC_ALL"
  invoker_iam_disabled = true
  deletion_protection  = true
  depends_on           = [google_cloud_run_v2_service_iam_member.hosted_invoker]

  template {
    service_account                  = google_service_account.hosted[0].email
    max_instance_request_concurrency = 8
    timeout                          = "90s"
    scaling {
      min_instance_count = 0
      max_instance_count = 1
    }
    containers {
      image = var.image_uri
      env {
        name  = "MLC_HOSTED_GATEWAY"
        value = google_cloud_run_v2_service.gateway.uri
      }
      resources {
        limits   = { cpu = "1", memory = "256Mi" }
        cpu_idle = true
      }
    }
  }
}

resource "google_cloud_run_v2_service_iam_binding" "hosted_discovery" {
  count    = var.hosted_console && length(local.admin_members) > 0 ? 1 : 0
  project  = var.project_id
  location = google_cloud_run_v2_service.hosted[0].location
  name     = google_cloud_run_v2_service.hosted[0].name
  role     = google_project_iam_custom_role.console_discovery[0].name
  members  = local.admin_members
}

resource "google_firestore_field" "hosted_login_ttl" {
  count      = var.hosted_console && local.admin_enabled ? 1 : 0
  project    = var.project_id
  database   = google_firestore_database.metadata[0].name
  collection = "hostedLogins"
  field      = "expiresAt"
  ttl_config {}
}

output "hosted_console_url" {
  value = one(google_cloud_run_v2_service.hosted[*].uri)
}
