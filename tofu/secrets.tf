# The Battle.net client secret. Principle V: secret *values* are never
# committed here or stored in state.
#
# Create the container with OpenTofu, then add the value once, out of band:
#
#   printf '%s' "$BNET_CLIENT_SECRET" | \
#     gcloud secrets versions add tomb-platform-bnet-client-secret --data-file=-
#
# That is the documented bootstrap exception: the value originates in Blizzard's
# developer portal and cannot be provisioned by any API.

resource "google_secret_manager_secret" "bnet_client_secret" {
  secret_id = "${var.service_name}-bnet-client-secret"

  replication {
    auto {}
  }
}

# Service account for the Cloud Run service, with only the access it needs.
resource "google_service_account" "run" {
  account_id   = "${var.service_name}-run"
  display_name = "TOMB platform Cloud Run service account"
}

resource "google_secret_manager_secret_iam_member" "bnet_client_secret" {
  secret_id = google_secret_manager_secret.bnet_client_secret.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.run.email}"
}

resource "google_secret_manager_secret_iam_member" "database_url" {
  secret_id = google_secret_manager_secret.database_url.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.run.email}"
}

resource "google_project_iam_member" "run_sql_client" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = "serviceAccount:${google_service_account.run.email}"
}
