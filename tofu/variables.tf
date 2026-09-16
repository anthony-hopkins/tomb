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
  default     = 20  # was 10; combat logs wait here between upload and parse (spec 003)
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
# Backups
# ---------------------------------------------------------------------------

variable "enable_auto_stop" {
  description = <<-EOT
    Whether this environment stops itself on a schedule.

    False in production, and structurally impossible there regardless -- see the
    guard in autostop.tf. True in develop, where the VM is the whole cost and an
    environment nobody is using should not be running.
  EOT
  type        = bool
  default     = false
}

variable "auto_stop_schedule" {
  description = <<-EOT
    Cron expression for the scheduled shutdown, in var.auto_stop_timezone.

    Default is 02:00 every day. Deliberately no matching start schedule: an
    environment that switches itself on each morning, used or not, defeats the
    purpose. Deploys and dev-lifecycle.yml start it on demand.
  EOT
  type        = string
  default     = "0 2 * * *"
}

variable "auto_stop_timezone" {
  description = <<-EOT
    IANA time zone the auto-stop cron is interpreted in. UTC by default so the
    schedule does not shift under daylight saving; set e.g. "America/Chicago" if
    you would rather it tracked local evenings.
  EOT
  type        = string
  default     = "UTC"
}

variable "enable_snapshots" {
  description = <<-EOT
    Whether to take daily snapshots of the Postgres data disk.

    True in production, where the disk is the only irreplaceable state in the
    project. False in develop, whose rows are re-created by the next sign-in and
    are not worth paying to retain.
  EOT
  type        = bool
  default     = true
}

variable "snapshot_retention_days" {
  description = <<-EOT
    How long automated snapshots of the Postgres data disk are kept.

    Fourteen days is deliberately longer than a weekend plus a holiday: the
    realistic failure is a bad migration or a wrong DELETE noticed days later,
    not a disk dying. Snapshots are incremental, and the disk holds two small
    tables, so the storage cost of a fortnight is cents per month.
  EOT
  type        = number
  default     = 14

  validation {
    condition     = var.snapshot_retention_days >= 1 && var.snapshot_retention_days <= 365
    error_message = "snapshot_retention_days must be between 1 and 365."
  }
}

variable "snapshot_start_time" {
  description = <<-EOT
    UTC time of the daily snapshot, "HH:MM" on a 15-minute boundary. Default
    09:00 UTC is the quiet early morning for a US guild.
  EOT
  type        = string
  default     = "09:00"

  validation {
    condition     = can(regex("^([01][0-9]|2[0-3]):(00|15|30|45)$", var.snapshot_start_time))
    error_message = "snapshot_start_time must be HH:MM in UTC on a 15-minute boundary, e.g. \"09:00\"."
  }
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
  description = <<-EOT
    The slug of the realm the guild was FOUNDED on, lowercase and hyphenated
    (FR-013) -- not the realm its members play on.

    These are routinely different. On a connected-realm cluster a guild keeps
    the realm it was created on while its members' characters sit on any realm
    in the group: TOMB is registered on "elune" and its members play on Area 52.
    Blizzard reports the guild's own realm on each character profile, so that is
    what membership is checked against.

    Setting this to the realm you play on refuses every member, and does it in a
    way that is indistinguishable from nobody being in the guild. If that
    happens, the app now says so outright -- look for "guild name matched but
    realm did not" in the logs, which prints the value to use.
  EOT
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

# ---------------------------------------------------------------------------
# Optional application configuration.
#
# Each of these reaches the container as the environment variable of the same
# name, by way of instance metadata and deploy/configure.sh. Empty -- the
# default -- means the variable is passed through empty, and the application
# reads empty as "use my default". So an unset repository variable changes
# nothing, and setting one is the whole of enabling it.
#
# The set here, the metadata keys in compute.tf, the lines in configure.sh and
# the entries in compose.yaml are held to agree by a test
# (internal/platform/plumbing_test.go): a value the app reads that is missing
# from any hop is a variable that silently does nothing, which is how
# TOMB_GUILD_RANKS shipped documented and unplumbed.
# ---------------------------------------------------------------------------

variable "guild_ranks" {
  description = "Rank names, most senior first, comma-separated. Empty leaves ranks numbered. Reaches the app as TOMB_GUILD_RANKS."
  type        = string
  default     = ""
}

variable "guild_roster_ttl" {
  description = "How often the guild page refreshes its roster, as a Go duration. Empty is the app's default of an hour. Reaches the app as TOMB_GUILD_ROSTER_TTL."
  type        = string
  default     = ""
}

variable "guild_officer_rank" {
  description = "The lowest rank index that is still an officer. Empty is the app's default of 1. Reaches the app as TOMB_GUILD_OFFICER_RANK."
  type        = string
  default     = ""
}

variable "admin" {
  description = "The site's administrator: a Battle.net account subject claim, or a battletag. Empty means none. Reaches the app as TOMB_ADMIN."
  type        = string
  default     = ""
}

variable "timezone" {
  description = "The IANA zone every time is shown in. Empty is the app's default, America/New_York. Reaches the app as TOMB_TIMEZONE."
  type        = string
  default     = ""
}

variable "discord_invite" {
  description = "The guild's Discord invite link, shown on the public front door (spec 004). A public https:// link, not a secret. Empty means the page says to ask an officer. Reaches the app as TOMB_DISCORD_INVITE."
  type        = string
  default     = ""
}

# --- Combat logs and the AI comparison (spec 003) --------------------------

variable "upload_dir" {
  description = "Where an uploaded combat log sits until it is parsed. Empty is the app's default, /var/lib/tomb/uploads, which compose.yaml binds to the data disk. Reaches the app as TOMB_UPLOAD_DIR."
  type        = string
  default     = ""
}

variable "ai_model" {
  description = "The Vertex AI model that writes a comparison. Empty is the app's default, gemini-3.1-pro. Reaches the app as TOMB_AI_MODEL."
  type        = string
  default     = ""
}

variable "ai_region" {
  description = "The region the model is called in. Empty is the app's default, us-central1. Reaches the app as TOMB_AI_REGION."
  type        = string
  default     = ""
}

variable "wcl_client_id" {
  description = "Warcraft Logs API client id, read-only use. Not a secret. Empty leaves comparisons unavailable. Reaches the app as WCL_CLIENT_ID; the secret is in Secret Manager."
  type        = string
  default     = ""
}
