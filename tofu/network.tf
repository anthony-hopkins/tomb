# Private networking so Cloud SQL needs no public IP.

resource "google_compute_network" "main" {
  name                    = "${var.service_name}-net"
  auto_create_subnetworks = false
}

resource "google_compute_global_address" "private_ip" {
  name          = "${var.service_name}-private-ip"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.main.id
}

resource "google_service_networking_connection" "main" {
  network                 = google_compute_network.main.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_ip.name]
}
