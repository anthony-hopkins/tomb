# Principle V: every Google Cloud resource is defined here. Manual Console
# changes ("ClickOps") are forbidden except the documented bootstrap steps in
# README.md that no API can perform.

terraform {
  required_version = ">= 1.12.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
    # Used to generate the Cloud SQL password, which is written straight to
    # Secret Manager rather than surfaced as an output.
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }

  # Remote state so volunteers who rotate in and out never fight over a local
  # state file, and so GitHub Actions and a workstation share one source of
  # truth. The bucket is created by tofu/bootstrap.
  #
  # Partial configuration on purpose: GCS bucket names are globally unique, so
  # the name is supplied at init time rather than hardcoded here.
  #   tofu init -backend-config="bucket=YOUR_BUCKET"
  backend "gcs" {
    prefix = "platform"
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}
