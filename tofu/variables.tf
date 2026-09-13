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
  description = <<-EOT
    Public base URL, used to build the Battle.net OAuth redirect URL.

    Empty is allowed for the very first apply, because the Cloud Run URL does
    not exist until Cloud Run does. The deploy workflow reads the URL from the
    service_url output and applies a second time to close the loop; after that
    it stays set.
  EOT
  type        = string
  default     = ""
}

variable "db_tier" {
  description = <<-EOT
    Cloud SQL machine tier.

    db-custom-1-3840 (1 vCPU, 3.75 GB) is the smallest DEDICATED-core tier and
    the first one Google covers with a full SLA -- the shared-core tiers
    (db-f1-micro, db-g1-small) are explicitly not recommended for production
    and cannot be made highly available.

    This is the single biggest line on the bill, because unlike Cloud Run the
    database runs 24/7 and never scales to zero. Roughly:
      db-f1-micro      shared core, 0.6 GB   ~$9/month    (dev only)
      db-g1-small      shared core, 1.7 GB   ~$27/month
      db-custom-1-3840 1 vCPU,     3.75 GB   ~$52/month   <- current default
      db-custom-2-7680 2 vCPU,     7.5 GB    ~$104/month
  EOT
  type        = string
  default     = "db-custom-1-3840"
}

variable "db_disk_size" {
  description = "Cloud SQL disk in GB. Autoresizes upward; this is the floor."
  type        = number
  default     = 20
}

variable "db_availability_type" {
  description = <<-EOT
    "ZONAL" or "REGIONAL". REGIONAL is synchronous standby failover and roughly
    doubles the database cost. ZONAL is the right call for a guild site: the
    data is two small tables that are rebuilt from Blizzard on every page view
    anyway, and backups plus point-in-time recovery are already enabled.
  EOT
  type        = string
  default     = "ZONAL"
}

variable "run_cpu" {
  description = <<-EOT
    vCPU per Cloud Run instance. Generous values are nearly free here because
    the service scales to zero and only bills while serving a request.
  EOT
  type        = string
  default     = "2"
}

variable "run_memory" {
  description = "Memory per Cloud Run instance."
  type        = string
  default     = "1Gi"
}

variable "run_max_instances" {
  description = "Upper bound on concurrent Cloud Run instances."
  type        = number
  default     = 10
}

# NOTE: there is deliberately no variable for the Battle.net client secret.
# Principle V forbids secrets in OpenTofu files or state; it lives in Secret
# Manager and is referenced by version. See README.md.

variable "db_deletion_protection" {
  description = <<-EOT
    Guards the Cloud SQL instance against `tofu destroy`.

    Leave true. The destroy workflow flips it to false in a first apply before
    tearing down, which makes deleting the database an explicit, auditable step
    rather than a side effect of a destroy command.
  EOT
  type        = bool
  default     = true
}
