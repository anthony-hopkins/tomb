variable "project_id" {
  description = "Google Cloud project that hosts the TOMB platform."
  type        = string
}

variable "region" {
  description = "Google Cloud region for Cloud Run and Cloud SQL."
  type        = string
  default     = "us-central1"
}

variable "service_name" {
  description = "Cloud Run service name."
  type        = string
  default     = "tomb-platform"
}

variable "image" {
  description = <<-EOT
    Fully qualified container image to deploy, e.g.
    us-central1-docker.pkg.dev/PROJECT/tomb/platform:GIT_SHA.
    The same image built for local development is promoted here (Principle IV).
  EOT
  type        = string
}

variable "bnet_region" {
  description = "The single Blizzard region this deployment serves (FR-014)."
  type        = string
  default     = "us"
}

variable "guild_name" {
  description = "TOMB guild name used for membership verification (FR-013)."
  type        = string
  default     = "TOMB"
}

variable "guild_realm" {
  description = "TOMB guild realm slug, lowercase and hyphenated (FR-013)."
  type        = string
}

variable "bnet_client_id" {
  description = "Battle.net OAuth client id. Not a secret, but environment-specific."
  type        = string
}

variable "public_url" {
  description = "Public base URL, used to build the OAuth redirect URL."
  type        = string
}

variable "db_tier" {
  description = "Cloud SQL machine tier. The smallest tier is ample for one guild."
  type        = string
  default     = "db-f1-micro"
}

# NOTE: there is deliberately no variable for the Battle.net client secret.
# Principle V forbids secrets in OpenTofu files or state; it lives in Secret
# Manager and is referenced by version. See README.md.
