output "service_url" {
  description = "Public URL of the Cloud Run service. Use this to set var.public_url and the Battle.net redirect URI."
  value       = google_cloud_run_v2_service.platform.uri
}

output "artifact_registry" {
  description = <<-EOT
    Docker repository images are pushed to. Created by tofu/bootstrap, not by
    this configuration -- see the note in bootstrap/main.tf about the circular
    dependency that caused.
  EOT
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/tomb"
}

output "sql_instance_name" {
  description = "Cloud SQL instance name, used by the destroy workflow to take a final backup."
  value       = google_sql_database_instance.main.name
}

output "deployed_image" {
  description = <<-EOT
    The image Cloud Run is currently serving. The deploy workflow reads this so
    an infrastructure-only run can re-apply without rebuilding, rather than
    silently rolling the service back to a stale tag.
  EOT
  value       = google_cloud_run_v2_service.platform.template[0].containers[0].image
}

output "sql_connection_name" {
  description = "Cloud SQL instance connection name."
  value       = google_sql_database_instance.main.connection_name
}

# No output exposes the database password or the client secret: both live only
# in Secret Manager (Principle V).
