# These three values are what GitHub Actions needs. Set them as repository
# variables (not secrets -- none of them is sensitive; they are identifiers,
# and the trust is enforced on Google's side).

output "workload_identity_provider" {
  description = "Set as the GCP_WIF_PROVIDER repository variable."
  value       = google_iam_workload_identity_pool_provider.github.name
}

output "deployer_service_account" {
  description = "Set as the GCP_DEPLOYER_SA repository variable."
  value       = google_service_account.deployer.email
}

output "project_id" {
  description = "Set as the GCP_PROJECT_ID repository variable."
  value       = var.project_id
}

output "artifact_registry" {
  description = "Docker repository the deploy pipeline pushes images to."
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.platform.repository_id}"
}

output "state_bucket" {
  description = "Must match the backend block in ../versions.tf."
  value       = google_storage_bucket.tfstate.name
}

output "gh_variable_commands" {
  description = "Copy-paste to set the repository variables with the gh CLI."
  value = join("\n", [
    "gh variable set GCP_PROJECT_ID  --body '${var.project_id}'",
    "gh variable set GCP_WIF_PROVIDER --body '${google_iam_workload_identity_pool_provider.github.name}'",
    "gh variable set GCP_DEPLOYER_SA --body '${google_service_account.deployer.email}'",
  ])
}
