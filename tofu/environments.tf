# How one configuration serves two environments.
#
# Constitution 2.0.0 (Development Workflow) requires develop and production to
# be stood up from the same OpenTofu configuration, differing only in
# parameters. This file is where that difference lives, and nowhere else.
#
# THE WORKSPACE IS THE SOURCE OF TRUTH.
#
# `terraform.workspace` decides both which state file is read and what every
# resource is named. Deriving both from one value is deliberate: if the
# environment were passed in as a variable *alongside* the workspace, the two
# could disagree, and the failure mode of that disagreement is applying develop
# names onto production state. There is no way to express that mistake here.
#
#   workspace `default`  -> production   (the pre-existing state; not renamed,
#                                         because migrating production state to
#                                         a new prefix is a real risk taken for
#                                         a cosmetic gain)
#   workspace `develop`  -> develop
#
# Everything else that differs -- hostname, machine size, whether the data disk
# is snapshotted -- arrives as an ordinary variable from the deploy workflow,
# so it is reviewable in the plan rather than hidden in here.

locals {
  environment = terraform.workspace == "default" ? "production" : terraform.workspace

  # An unknown workspace fails the lookup below, and the plan stops before it
  # can create a half-named third environment nobody asked for.
  environment_settings = {
    production = {
      name_prefix = var.service_name
    }
    develop = {
      name_prefix = "${var.service_name}-develop"
    }
  }

  # Name-length budget. A GCP service account id is capped at 30 characters and
  # compute.tf derives one as "<name_prefix>-vm", which makes it the binding
  # constraint on how long an environment name may be:
  #
  #   tomb-platform-develop-vm   24/30   4 characters of headroom
  #
  # A longer environment name than "develop" fails at apply time, not at plan
  # time, and only on that one resource. Check it here before adding a third.

  env           = local.environment_settings[local.environment]
  name          = local.env.name_prefix
  is_production = local.environment == "production"
}
