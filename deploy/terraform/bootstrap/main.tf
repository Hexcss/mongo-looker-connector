terraform {
  required_version = ">= 1.6, < 2.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 8.0"
    }
  }
  backend "gcs" {
    prefix = "bootstrap"
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

resource "google_storage_bucket" "state" {
  name                        = "${var.project_id}-tfstate"
  project                     = var.project_id
  location                    = var.region
  public_access_prevention    = "enforced"
  uniform_bucket_level_access = true
  force_destroy               = false

  versioning {
    enabled = true
  }

  lifecycle {
    prevent_destroy = true
  }
}

output "state_bucket" {
  value = google_storage_bucket.state.name
}
