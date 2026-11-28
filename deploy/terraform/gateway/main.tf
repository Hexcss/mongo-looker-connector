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

variable "connections" {
  description = "Named connections using existing same-project Secret Manager versions containing MongoDB URIs. An empty map selects the synthetic demo."
  type = map(object({
    secret_id = string
    version   = string
    databases = map(set(string))
  }))
  default  = {}
  nullable = false

  validation {
    condition = alltrue([for id, c in var.connections :
      can(regex("^[A-Za-z0-9_-]{1,64}$", id)) &&
      can(regex("^[A-Za-z0-9_-]{1,255}$", c.secret_id)) &&
      can(regex("^(latest|[1-9][0-9]*)$", c.version))
    ])
    error_message = "Use a 1–64 character connection ID, a same-project secret ID, and a positive version number or latest."
  }

  validation {
    condition = alltrue([for c in var.connections :
      length(c.databases) > 0 && alltrue([for db, collections in c.databases :
        length(collections) > 0 && alltrue([for name in concat([db], tolist(collections)) :
          name != "" && name == trimspace(name) && !strcontains(name, "\u0000")
        ])
      ])
    ])
    error_message = "Provide nonempty database and collection allowlists with valid names."
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
  uri_env = { for i, id in keys(var.connections) : id => "MONGODB_URI_${i + 1}" }
  demo = [{
    id        = "demo"
    uriEnv    = "MONGODB_URI"
    databases = { (local.sandbox.database) = ["orders"] }
  }]
  named = [for id, c in var.connections : {
    id        = id
    uriEnv    = local.uri_env[id]
    databases = c.databases
  }]
}

resource "google_project_service" "secret_manager" {
  count              = length(var.connections) > 0 || local.admin_enabled ? 1 : 0
  project            = var.project_id
  service            = "secretmanager.googleapis.com"
  disable_on_destroy = false
}

resource "google_secret_manager_secret_iam_member" "reader" {
  for_each   = toset([for c in var.connections : c.secret_id])
  project    = var.project_id
  secret_id  = each.key
  role       = "roles/secretmanager.secretAccessor"
  member     = "serviceAccount:${local.sandbox.runtime_account}"
  depends_on = [google_project_service.secret_manager]
}

resource "google_cloud_run_v2_service" "gateway" {
  project              = var.project_id
  location             = var.region
  name                 = "mlc-sandbox"
  ingress              = "INGRESS_TRAFFIC_ALL"
  invoker_iam_disabled = false
  deletion_protection  = true
  depends_on = [
    google_secret_manager_secret_iam_member.reader,
    google_project_iam_member.metadata_writer,
    google_secret_manager_secret_iam_member.managed_versions,
    google_service_account_iam_binding.console_login,
  ]

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
      dynamic "env" {
        for_each = length(var.connections) == 0 ? [local.sandbox.mongodb_uri] : []
        content {
          name  = "MONGODB_URI"
          value = env.value
        }
      }
      dynamic "env" {
        for_each = var.connections
        content {
          name = local.uri_env[env.key]
          value_source {
            secret_key_ref {
              secret  = env.value.secret_id
              version = env.value.version
            }
          }
        }
      }
      dynamic "env" {
        for_each = merge(local.admin_env, var.hosted_console ? { MLC_HOSTED_LOGIN_ENABLED = "true" } : {}, var.looker_handoff == null ? {} : {
          MLC_LOOKER_CONNECTOR_ID   = var.looker_handoff.connector_id
          MLC_LOOKER_GATEWAY_ORIGIN = var.looker_handoff.gateway_origin
        })
        content {
          name  = env.key
          value = env.value
        }
      }
      env {
        name  = "GATEWAY_CONNECTIONS"
        value = length(var.connections) == 0 ? jsonencode(local.demo) : jsonencode(local.named)
      }
    }
  }

  lifecycle {
    precondition {
      condition     = !local.admin_enabled || alltrue([for c in var.connections : c.secret_id != "mlc-managed-connections"])
      error_message = "Reserve mlc-managed-connections for console-managed credential versions."
    }
    precondition {
      condition     = startswith(var.image_uri, "${local.sandbox.registry}/")
      error_message = "Publish the release image to this sandbox's Artifact Registry repository."
    }
  }
}

resource "google_cloud_run_v2_service_iam_member" "invoker" {
  for_each = setunion(var.invokers, local.admin_members)
  project  = var.project_id
  location = google_cloud_run_v2_service.gateway.location
  name     = google_cloud_run_v2_service.gateway.name
  role     = "roles/run.invoker"
  member   = each.value
}

output "service_url" {
  value = google_cloud_run_v2_service.gateway.uri
}
