# Bootstrap: the things that must exist before GitHub Actions can run OpenTofu
# at all. Applied ONCE, by hand, from a workstation with your own credentials.
#
# This is the documented bootstrap exception Principle V allows: it cannot be
# created by the pipeline it is creating.
#
# It deliberately keeps LOCAL state (terraform.tfstate here, gitignored). That
# state is only needed if you ever change the trust configuration, and keeping
# it local avoids a second chicken-and-egg with the very bucket it creates.

terraform {
  required_version = ">= 1.12.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

# ---------------------------------------------------------------------------
# APIs the platform and the pipeline need. Enabling them here means the
# deployer service account does not need serviceusage permissions later.
# ---------------------------------------------------------------------------

locals {
  required_apis = [
    "run.googleapis.com",
    "sqladmin.googleapis.com",
    "secretmanager.googleapis.com",
    "artifactregistry.googleapis.com",
    "servicenetworking.googleapis.com",
    "compute.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "sts.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "storage.googleapis.com",
  ]
}

resource "google_project_service" "required" {
  for_each = toset(local.required_apis)

  service = each.value

  # Leave the APIs on if this bootstrap is ever destroyed; disabling them
  # would break anything else in the project that depends on them.
  disable_on_destroy = false
}

# ---------------------------------------------------------------------------
# Remote state for the main configuration.
# ---------------------------------------------------------------------------

resource "google_storage_bucket" "tfstate" {
  name     = var.state_bucket
  location = var.state_bucket_location

  # Versioning is the undo button for a corrupted or truncated state file.
  versioning {
    enabled = true
  }

  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"

  # State is small; keep a bounded history rather than growing forever.
  lifecycle_rule {
    condition {
      num_newer_versions = 20
    }
    action {
      type = "Delete"
    }
  }

  # Refuse to delete a bucket that still holds state.
  force_destroy = false

  depends_on = [google_project_service.required]
}

# ---------------------------------------------------------------------------
# Container registry.
#
# This lives here rather than in the main configuration to break a circular
# dependency: the deploy pipeline must push an image before it can apply, but
# the main configuration was the thing creating the repository to push to. The
# registry is pipeline infrastructure, like the state bucket, so it belongs
# with the other prerequisites.
# ---------------------------------------------------------------------------

resource "google_artifact_registry_repository" "platform" {
  location      = var.region
  repository_id = "tomb"
  description   = "Container images for the TOMB guild platform."
  format        = "DOCKER"

  docker_config {
    # A tag always means one specific build. Re-running a deploy for the same
    # commit finds the tag present rather than silently replacing it.
    immutable_tags = true
  }

  depends_on = [google_project_service.required]
}

# ---------------------------------------------------------------------------
# Workload Identity Federation: GitHub Actions authenticates as a GCP service
# account by presenting its OIDC token. No service-account keys are ever
# created, downloaded, or stored in GitHub secrets.
# ---------------------------------------------------------------------------

resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "github-pool"
  display_name              = "GitHub Actions"
  description               = "Identity pool for GitHub Actions OIDC federation"

  depends_on = [google_project_service.required]
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github-provider"
  display_name                       = "GitHub"

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.repository" = "assertion.repository"
    "attribute.ref"        = "assertion.ref"
  }

  # Belt and braces alongside the service-account binding below: even a token
  # from another repository cannot exchange for GCP credentials here.
  attribute_condition = "assertion.repository == \"${var.github_repository}\""

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

# ---------------------------------------------------------------------------
# The deployer service account that Actions impersonates.
# ---------------------------------------------------------------------------

resource "google_service_account" "deployer" {
  account_id   = "tomb-deployer"
  display_name = "TOMB platform CI deployer"
  description  = "Impersonated by GitHub Actions via Workload Identity Federation"

  depends_on = [google_project_service.required]
}

# Only tokens from this repository may impersonate the deployer.
resource "google_service_account_iam_member" "github_impersonation" {
  service_account_id = google_service_account.deployer.name
  role               = "roles/iam.workloadIdentityUser"
  member = format(
    "principalSet://iam.googleapis.com/%s/attribute.repository/%s",
    google_iam_workload_identity_pool.github.name,
    var.github_repository,
  )
}

# ---------------------------------------------------------------------------
# Deployer permissions.
#
# This is a broad set, because the main configuration creates networks, IAM
# bindings, a Cloud SQL instance and secrets. It is effectively project
# administrator, which is exactly why the identity is federated to one
# repository and holds no downloadable key.
# ---------------------------------------------------------------------------

locals {
  deployer_roles = [
    "roles/run.admin",                       # Cloud Run service
    "roles/artifactregistry.admin",          # repository + image push
    "roles/secretmanager.admin",             # secret containers and versions
    "roles/compute.networkAdmin",            # VPC, private IP range
    "roles/servicenetworking.networksAdmin", # private services access
    "roles/compute.admin",                   # the VM, disks, firewall, address
    "roles/iam.serviceAccountAdmin",         # creates the VM runtime SA
    "roles/iam.serviceAccountUser",          # attaches that SA to the VM
    "roles/resourcemanager.projectIamAdmin", # project-level IAM bindings
    "roles/iap.tunnelResourceAccessor",      # SSH to the VM through IAP
    "roles/compute.osAdminLogin",            # run the deploy script with sudo
    "roles/serviceusage.serviceUsageAdmin",  # enable project APIs (Vertex AI, spec 003)
  ]
}

resource "google_project_iam_member" "deployer" {
  for_each = toset(local.deployer_roles)

  project = var.project_id
  role    = each.value
  member  = "serviceAccount:${google_service_account.deployer.email}"
}

# State bucket access, scoped to the bucket rather than granted project-wide.
resource "google_storage_bucket_iam_member" "deployer_state" {
  bucket = google_storage_bucket.tfstate.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.deployer.email}"
}
