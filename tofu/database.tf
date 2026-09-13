# Cloud SQL for PostgreSQL. Only users and sessions are stored: FR-016 fetches
# character data live on every view, so none of it is persisted (data-model.md).

resource "google_sql_database_instance" "main" {
  name   = "${var.service_name}-db"
  region = var.region
  # Pinned to the same major version as compose.yaml's postgres:18, so local
  # development and production cannot drift apart.
  database_version = "POSTGRES_18"

  settings {
    # Explicit: PostgreSQL 16+ otherwise defaults to ENTERPRISE_PLUS, whose
    # tier family does not include db-custom-*. See var.db_edition.
    edition           = var.db_edition
    tier              = var.db_tier
    availability_type = var.db_availability_type
    disk_size         = var.db_disk_size
    disk_autoresize   = true
    disk_type         = "PD_SSD"

    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
      start_time                     = "09:00"
    }

    ip_configuration {
      # Reached over the Cloud SQL socket from Cloud Run, so no public IP.
      ipv4_enabled = false
      # Requires the service networking connection created in network.tf.
      private_network = google_compute_network.main.id
    }

    database_flags {
      name  = "log_min_duration_statement"
      value = "1000"
    }
  }

  # A guild site is not worth an accidental `tofu destroy` of member records.
  deletion_protection = var.db_deletion_protection

  # Catch an edition/tier mismatch during plan instead of a minute into an
  # apply, after the VPC and the private service connection already exist.
  lifecycle {
    precondition {
      condition = (
        var.db_edition == "ENTERPRISE_PLUS"
        ? startswith(var.db_tier, "db-perf-optimized-")
        : !startswith(var.db_tier, "db-perf-optimized-")
      )
      error_message = <<-EOT
        db_tier "${var.db_tier}" is not valid for db_edition "${var.db_edition}".

        ENTERPRISE takes shared-core (db-f1-micro, db-g1-small) or
        dedicated-core (db-custom-CPU-MEMORY) tiers.
        ENTERPRISE_PLUS takes only db-perf-optimized-N-* tiers.
      EOT
    }
  }

  depends_on = [google_service_networking_connection.main]
}

resource "google_sql_database" "tomb" {
  name     = "tomb"
  instance = google_sql_database_instance.main.name
}

resource "google_sql_user" "app" {
  name     = "tomb"
  instance = google_sql_database_instance.main.name
  password = random_password.db.result
}

resource "random_password" "db" {
  length  = 32
  special = false
}

# The generated password is written to Secret Manager rather than surfaced as an
# output, so it is not printed in plan or apply logs.
resource "google_secret_manager_secret" "database_url" {
  secret_id = "${var.service_name}-database-url"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "database_url" {
  secret = google_secret_manager_secret.database_url.id
  secret_data = format(
    "postgres://%s:%s@/%s?host=/cloudsql/%s",
    google_sql_user.app.name,
    random_password.db.result,
    google_sql_database.tomb.name,
    google_sql_database_instance.main.connection_name,
  )
}
