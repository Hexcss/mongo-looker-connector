terraform {
  required_version = ">= 1.6, < 2.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 8.0"
    }
  }
  backend "gcs" {
    prefix = "sandbox/foundation"
  }
}

variable "project_id" {
  type = string
}

variable "region" {
  type    = string
  default = "europe-west1"
}

provider "google" {
  project = var.project_id
  region  = var.region
}

resource "google_project_service" "sandbox" {
  for_each = toset([
    "artifactregistry.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "firestore.googleapis.com",
    "iam.googleapis.com",
    "run.googleapis.com",
  ])
  project            = var.project_id
  service            = each.key
  disable_on_destroy = false
}

resource "google_artifact_registry_repository" "gateway" {
  project       = var.project_id
  location      = var.region
  repository_id = "mlc-sandbox"
  description   = "Mongo Looker Connector gateway sandbox images"
  format        = "DOCKER"
  depends_on    = [google_project_service.sandbox["artifactregistry.googleapis.com"]]
}

resource "google_service_account" "gateway" {
  project      = var.project_id
  account_id   = "mlc-sandbox-gateway"
  display_name = "Mongo Looker Connector sandbox runtime"
  depends_on   = [google_project_service.sandbox["iam.googleapis.com"]]
}

resource "google_firestore_database" "fixture" {
  project                             = var.project_id
  name                                = "mlc-sandbox"
  location_id                         = var.region
  type                                = "FIRESTORE_NATIVE"
  database_edition                    = "ENTERPRISE"
  firestore_data_access_mode          = "DATA_ACCESS_MODE_DISABLED"
  mongodb_compatible_data_access_mode = "DATA_ACCESS_MODE_ENABLED"
  delete_protection_state             = "DELETE_PROTECTION_ENABLED"
  deletion_policy                     = "PREVENT"
  depends_on                          = [google_project_service.sandbox["firestore.googleapis.com"]]
}

# A selective fixture query demonstrates an actual index in the Explain panel.
resource "google_firestore_index" "orders_amount" {
  project     = var.project_id
  database    = google_firestore_database.fixture.name
  collection  = "orders"
  query_scope = "COLLECTION_GROUP"
  api_scope   = "MONGODB_COMPATIBLE_API"
  density     = "DENSE"

  fields {
    field_path = "amount"
    order      = "ASCENDING"
  }
}

resource "google_project_iam_member" "fixture_reader" {
  project = var.project_id
  role    = "roles/datastore.viewer"
  member  = "serviceAccount:${google_service_account.gateway.email}"

  condition {
    title      = "read-mlc-sandbox-database"
    expression = "resource.name == 'projects/${var.project_id}/databases/${google_firestore_database.fixture.name}'"
  }
  depends_on = [google_project_service.sandbox["cloudresourcemanager.googleapis.com"]]
}

output "registry" {
  value = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.gateway.repository_id}"
}

output "runtime_account" {
  value = google_service_account.gateway.email
}

output "database" {
  value = google_firestore_database.fixture.name
}

output "mongodb_uri" {
  value = "mongodb://${google_firestore_database.fixture.uid}.${var.region}.firestore.goog:443/${google_firestore_database.fixture.name}?loadBalanced=true&tls=true&retryWrites=false&authMechanism=MONGODB-OIDC&authMechanismProperties=ENVIRONMENT:gcp,TOKEN_RESOURCE:FIRESTORE"
}
