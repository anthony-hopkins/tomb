# Automated snapshots of the Postgres data disk.
#
# Self-hosting the database means owning its backups. This is the piece that
# was missing: the disk carried prevent_destroy, which stops OpenTofu deleting
# it, but that is not a backup -- it does nothing about disk failure, a bad
# migration, or someone truncating a table.
#
# Google takes the snapshots on a schedule, incrementally, so nothing runs on
# the VM and there is no cron job to rot. Snapshots of a running Postgres are
# crash-consistent rather than application-consistent, which is genuinely
# restorable: Postgres treats it exactly like recovering from a power cut and
# replays its WAL on start. See the restore procedure in README.md.

resource "google_compute_resource_policy" "data_snapshots" {
  # Parameterised rather than hardcoded to production: develop's database holds
  # throwaway rows re-created by the next sign-in, so paying to snapshot it
  # daily buys nothing. The policy is identical wherever it IS enabled.
  count = var.enable_snapshots ? 1 : 0

  name   = "${local.name}-data-snapshots"
  region = var.region

  snapshot_schedule_policy {
    schedule {
      daily_schedule {
        days_in_cycle = 1
        # UTC. Early morning UTC is the quiet end of a US guild's evening.
        start_time = var.snapshot_start_time
      }
    }

    retention_policy {
      max_retention_days = var.snapshot_retention_days

      # Keep the snapshots when the disk goes away -- which is the one moment
      # they matter most. A destroy or an accidental disk deletion must not
      # take the backups with it.
      on_source_disk_delete = "KEEP_AUTO_SNAPSHOTS"
    }

    snapshot_properties {
      # Same region as the disk: cheaper than multi-region and sufficient,
      # since this protects against disk and operator failure rather than a
      # regional outage.
      storage_locations = [var.region]

      # guest_flush would quiesce the filesystem for an application-consistent
      # snapshot, but it needs a guest agent and application hooks that are not
      # set up here. Crash-consistent plus Postgres WAL replay is the honest,
      # working choice.
      guest_flush = false

      labels = {
        component   = "tomb-platform"
        contents    = "postgres-data"
        environment = local.environment
      }
    }
  }
}

resource "google_compute_disk_resource_policy_attachment" "data" {
  count = var.enable_snapshots ? 1 : 0

  name = google_compute_resource_policy.data_snapshots[0].name
  disk = google_compute_disk.data.name
  zone = var.zone
}
