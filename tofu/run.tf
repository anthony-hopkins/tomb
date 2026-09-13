# The Cloud Run service. Configuration arrives strictly as environment
# variables, so the same image serves every environment (Principle IV).

resource "google_cloud_run_v2_service" "platform" {
  name     = var.service_name
  location = var.region

  # Cloud Run manages TLS, so the app always sets Secure cookies here.
  ingress = "INGRESS_TRAFFIC_ALL"

  template {
    service_account = google_service_account.run.email

    scaling {
      min_instance_count = 0
      max_instance_count = 4
    }

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.main.connection_name]
      }
    }

    containers {
      image = var.image

      ports {
        container_port = 8080
      }

      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }

      env {
        name  = "BNET_REGION"
        value = var.bnet_region
      }
      env {
        name  = "BNET_CLIENT_ID"
        value = var.bnet_client_id
      }
      env {
        name  = "BNET_REDIRECT_URL"
        value = "${var.public_url}/auth/callback"
      }
      env {
        name  = "TOMB_GUILD_NAME"
        value = var.guild_name
      }
      env {
        name  = "TOMB_GUILD_REALM"
        value = var.guild_realm
      }
      env {
        # Always true off localhost. The app refuses to downgrade unless this
        # is an explicit "false".
        name  = "SESSION_COOKIE_SECURE"
        value = "true"
      }

      env {
        name = "BNET_CLIENT_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.bnet_client_secret.secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "DATABASE_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.database_url.secret_id
            version = "latest"
          }
        }
      }

      # A dashboard view fans out to Blizzard (1 + N calls), so the probes are
      # generous enough not to kill a container mid-fetch.
      startup_probe {
        http_get {
          path = "/healthz"
        }
        initial_delay_seconds = 5
        timeout_seconds       = 3
        failure_threshold     = 6
      }

      liveness_probe {
        http_get {
          path = "/healthz"
        }
        period_seconds  = 30
        timeout_seconds = 3
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }
    }

    timeout = "60s"
  }

  traffic {
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
    percent = 100
  }

  depends_on = [
    google_secret_manager_secret_iam_member.bnet_client_secret,
    google_secret_manager_secret_iam_member.database_url,
  ]
}

# The guild site is public; the app's own guild gate decides who sees what.
resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.platform.name
  location = google_cloud_run_v2_service.platform.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}
