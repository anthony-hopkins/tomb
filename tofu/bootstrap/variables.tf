variable "project_id" {
  description = "Google Cloud project that hosts the TOMB platform."
  type        = string
}

variable "region" {
  description = "Default region. Must match the main configuration."
  type        = string
  default     = "us-central1"
}

variable "github_repository" {
  description = <<-EOT
    The GitHub repository allowed to impersonate the deployer, as
    "owner/name". Only OIDC tokens carrying this repository claim can exchange
    for GCP credentials, so a fork or another repository cannot deploy.
  EOT
  type        = string
  default     = "anthony-hopkins/tomb"

  validation {
    condition     = can(regex("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", var.github_repository))
    error_message = "github_repository must be in owner/name form."
  }
}

variable "state_bucket" {
  description = <<-EOT
    GCS bucket for the main configuration's OpenTofu state. Bucket names are
    globally unique across all of Google Cloud, so this may need a suffix.
    It must match the backend block in ../versions.tf.
  EOT
  type        = string
  default     = "tomb-platform-tfstate"
}

variable "state_bucket_location" {
  description = "Location for the state bucket."
  type        = string
  default     = "US"
}
