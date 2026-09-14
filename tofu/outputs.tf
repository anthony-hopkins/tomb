output "environment" {
  description = <<-EOT
    Which environment this state describes, derived from the OpenTofu workspace.
    Read back by the deploy workflow as a cross-check that the workspace it
    selected is the environment it believes it is deploying to.
  EOT
  value       = local.environment
}

output "service_url" {
  description = "Public URL of the site. Register <this>/auth/callback with Battle.net."
  value       = local.effective_public_url
}

output "domain" {
  description = "Hostname Caddy holds a certificate for."
  value       = local.effective_domain
}

output "public_ip" {
  description = <<-EOT
    Reserved public address of the VM. Point an A record here when you move off
    the sslip.io fallback; the address survives VM recreation.
  EOT
  value       = google_compute_address.main.address
}

output "instance_name" {
  description = "Compute Engine instance name, for gcloud compute ssh."
  value       = google_compute_instance.main.name
}

output "zone" {
  description = "Zone the instance runs in."
  value       = var.zone
}

output "data_disk_name" {
  description = "Postgres data disk. Snapshots of this are the backups."
  value       = google_compute_disk.data.name
}

output "snapshot_policy" {
  description = <<-EOT
    Resource policy taking the daily snapshots of the data disk, or empty in an
    environment where snapshots are disabled (see var.enable_snapshots).
  EOT
  value       = var.enable_snapshots ? google_compute_resource_policy.data_snapshots[0].name : ""
}

output "artifact_registry" {
  description = <<-EOT
    Docker repository images are pushed to. Created by tofu/bootstrap, not by
    this configuration -- see the note in bootstrap/main.tf about the circular
    dependency that caused.
  EOT
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/tomb"
}

output "deployed_image" {
  description = <<-EOT
    The image the VM is configured to run. Read by the deploy workflow so an
    infrastructure-only run can re-apply without rebuilding, rather than
    silently rolling the site back to a stale tag.
  EOT
  value       = google_compute_instance.main.metadata["tomb-image"]
}

# No output exposes the database password or the Battle.net client secret: both
# live only in Secret Manager (Principle V).
