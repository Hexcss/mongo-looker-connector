terraform {
  required_version = ">= 1.6, < 2.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 8.0"
    }
  }
  backend "gcs" {
    prefix = "sandbox/gateway"
  }
}

variable "project_id" {
  type = string
}

variable "region" {
  type    = string
  default = "europe-west1"
}

variable "image_uri" {
  type        = string
  description = "Immutable sandbox Artifact Registry image including @sha256 digest."
  validation {
    condition     = can(regex("^[a-z0-9-]+-docker[.]pkg[.]dev/[a-z0-9-]+/[a-z0-9-]+/[a-z0-9._/-]+@sha256:[a-f0-9]{64}$", var.image_uri))
    error_message = "Provide an Artifact Registry image URI pinned by sha256 digest."
  }
}

variable "invokers" {
  type    = set(string)
  default = []
  validation {
    condition     = alltrue([for v in var.invokers : can(regex("^(user|group|serviceAccount):[^ ]+@[^ ]+$", v))])
    error_message = "Use an explicit user, group, or service account email for every private invoker."
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

data "terraform_remote_state" "sandbox" {
  backend = "gcs"
  config = {
    bucket = "${var.project_id}-tfstate"
    prefix = "sandbox/foundation"
  }
}

locals {
  sandbox = data.terraform_remote_state.sandbox.outputs
}

resource "google_cloud_run_v2_service" "gateway" {
  project              = var.project_id
  location             = var.region
  name                 = "mlc-sandbox"
  ingress              = "INGRESS_TRAFFIC_ALL"
  invoker_iam_disabled = false
  deletion_protection  = true

  template {
    max_instance_request_concurrency = 4
    service_account                  = local.sandbox.runtime_account
    timeout                          = "60s"
    scaling {
      min_instance_count = 0
      max_instance_count = 1
    }
    containers {
      image = var.image_uri
      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
        cpu_idle = true
      }
      env {
        name  = "MONGODB_URI"
        value = local.sandbox.mongodb_uri
      }
      env {
        name = "GATEWAY_CONNECTIONS"
        value = jsonencode([{
          id        = "demo"
          uriEnv    = "MONGODB_URI"
          databases = { (local.sandbox.database) = ["orders"] }
        }])
      }
    }
  }

  lifecycle {
    precondition {
      condition     = startswith(var.image_uri, "${local.sandbox.registry}/")
      error_message = "Publish the release image to this sandbox's Artifact Registry repository."
    }
  }
}

resource "google_cloud_run_v2_service_iam_member" "invoker" {
  for_each = var.invokers
  project  = var.project_id
  location = google_cloud_run_v2_service.gateway.location
  name     = google_cloud_run_v2_service.gateway.name
  role     = "roles/run.invoker"
  member   = each.value
}

output "service_url" {
  value = google_cloud_run_v2_service.gateway.uri
}
