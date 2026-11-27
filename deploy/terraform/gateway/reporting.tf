variable "report_members" {
  type        = set(string)
  default     = []
  nullable    = false
  description = "Google users who can authenticate the Looker Studio connector to the private gateway. Each also needs a scoped reporting token."
  validation {
    condition     = alltrue([for member in var.report_members : can(regex("^user:[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+[.][A-Za-z]{2,}$", member))])
    error_message = "Use user:email-address for each reporting user."
  }
}

resource "google_service_account" "reporting" {
  count        = length(var.report_members) > 0 ? 1 : 0
  project      = var.project_id
  account_id   = "mlc-reporting"
  display_name = "Mongo Looker Connector reporting transport"
  depends_on   = [google_project_service.credentials]

  lifecycle {
    precondition {
      condition     = local.admin_enabled
      error_message = "Enable scoped reporting tokens with admin_members before adding report_members."
    }
  }
}

resource "google_service_account_iam_binding" "report_login" {
  count              = length(google_service_account.reporting)
  service_account_id = google_service_account.reporting[0].name
  role               = "roles/iam.serviceAccountOpenIdTokenCreator"
  members            = var.report_members
}

resource "google_project_iam_custom_role" "report_quota" {
  count       = length(google_service_account.reporting)
  project     = var.project_id
  role_id     = "mlcReportingQuota"
  title       = "Mongo Looker Connector reporting quota"
  permissions = ["serviceusage.services.use"]
}

resource "google_project_iam_binding" "report_quota" {
  count   = length(google_project_iam_custom_role.report_quota)
  project = var.project_id
  role    = google_project_iam_custom_role.report_quota[0].name
  members = var.report_members
}

resource "google_cloud_run_v2_service_iam_member" "report_invoker" {
  count    = length(google_service_account.reporting)
  project  = var.project_id
  location = google_cloud_run_v2_service.gateway.location
  name     = google_cloud_run_v2_service.gateway.name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.reporting[0].email}"
}

output "reporting_service_account" {
  value = one(google_service_account.reporting[*].email)
}
