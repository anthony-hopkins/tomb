# Secrets. Principle V: secret *values* are never committed here or stored in
# OpenTofu state.

# ---------------------------------------------------------------------------
# Battle.net client secret.
#
# OpenTofu creates the container; the value is added once, out of band:
#
#   printf '%s' "$BNET_CLIENT_SECRET" | \
#     gcloud secrets versions add tomb-platform-bnet-client-secret --data-file=-
#
# That is the documented bootstrap exception: the value originates in Blizzard's
# developer portal and no API can provision it.
# ---------------------------------------------------------------------------

resource "google_secret_manager_secret" "bnet_client_secret" {
  secret_id = "${var.service_name}-bnet-client-secret"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_iam_member" "vm_bnet_client_secret" {
  secret_id = google_secret_manager_secret.bnet_client_secret.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.vm.email}"
}

# ---------------------------------------------------------------------------
# Postgres password.
#
# Generated here and written straight to Secret Manager. The VM reads it at
# boot to build DATABASE_URL, so the password never appears in metadata, in a
# Compose file, or in a workflow log.
#
# It does land in OpenTofu state, which is why the state bucket is private with
# uniform access and public access prevention enforced. Postgres listens only
# on the Compose network, so this password is not reachable from outside the VM
# in any case.
# ---------------------------------------------------------------------------

resource "random_password" "db" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "db_password" {
  secret_id = "${var.service_name}-db-password"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "db_password" {
  secret      = google_secret_manager_secret.db_password.id
  secret_data = random_password.db.result
}

resource "google_secret_manager_secret_iam_member" "vm_db_password" {
  secret_id = google_secret_manager_secret.db_password.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.vm.email}"
}
