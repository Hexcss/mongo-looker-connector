variable "admin_members" {
  type        = set(string)
  default     = null
  description = "Explicit Google user principals who can open the console. Omit to preserve the legacy gateway, or set [] to keep reporting protected while locking out all console users."
  validation {
    condition     = var.admin_members == null ? true : alltrue([for member in var.admin_members : can(regex("^user:[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+[.][A-Za-z]{2,}$", member))])
    error_message = "Use user:email-address for each console administrator."
  }
}

locals {
  admin_enabled  = var.admin_members != null
  admin_members  = var.admin_members == null ? toset([]) : var.admin_members
  admin_audience = "https://mlc-admin.invalid/projects/${var.project_id}/locations/${var.region}/services/mlc-sandbox"
  admin_env = local.admin_enabled ? {
    MLC_ADMIN_PROJECT  = var.project_id
    MLC_ADMIN_DATABASE = google_firestore_database.metadata[0].name
    MLC_ADMIN_SECRET   = google_secret_manager_secret.managed[0].secret_id
    MLC_ADMIN_EMAIL    = google_service_account.console[0].email
    MLC_ADMIN_AUDIENCE = local.admin_audience
  } : {}
}

resource "google_project_service" "credentials" {
  count              = local.admin_enabled ? 1 : 0
  project            = var.project_id
  service            = "iamcredentials.googleapis.com"
  disable_on_destroy = false
}

# This identity proves console access. It does not run the gateway or receive project roles.
resource "google_service_account" "console" {
  count        = local.admin_enabled ? 1 : 0
  project      = var.project_id
  account_id   = "mlc-console-login"
  display_name = "Mongo Looker Connector console login"
  depends_on   = [google_project_service.credentials]
}

resource "google_service_account_iam_binding" "console_login" {
  count              = length(local.admin_members) > 0 ? 1 : 0
  service_account_id = google_service_account.console[0].name
  role               = "roles/iam.serviceAccountOpenIdTokenCreator"
  members            = local.admin_members
}

resource "google_firestore_database" "metadata" {
  count                   = local.admin_enabled ? 1 : 0
  project                 = var.project_id
  name                    = "mlc-admin-metadata"
  location_id             = var.region
  type                    = "FIRESTORE_NATIVE"
  database_edition        = "STANDARD"
  delete_protection_state = "DELETE_PROTECTION_ENABLED"
  deletion_policy         = "PREVENT"
  lifecycle {
    prevent_destroy = true
  }
}

resource "google_project_iam_member" "metadata_writer" {
  count   = local.admin_enabled ? 1 : 0
  project = var.project_id
  role    = "roles/datastore.user"
  member  = "serviceAccount:${local.sandbox.runtime_account}"
  condition {
    title      = "write-mlc-admin-metadata-only"
    expression = "resource.name == 'projects/${var.project_id}/databases/${google_firestore_database.metadata[0].name}'"
  }
}

resource "google_secret_manager_secret" "managed" {
  count               = local.admin_enabled ? 1 : 0
  project             = var.project_id
  secret_id           = "mlc-managed-connections"
  deletion_protection = true
  replication {
    auto {}
  }
  lifecycle {
    prevent_destroy = true
  }
  depends_on = [google_project_service.secret_manager]
}

resource "google_project_iam_custom_role" "managed_versions" {
  count   = local.admin_enabled ? 1 : 0
  project = var.project_id
  role_id = "mlcManagedConnectionVersions"
  title   = "Mongo Looker Connector managed credential versions"
  permissions = [
    "secretmanager.versions.access",
    "secretmanager.versions.add",
    "secretmanager.versions.destroy",
    "secretmanager.versions.list",
  ]
  depends_on = [google_project_service.secret_manager]
}

resource "google_secret_manager_secret_iam_member" "managed_versions" {
  count     = local.admin_enabled ? 1 : 0
  project   = var.project_id
  secret_id = google_secret_manager_secret.managed[0].id
  role      = google_project_iam_custom_role.managed_versions[0].name
  member    = "serviceAccount:${local.sandbox.runtime_account}"
}
