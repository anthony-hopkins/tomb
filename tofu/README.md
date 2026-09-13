# Infrastructure (OpenTofu)

Every Google Cloud resource for the TOMB platform is defined here. Per
constitution Principle V, changes made directly in the Cloud Console are
forbidden — the only exceptions are the bootstrap steps below, which no API can
perform.

## Running `tofu` on this machine

`tofu` (v1.12.6) lives in the WSL Ubuntu distro at `~/.local/bin/tofu`, **not** on
the Windows PATH. Run it from there; WSL sees this repository at `/mnt/c/...`:

```sh
wsl -e bash -lc 'cd /mnt/c/Users/antho/development/WOW/Guilds/tomb/tofu && tofu plan'
```

Or open a WSL shell and work in that directory directly. Note that `docker` is
the reverse — it works from Windows but not from WSL — so infrastructure and
container work happen in different shells on this machine.

## One-time bootstrap (manual, and deliberately so)

These three steps precede the first `tofu apply`. Each is something OpenTofu
cannot do for itself, which is exactly the exception Principle V allows.

1. **Create the state bucket.** The GCS backend in `versions.tf` must exist
   before `tofu init` can use it:

   ```sh
   gcloud storage buckets create gs://tomb-platform-tfstate \
     --project PROJECT_ID --location US --uniform-bucket-level-access
   gcloud storage buckets update gs://tomb-platform-tfstate --versioning
   ```

2. **Register the Battle.net application** at <https://develop.battle.net>, note
   the client id and secret, and add `https://YOUR_URL/auth/callback` as a
   redirect URI. The URL comes from the `service_url` output, so the first apply
   uses a placeholder and the redirect URI is added afterwards.

2. **Store the client secret.** OpenTofu creates the secret *container*; the
   value is added out of band so it never enters a `.tf` file or state:

   ```sh
   printf '%s' "$BNET_CLIENT_SECRET" | \
     gcloud secrets versions add tomb-platform-bnet-client-secret --data-file=-
   ```

Also enable the APIs this configuration uses, once per project:

```sh
gcloud services enable run.googleapis.com sqladmin.googleapis.com \
  secretmanager.googleapis.com artifactregistry.googleapis.com \
  servicenetworking.googleapis.com compute.googleapis.com --project PROJECT_ID
```

## Deployed by GitHub Actions

Day to day you should not run these commands at all — `deploy.yml` does it,
gated on an approval. See [../docs/deployment.md](../docs/deployment.md). The
manual sequence below is the fallback, and what the pipeline is doing under the
hood.

Note the backend is a *partial* configuration, so every `tofu init` needs the
bucket:

```sh
tofu init -backend-config="bucket=YOUR_STATE_BUCKET"
```

## First deployment, by hand

The first apply has two circular dependencies, so it cannot be a single
`tofu apply`. Both resolve on a second pass.

1. **Cloud Run needs an image, but Artifact Registry does not exist yet.**
2. **The Battle.net redirect URL needs the Cloud Run URL, which does not exist
   until Cloud Run is created.**

Run it in this order.

### Phase 1 — authenticate and enable the project

```sh
gcloud auth login
gcloud auth application-default login    # this is what OpenTofu reads
gcloud config set project PROJECT_ID

gcloud services enable run.googleapis.com sqladmin.googleapis.com secretmanager.googleapis.com artifactregistry.googleapis.com servicenetworking.googleapis.com compute.googleapis.com
```

### Phase 2 — state bucket, then registry only

```sh
gcloud storage buckets create gs://tomb-platform-tfstate --location US --uniform-bucket-level-access
gcloud storage buckets update gs://tomb-platform-tfstate --versioning

cp terraform.tfvars.example terraform.tfvars   # gitignored; fill it in
tofu init

# Create just the registry, so there is somewhere to push to.
tofu apply -target=google_artifact_registry_repository.platform
```

### Phase 3 — build and push the image

Docker runs from **Windows** on this machine, not WSL:

```sh
IMAGE="us-central1-docker.pkg.dev/PROJECT_ID/tomb/platform:$(git rev-parse --short HEAD)"
gcloud auth configure-docker us-central1-docker.pkg.dev
docker build -t "$IMAGE" .
docker push "$IMAGE"
```

Put that exact tag in `terraform.tfvars` as `image`. Tags are immutable in this
registry, so always push a new tag rather than overwriting.

### Phase 4 — full apply

```sh
tofu plan -out=tfplan      # review this; Principle V requires it
tofu apply tfplan
```

Cloud SQL takes 10-15 minutes to create on the first run.

### Phase 5 — close the redirect-URL loop

```sh
tofu output service_url
```

Set that value as `public_url` in `terraform.tfvars`, register
`<service_url>/auth/callback` as a redirect URI in the Battle.net developer
portal, store the client secret, and apply once more:

```sh
printf '%s' "$BNET_CLIENT_SECRET" | gcloud secrets versions add tomb-platform-bnet-client-secret --data-file=-

tofu plan -out=tfplan && tofu apply tfplan
```

Then check it is alive:

```sh
curl -fsS "$(tofu output -raw service_url)/healthz"   # -> ok
curl -fsS "$(tofu output -raw service_url)/readyz"    # -> ok
```

`/readyz` returning 503 means the container is up but cannot reach Cloud SQL --
check the `cloudsql` volume mount and the `roles/cloudsql.client` binding.

## Subsequent deployments

```sh
docker build -t "$IMAGE" . && docker push "$IMAGE"   # new tag
# update `image` in terraform.tfvars
tofu plan -out=tfplan
tofu apply tfplan
```

Per the constitution's Development Workflow, any change under `tofu/` must
include the `tofu plan` output in the pull request, and `apply` runs only after
that plan is approved. CI runs `fmt` and `validate` but cannot produce a real
plan until Workload Identity Federation is configured.

## Sizing and what it costs

Defaults are set for a moderate production posture, not the cheapest possible
one. Every value is a variable, so dial it in `terraform.tfvars`.

| Resource | Default | Monthly, roughly |
|---|---|---|
| Cloud SQL `db-custom-1-3840` (1 vCPU, 3.75 GB, zonal) | `db_tier` | **~$52** |
| Cloud SQL disk, 20 GB PD_SSD | `db_disk_size` | ~$3 |
| Cloud Run, 2 vCPU / 1 GiB, scales to zero | `run_cpu`, `run_memory` | ~$0-2 |
| Artifact Registry, Secret Manager | -- | under $1 |
| | | **~$55-60** |

**The database is the entire bill.** Cloud Run scales to zero and bills only
while serving a request, so 2 vCPU there costs almost nothing at guild traffic;
Cloud SQL runs 24/7 whether anyone visits or not.

If ~$55/month is more than a guild site should cost, `db_tier` is the one knob
that matters:

```hcl
db_tier = "db-g1-small"   # shared core, 1.7 GB, ~$27/month
db_tier = "db-f1-micro"   # shared core, 0.6 GB, ~$9/month -- dev only
```

The shared-core tiers carry no production SLA and cannot be made highly
available, which is why they are not the default. For two small tables holding
users and sessions they would work fine in practice.

## What gets created

| File | Resources |
|---|---|
| `network.tf` | VPC plus private service networking, so Cloud SQL needs no public IP |
| `database.tf` | Cloud SQL for PostgreSQL 18, the `tomb` database and user, and the connection string in Secret Manager |
| `registry.tf` | Artifact Registry Docker repository with immutable tags |
| `secrets.tf` | The Battle.net client-secret container, the runtime service account, and its IAM bindings |
| `run.tf` | The Cloud Run service, its environment, probes, and public invoker binding |

## Notes worth knowing

- **Postgres major version is pinned twice**, here and in `compose.yaml`
  (`postgres:18`). Change both together or local and production drift apart.
- **`deletion_protection = true`** on the SQL instance. A `tofu destroy` will
  refuse until it is turned off deliberately — member records are not worth an
  accidental teardown.
- **No secret values in state.** The database password is generated by
  `random_password` and written straight to Secret Manager; no output exposes
  it. The Battle.net secret is never in state at all.
- **`SESSION_COOKIE_SECURE` is hardcoded `true`** in `run.tf`. Cloud Run
  terminates TLS, so there is no reason for it to be anything else; only local
  plain-HTTP development sets it false.
- **The service is publicly invokable.** The guild gate (FR-013a) lives in the
  application, not in IAM, because members sign in with Battle.net rather than
  Google identities.
