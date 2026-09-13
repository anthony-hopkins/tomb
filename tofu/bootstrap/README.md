# Bootstrap

Creates the things that must exist before GitHub Actions can run OpenTofu at
all, so it cannot itself run in that pipeline. Apply it **once, by hand**.

It creates:

- the GCS bucket holding the main configuration's state
- a Workload Identity Federation pool and provider trusting GitHub's OIDC
  issuer, restricted to one repository
- the `tomb-deployer` service account Actions impersonates, and its roles

**No service-account keys are created or downloaded.** GitHub authenticates by
presenting a short-lived OIDC token which Google exchanges for a short-lived
access token. There is nothing to leak and nothing to rotate.

## Apply it

```sh
gcloud auth login
gcloud auth application-default login
gcloud config set project YOUR_PROJECT_ID

cd tofu/bootstrap
cp terraform.tfvars.example terraform.tfvars   # fill in project_id
tofu init
tofu plan -out=tfplan
tofu apply tfplan
```

Then set the repository variables it prints:

```sh
tofu output -raw gh_variable_commands
```

State is kept **local** here (`terraform.tfstate`, gitignored). That avoids a
second chicken-and-egg with the bucket this config creates, and the state is
only needed if you change the trust configuration later. Keep a copy if you
care about that; recreating from scratch is also fine, since every resource
here is importable or idempotent.

## Why the deployer is so privileged

The main configuration creates a VPC, a VM, disks, secrets, a service account
and project-level IAM bindings, and the pipeline SSHes to the VM through IAP to
roll out images. Doing that needs close to project administrator. The mitigations are that the identity has no downloadable key,
can only be assumed by OIDC tokens carrying this repository's claim, and is
additionally constrained by the provider's `attribute_condition`.

If you want to tighten it, the roles are a plain list in `main.tf`
(`local.deployer_roles`). Narrowing them is a matter of replacing the admin
roles with the specific permissions each resource needs, which is worth doing
if this project ever holds anything sensitive.
