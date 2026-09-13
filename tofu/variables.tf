variable "project_id" {
  description = "Google Cloud project that hosts the TOMB platform."
  type        = string
}

variable "region" {
  description = "Google Cloud region."
  type        = string
  default     = "us-central1"
}

variable "zone" {
  description = "Zone for the VM. Must be inside var.region."
  type        = string
  default     = "us-central1-a"
}

variable "service_name" {
  description = "Name prefix for the platform's resources."
  type        = string
  default     = "tomb-platform"
}

variable "image" {
  description = <<-EOT
    Fully qualified container image to run, e.g.
    us-central1-docker.pkg.dev/PROJECT/tomb/platform:GIT_SHA.
    The same image built for local development is promoted here (Principle IV).
  EOT
  type        = string
}

# ---------------------------------------------------------------------------
# Compute
#
# The whole stack -- app, Postgres and Caddy -- runs as a Compose project on a
# single VM. Postgres is self-hosted rather than Cloud SQL: the database holds
# two small tables (users and sessions) because FR-016 fetches character data
# live, so a managed instance at roughly $52/month bought very little for a
# guild site.
# ---------------------------------------------------------------------------

variable "machine_type" {
  description = <<-EOT
    VM machine type. Costs below are us-central1 on-demand, derived from the
    Cloud Billing Catalog (E2 core $0.021811590/vCPU-hour, RAM
    $0.002923530/GiB-hour):

      e2-micro   0.25 vCPU, 1 GiB   ~$6.11/month   (free tier eligible; tight)
      e2-small   0.5 vCPU,  2 GiB   ~$12.23/month  <- current default
      e2-medium  1 vCPU,    4 GiB   ~$24.48/month
      e2-standard-2  2 vCPU, 8 GiB  ~$48.96/month

    e2-small comfortably runs a Go binary plus a tuned Postgres for one guild.
    Move to e2-medium if more apps land on the platform.
  EOT
  type        = string
  default     = "e2-small"
}

variable "boot_disk_size" {
  description = "Boot disk in GB. Holds the OS and container images."
  type        = number
  default     = 20
}

variable "data_disk_size" {
  description = <<-EOT
    Size of the separate persistent disk holding the Postgres data directory.

    Separate from the boot disk on purpose: the VM can be recreated, resized or
    reimaged without touching member records, and the disk survives with
    prevent_destroy.
  EOT
  type        = number
  default     = 10
}

variable "disk_type" {
  description = <<-EOT
    "pd-standard" or "pd-balanced". pd-standard is what the always-free tier
    covers (30 GB); pd-balanced is faster and cheap at this size. Two small
    tables do not need SSD.
  EOT
  type        = string
  default     = "pd-standard"
}

# ---------------------------------------------------------------------------
# Networking and TLS
# ---------------------------------------------------------------------------

variable "domain" {
  description = <<-EOT
    Hostname the site is served on, e.g. "tomb.example". Caddy obtains a
    Let's Encrypt certificate for it automatically.

    Leave empty and the stack falls back to "<dashed-ip>.sslip.io", which is
    real public DNS resolving to the VM's own address, so TLS still works and
    you are not blocked on buying a domain. Point a real domain here when you
    have one: sslip.io is shared infrastructure and subject to Let's Encrypt's
    per-registered-domain rate limits.

    HTTPS is not optional. Blizzard rejects a plain-HTTP redirect URI for a
    non-localhost callback, and the app issues Secure cookies.
  EOT
  type        = string
  default     = ""
}

variable "acme_email" {
  description = <<-EOT
    Contact address Let's Encrypt uses for expiry warnings. Empty is allowed;
    Caddy then registers without one.
  EOT
  type        = string
  default     = ""
}

variable "ssh_source_ranges" {
  description = <<-EOT
    CIDR ranges allowed to reach port 22 directly. Empty by default: SSH goes
    through Identity-Aware Proxy instead, so the VM exposes no public SSH.
  EOT
  type        = list(string)
  default     = []
}

# ---------------------------------------------------------------------------
# Application configuration
# ---------------------------------------------------------------------------

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
    Public base URL used to build the Battle.net OAuth redirect URL.

    Empty means derive it from var.domain, or from the sslip.io fallback. Set it
    explicitly only if the site is reached through something this configuration
    does not know about, such as a CDN in front of the VM.
  EOT
  type        = string
  default     = ""
}

# NOTE: there is deliberately no variable for the Battle.net client secret.
# Principle V forbids secrets in OpenTofu files or state; it lives in Secret
# Manager and the VM reads it at boot. See README.md.
