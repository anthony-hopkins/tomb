# Container images. The same image promoted from local development is deployed
# to Cloud Run (Principle IV).

resource "google_artifact_registry_repository" "platform" {
  location      = var.region
  repository_id = "tomb"
  description   = "Container images for the TOMB guild platform."
  format        = "DOCKER"

  docker_config {
    immutable_tags = true
  }
}
