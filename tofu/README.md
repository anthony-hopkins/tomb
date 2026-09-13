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

## Architecture

The whole stack runs as a Compose project on **one Compute Engine VM**:

```
Compute Engine e2-small (Ubuntu 24.04)
  reserved public IP, ports 80/443 open, SSH via IAP only
  |
  +-- caddy     automatic Let's Encrypt TLS, reverse proxy
  +-- app       the Go binary, from Artifact Registry
  +-- postgres  18, data on a separate persistent disk
```

Postgres is self-hosted rather than Cloud SQL. The database holds two small
tables -- users and sessions -- because FR-016 fetches character data live on
every view, so a managed instance at roughly $52/month bought very little for a
guild site.

The VM configures itself. `deploy/startup.sh` runs on every boot: it installs
Docker, mounts and (only if blank) formats the data disk, reads configuration
from instance metadata, fetches the two secrets from Secret Manager, writes
`/opt/tomb/.env`, and starts the stack. The Compose project and Caddyfile travel
**inside the app image** under `/deploy`, so the VM never needs a checkout of
this repository and a change to `compose.yaml` ships with the code expecting it.

## First deployment, by hand

```sh
cp terraform.tfvars.example terraform.tfvars   # gitignored; fill it in
tofu init -backend-config="bucket=YOUR_STATE_BUCKET"
tofu plan -out=tfplan
tofu apply tfplan
```

Then push an image and roll the VM onto it:

```sh
IMAGE="us-central1-docker.pkg.dev/PROJECT/tomb/platform:$(git rev-parse --short HEAD)"
gcloud auth configure-docker us-central1-docker.pkg.dev
docker build -t "$IMAGE" . && docker push "$IMAGE"

gcloud compute ssh "$(tofu output -raw instance_name)"   --zone "$(tofu output -raw zone)" --tunnel-through-iap   --command "sudo /opt/tomb/deploy.sh '$IMAGE'"
```

Check it answers:

```sh
curl -fsS "$(tofu output -raw service_url)/healthz"   # -> ok
curl -fsS "$(tofu output -raw service_url)/readyz"    # -> ok
```

`/readyz` returning 503 means the app is up but cannot reach Postgres. SSH in and
look at `docker compose logs db`.

## TLS and the hostname

HTTPS is not optional: Blizzard rejects a plain-HTTP OAuth redirect URI, and the
app issues `Secure` cookies. Caddy gets a certificate automatically.

Without a domain, the stack serves on `<dashed-ip>.sslip.io` -- real public DNS
pointing at the VM's own address, so Let's Encrypt can validate it. That gets
you running today. Set `domain` in `terraform.tfvars` (or the `TOMB_DOMAIN`
repository variable) once you have a real name, since sslip.io is shared
infrastructure subject to Let's Encrypt's per-registered-domain rate limits.

The public IP is **reserved**, so it survives VM recreation and a DNS record
pointed at it stays valid.

## Sizing and what it costs

Rates below are us-central1 on-demand, taken from the Cloud Billing Catalog API
(E2 core $0.021811590/vCPU-hour, E2 RAM $0.002923530/GiB-hour).

| Resource | Default | Monthly |
|---|---|---|
| Compute Engine `e2-small` (0.5 vCPU, 2 GiB) | `machine_type` | **~$12.23** |
| Boot disk, 20 GB `pd-standard` | `boot_disk_size` | ~$0.80 |
| Data disk, 10 GB `pd-standard` | `data_disk_size` | ~$0.40 |
| Reserved external IPv4 | -- | ~$3-7 |
| Artifact Registry, Secret Manager | -- | under $1 |
| | | **~$17-21** |

Other machine types, if the guild outgrows 2 GiB:

```hcl
machine_type = "e2-micro"      # 1 GiB,  ~$6.11/mo, free-tier eligible, tight
machine_type = "e2-medium"     # 4 GiB,  ~$24.48/mo
machine_type = "e2-standard-2" # 8 GiB,  ~$48.96/mo
```

Postgres is tuned in `deploy/compose.yaml` for a 2 GiB host shared with the app
and Caddy (`shared_buffers=128MB`, `max_connections=25`). Raise those when you
raise the machine type; the defaults assume a dedicated database host and will
get something OOM-killed here.

## What you now own

Self-hosting trades money for responsibility. On this design you are
responsible for OS patching (`unattended-upgrades` is on by default in Ubuntu
but reboots are yours), Postgres backups, and the fact that this is a single
machine with no failover. The data disk carries `prevent_destroy` and is
separate from the boot disk, so the VM can be rebuilt without touching member
records -- but nothing here takes a backup for you yet. A `gcloud compute disks
snapshot` on a schedule is the obvious next step.

## What gets created

| File | Resources |
|---|---|
| `network.tf` | VPC, subnet, firewall rules (web, IAP SSH), reserved public IP |
| `compute.tf` | The VM, its runtime service account and IAM, and the Postgres data disk |
| `secrets.tf` | Battle.net client-secret container, generated Postgres password, VM accessor bindings |
## Notes worth knowing

- **Postgres major version is pinned in two places**, `deploy/compose.yaml` for
  production and the repository-root `compose.yaml` for local development. Change
  both together or the two environments drift.
- **The data disk carries `prevent_destroy`.** `tofu destroy` will refuse while
  that guard is in place, which is the point: it is the only irreplaceable state
  in the project. Removing it is three deliberate steps, documented in the
  destroy workflow's run summary.
- **No secret values in state, with one exception.** The Battle.net client
  secret never enters state at all. The generated Postgres password does, which
  is why the state bucket is private with uniform access and public access
  prevention enforced -- and Postgres listens only on the Compose network, so
  that password is not reachable from outside the VM regardless.
- **`SESSION_COOKIE_SECURE` is `true`** in `deploy/compose.yaml`. Caddy
  terminates TLS, so the browser is always on HTTPS even though the hop from
  Caddy to the app is plaintext inside the host.
- **SSH is not exposed.** Port 22 is open only to Google's IAP range
  (`35.235.240.0/20`); the pipeline tunnels through it with its own credentials.
  Set `ssh_source_ranges` if you want direct access from your own address.
- **The guild gate lives in the application** (FR-013a), not in IAM, because
  members sign in with Battle.net rather than Google identities.
