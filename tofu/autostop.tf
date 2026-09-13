# Scheduled shutdown for non-production environments.
#
# The VM is the entire cost of an environment -- ~$12.23/month of a ~$17 bill,
# with the database's own disk at 40 cents -- so an idle develop environment is
# almost all waste. A Compute Engine instance schedule stops it on a cron and
# costs nothing itself: Google runs it, there is no Cloud Scheduler job, no
# function, and nothing on the VM to rot.
#
# Stopping rather than destroying is deliberate. Caddy's certificate lives in
# the caddy-data volume on the boot disk, so destroying the VM means a fresh
# Let's Encrypt issuance on every rebuild -- and LE allows only five duplicate
# certificates per name per week. Cycle develop more than that and it comes back
# with no working TLS until the window clears. Stopping keeps the disk, the
# certificate, and the reserved address, so it comes back exactly as it was.
#
# Starting is on demand: `dev-lifecycle.yml`, or any deploy, which starts the VM
# if it is stopped. There is deliberately no start schedule -- an environment
# that switches itself on every morning whether or not anyone is working is the
# cost problem this file exists to solve.

data "google_project" "current" {}

resource "google_compute_resource_policy" "auto_stop" {
  # Two conditions, and the first is not redundant. `enable_auto_stop` is an
  # ordinary variable and could be passed true for production by a typo in a
  # workflow; `local.is_production` cannot be. Production must never acquire a
  # policy that switches the site off overnight, so the guard is structural.
  count = !local.is_production && var.enable_auto_stop ? 1 : 0

  name   = "${local.name}-auto-stop"
  region = var.region

  instance_schedule_policy {
    time_zone = var.auto_stop_timezone

    vm_stop_schedule {
      schedule = var.auto_stop_schedule
    }
  }
}

# Instance schedules are executed by the Compute Engine Service Agent, which
# cannot stop an instance without this role. Without the binding the policy is
# created and attached successfully and then silently never fires, which is a
# worse failure than an error: the bill just stays high.
#
# Managed only by the environment that uses schedules, so the two workspaces
# never fight over the same project-level binding.
resource "google_project_iam_member" "compute_agent_scheduler" {
  count = !local.is_production && var.enable_auto_stop ? 1 : 0

  project = var.project_id
  role    = "roles/compute.instanceAdmin.v1"
  member  = "serviceAccount:service-${data.google_project.current.number}@compute-system.iam.gserviceaccount.com"
}
