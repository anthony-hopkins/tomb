# Networking for a single-VM deployment.
#
# Simpler than the Cloud SQL design this replaced: with Postgres running as a
# container on the same host, there is no private services access, no VPC
# peering and no serverless connector. The database is reachable only over the
# Compose network and never leaves the VM.

resource "google_compute_network" "main" {
  name                    = "${local.name}-net"
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "main" {
  name          = "${local.name}-subnet"
  network       = google_compute_network.main.id
  region        = var.region
  ip_cidr_range = "10.10.0.0/24"

  # Lets the VM reach Google APIs (Secret Manager, Artifact Registry) without
  # a public route when one is not otherwise needed.
  private_ip_google_access = true
}

# HTTP is open only so Caddy can answer the ACME HTTP-01 challenge and redirect
# everything else to HTTPS.
resource "google_compute_firewall" "web" {
  name    = "${local.name}-allow-web"
  network = google_compute_network.main.name

  allow {
    protocol = "tcp"
    ports    = ["80", "443"]
  }

  source_ranges = ["0.0.0.0/0"]
  target_tags   = ["tomb-web"]
}

# SSH from Identity-Aware Proxy only. 35.235.240.0/20 is Google's fixed IAP
# range, so the VM exposes no SSH to the internet and the deploy pipeline
# tunnels in with its own credentials.
resource "google_compute_firewall" "ssh_iap" {
  name    = "${local.name}-allow-ssh-iap"
  network = google_compute_network.main.name

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  source_ranges = ["35.235.240.0/20"]
  target_tags   = ["tomb-web"]
}

# Optional direct SSH, off unless ssh_source_ranges is set.
resource "google_compute_firewall" "ssh_direct" {
  count = length(var.ssh_source_ranges) > 0 ? 1 : 0

  name    = "${local.name}-allow-ssh-direct"
  network = google_compute_network.main.name

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  source_ranges = var.ssh_source_ranges
  target_tags   = ["tomb-web"]
}

# A reserved address so the DNS record (or the sslip.io hostname) stays valid
# across VM recreation. Without this, replacing the VM changes the public IP and
# silently breaks both DNS and the registered OAuth redirect URI.
resource "google_compute_address" "main" {
  name   = "${local.name}-ip"
  region = var.region
}
