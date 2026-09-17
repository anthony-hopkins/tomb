# The VM that runs the whole stack as a Compose project: Caddy for TLS, the Go
# app, and Postgres. Replaces the Cloud Run service and the Cloud SQL instance.

locals {
  # Without a real domain, fall back to sslip.io: it resolves <dashed-ip>.sslip.io
  # to that address from public DNS, so Let's Encrypt can validate it and the
  # site gets a genuine certificate rather than a warning page.
  effective_domain = (
    var.domain != ""
    ? var.domain
    : "${replace(google_compute_address.main.address, ".", "-")}.sslip.io"
  )

  effective_public_url = (
    var.public_url != ""
    ? var.public_url
    : "https://${local.effective_domain}"
  )
}

# Runtime identity for the VM. Distinct from the CI deployer: this one may only
# read the two secrets it needs and pull images.
resource "google_service_account" "vm" {
  account_id   = "${local.name}-vm"
  display_name = "TOMB platform VM runtime"
  description  = "Identity of the VM running the Compose stack"
}

resource "google_project_iam_member" "vm_artifact_reader" {
  project = var.project_id
  role    = "roles/artifactregistry.reader"
  member  = "serviceAccount:${google_service_account.vm.email}"
}

# Logs and metrics from the host, so a crash-looping container is visible
# without SSH.
resource "google_project_iam_member" "vm_logging" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.vm.email}"
}

resource "google_project_iam_member" "vm_metrics" {
  project = var.project_id
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.vm.email}"
}

# Vertex AI, for the combat-log comparison's write-up (spec 003). The VM
# calls it as itself -- the cloud-platform scope below plus this role -- so
# there is no API key anywhere. The API is project-wide and both environments
# live in one project, so each workspace enables it and neither disables it on
# destroy: tearing down develop must not switch production's model off.
resource "google_project_service" "aiplatform" {
  project            = var.project_id
  service            = "aiplatform.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_iam_member" "vm_aiplatform" {
  project = var.project_id
  role    = "roles/aiplatform.user"
  member  = "serviceAccount:${google_service_account.vm.email}"
}

# ---------------------------------------------------------------------------
# Postgres data disk.
#
# Separate from the boot disk so the VM can be recreated, resized or reimaged
# without touching member records.
# ---------------------------------------------------------------------------

resource "google_compute_disk" "data" {
  name = "${local.name}-data"
  type = var.disk_type
  zone = var.zone
  size = var.data_disk_size

  lifecycle {
    # The one piece of genuinely irreplaceable state in the whole project.
    # Removing this guard is a deliberate act, not a side effect of a refactor.
    prevent_destroy = true
  }
}

resource "google_compute_instance" "main" {
  name         = local.name
  machine_type = var.machine_type
  zone         = var.zone

  tags = ["tomb-web"]

  boot_disk {
    initialize_params {
      # Ubuntu LTS rather than Container-Optimized OS: COS ships Docker but not
      # the Compose plugin, and running this stack with Compose is the point.
      image = "ubuntu-os-cloud/ubuntu-2404-lts-amd64"
      size  = var.boot_disk_size
      type  = var.disk_type
    }
  }

  attached_disk {
    source      = google_compute_disk.data.id
    device_name = "tomb-data"
    mode        = "READ_WRITE"
  }

  network_interface {
    subnetwork = google_compute_subnetwork.main.id

    access_config {
      nat_ip = google_compute_address.main.address
    }
  }

  # Empty in production, where the auto-stop policy does not exist.
  resource_policies = google_compute_resource_policy.auto_stop[*].self_link

  service_account {
    email = google_service_account.vm.email
    # cloud-platform scoped, then narrowed by the IAM roles above. Finer scopes
    # are legacy and interact badly with Secret Manager.
    scopes = ["https://www.googleapis.com/auth/cloud-platform"]
  }

  metadata = {
    # Consumed by the startup script. Changing these re-runs configuration on
    # the next boot; the deploy workflow restarts the stack directly rather
    # than waiting for a reboot.
    tomb-image          = var.image
    tomb-domain         = local.effective_domain
    tomb-public-url     = local.effective_public_url
    tomb-bnet-region    = var.bnet_region
    tomb-bnet-client-id = var.bnet_client_id
    tomb-guild-name     = var.guild_name
    tomb-guild-realm    = var.guild_realm
    tomb-acme-email     = var.acme_email

    # Optional application configuration; empty means the app's default.
    tomb-guild-ranks        = var.guild_ranks
    tomb-guild-roster-ttl   = var.guild_roster_ttl
    tomb-guild-officer-rank = var.guild_officer_rank
    tomb-timezone           = var.timezone
    tomb-admin              = var.admin
    tomb-discord-invite     = var.discord_invite
    tomb-upload-dir         = var.upload_dir
    tomb-ai-model           = var.ai_model
    tomb-ai-region          = var.ai_region
    tomb-ai-assistant-model = var.ai_assistant_model
    tomb-wcl-client-id      = var.wcl_client_id
    tomb-db-secret          = google_secret_manager_secret.db_password.secret_id
    tomb-bnet-secret        = google_secret_manager_secret.bnet_client_secret.secret_id
    tomb-wcl-secret         = google_secret_manager_secret.wcl_client_secret.secret_id

    # OS Login so the pipeline authenticates with IAM rather than managed keys.
    enable-oslogin = "TRUE"

    startup-script = file("${path.module}/../deploy/startup.sh")
  }

  # Compose files are fetched by the startup script from this repository's
  # published image, so the VM needs no copy of the repo.

  allow_stopping_for_update = true

  lifecycle {
    # The image tag lives in metadata and is applied by the deploy workflow
    # over SSH, so a metadata change alone must not force a VM replacement.
    ignore_changes = [metadata["tomb-image"]]
  }

  depends_on = [
    google_project_iam_member.vm_artifact_reader,
    google_secret_manager_secret_iam_member.vm_bnet_client_secret,
    google_secret_manager_secret_iam_member.vm_db_password,
  ]
}
