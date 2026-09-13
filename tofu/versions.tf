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
  # state file. Bootstrap this bucket once, by hand, before the first init.
  backend "gcs" {
    bucket = "tomb-platform-tfstate"
    prefix = "platform"
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}
