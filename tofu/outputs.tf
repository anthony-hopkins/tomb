output "service_url" {
  description = "Public URL of the Cloud Run service. Use this to set var.public_url and the Battle.net redirect URI."
  value       = google_cloud_run_v2_service.platform.uri
}

output "artifact_registry" {
  description = "Docker repository to push images to."
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.platform.repository_id}"
}

output "sql_connection_name" {
  description = "Cloud SQL instance connection name."
  value       = google_sql_database_instance.main.connection_name
}

# No output exposes the database password or the client secret: both live only
# in Secret Manager (Principle V).
