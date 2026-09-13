# Deployment pipeline

Infrastructure and the application are both deployed by GitHub Actions running
OpenTofu against Google Cloud. Authentication is keyless — there is no
service-account JSON key anywhere, in GitHub secrets or otherwise.

## The four workflows

| Workflow | Trigger | What it does |
|---|---|---|
| `ci.yml` | pull requests only | gofmt, build, vet, `go test -race`, Docker build |
| `infra-plan.yml` | PR touching `tofu/**` | `tofu plan`, posted as a PR comment. Never applies. |
| `deploy.yml` | push to `main` builds only; **manual run applies** | test → build and push image → `tofu apply` → roll the VM over SSH → verify the live site |
| `infra-destroy.yml` | manual only | guarded teardown, dry-run by default |

Deploy and upgrade are the same path: an application change and an
infrastructure change both go through `deploy.yml`.

`ci.yml` deliberately does **not** trigger on a push to `main`: `deploy.yml`
runs the same tests and the same Docker build there, so triggering both ran
every merge's work twice.

The stack runs as a Compose project on one Compute Engine VM — Caddy for TLS,
the Go app, and self-hosted Postgres on a persistent disk. `deploy.yml` applies
OpenTofu, then rolls the VM onto the new image over an IAP-tunnelled SSH
session; the VM has no public SSH port.

## How authentication works

GitHub Actions presents a short-lived OIDC token describing the workflow run.
Google's Security Token Service exchanges it for a short-lived access token for
the `tomb-deployer` service account. Two independent conditions must hold:

1. the Workload Identity **provider** has
   `attribute_condition = assertion.repository == "anthony-hopkins/tomb"`
2. the **service account** only grants `roles/iam.workloadIdentityUser` to the
   principal set for that same repository

So a fork, or any other repository, gets nothing. There is no key to leak and
nothing to rotate.

`tofu/bootstrap` creates that trust, and it is the one thing that cannot run in
the pipeline it creates — see its README.

## One-time setup

### 1. Google Cloud project

```sh
gcloud projects create YOUR_PROJECT_ID          # or use an existing project
gcloud billing projects link YOUR_PROJECT_ID --billing-account=BILLING_ID
```

Billing must be enabled: Compute Engine will not create a VM without it.

### 2. Apply the bootstrap, locally

```sh
gcloud auth login
gcloud auth application-default login
gcloud config set project YOUR_PROJECT_ID

cd tofu/bootstrap
cp terraform.tfvars.example terraform.tfvars    # set project_id
tofu init
tofu plan -out=tfplan
tofu apply tfplan
```

This enables the APIs, creates the state bucket, and creates the WIF trust plus
the deployer service account.

### 3. Set the repository variables

The bootstrap prints the commands:

```sh
tofu output -raw gh_variable_commands
```

The full set the workflows read:

| Variable | Value | Needed by |
|---|---|---|
| `GCP_PROJECT_ID` | your project id | all |
| `GCP_WIF_PROVIDER` | bootstrap output | all |
| `GCP_DEPLOYER_SA` | bootstrap output | all |
| `GCP_STATE_BUCKET` | bootstrap output | all |
| `GCP_REGION` | `us-central1` (optional) | deploy |
| `TOMB_GUILD_REALM` | your realm slug, e.g. `area-52` | all |
| `BNET_CLIENT_ID` | Battle.net client id | all |
| `TOMB_DOMAIN` | your domain, or empty for the `sslip.io` fallback | all |
| `ACME_EMAIL` | optional Let's Encrypt contact address | all |
| `GCP_CURRENT_IMAGE` | optional; lets plan and destroy avoid a placeholder image | plan, destroy |

These are **variables, not secrets**. None is sensitive: they are identifiers,
and the trust is enforced on Google's side. The only actual secret is the
Battle.net client secret, which lives in Secret Manager and never touches
GitHub.

### 4. The production gate

Both environments (`production`, `production-destroy`) already exist, so
deploys and teardowns appear in the repository's deployment history.

**They carry no protection rules, and on this account they cannot.** GitHub does
not offer environment protection rules — required reviewers among them — on
private repositories on the free plan. Attempting it returns:

```
Failed to create the environment protection rule. Please ensure the billing
plan supports the required reviewers protection rule. (HTTP 422)
```

So the approval Principle V requires takes a different shape: **the trigger is
the approval.**

- A push to `main` runs the tests, builds the image and pushes it. It does
  **not** apply.
- Applying requires someone to run **Deploy** from the Actions tab with
  `apply` checked. That is a deliberate human action, attributed and logged.
- Destroying additionally requires typing the project id and the word `DESTROY`,
  and defaults to a dry run.

That is weaker than a second pair of eyes but stronger than unattended
auto-apply, and it is the best available without changing plan or visibility.

**To get a true approval gate**, either make the repository public or move the
account to a paid plan, then:

1. add yourself as a required reviewer on both environments
2. delete the `github.event_name == 'workflow_dispatch' && inputs.apply`
   condition on the `apply` job in `deploy.yml`

That restores auto-deploy on merge with an approval prompt, which is the
arrangement the workflows were originally shaped for.

### 5. Battle.net application

Register a client at <https://develop.battle.net>, then store the secret in
Secret Manager. The container is created by the main config, so this comes
after the first apply:

```sh
printf '%s' "$BNET_CLIENT_SECRET" | gcloud secrets versions add tomb-platform-bnet-client-secret --data-file=-
```

## The first deploy

Run **Deploy** from the Actions tab with `apply` checked — a merge to `main`
alone builds the image but deliberately stops short of applying. Expect:

1. `test` — the Go suite
2. `build` — image built and pushed, tagged with the commit SHA
3. `apply` — creates the VM, network, disks and secrets
4. `Roll the VM onto the new image` — SSH through IAP and run
   `/opt/tomb/deploy.sh`. This retries for several minutes, because a brand-new
   VM is still installing Docker in its startup script.
5. `verify` — liveness, readiness, the landing page, and the anonymous guild gate

Then finish the two things the pipeline cannot do for you:

```sh
# 1. the Battle.net client secret (the container exists only after step 3)
printf '%s' 'YOUR_CLIENT_SECRET' | gcloud secrets versions add tomb-platform-bnet-client-secret --data-file=-

# 2. restart so the app picks it up
gcloud compute ssh tomb-platform --zone us-central1-a --tunnel-through-iap   --command "cd /opt/tomb && sudo docker compose --env-file .env restart app"
```

and register `<service_url>/auth/callback` as a redirect URI at
<https://develop.battle.net>. Until that is done, signing in fails: OAuth
requires the `redirect_uri` to match exactly, down to the trailing slash.

## Hostname and TLS

HTTPS is mandatory — Blizzard rejects a plain-HTTP redirect URI and the app
issues `Secure` cookies — and Caddy handles certificates automatically.

With `TOMB_DOMAIN` unset the site serves on `<dashed-ip>.sslip.io`, which is
real public DNS pointing at the VM, so Let's Encrypt can validate it. Set
`TOMB_DOMAIN` to a real name when you have one and point an A record at
`tofu output -raw public_ip`; that address is reserved and survives VM
recreation.

## Everyday deploys

Merging to `main` builds and pushes an image tagged with the commit SHA, so the
artifact is always ready. To ship it, run **Deploy** with `apply` checked.

For an infrastructure-only change where you do not want a new image, run
**Deploy** with **skip_build** checked; it reuses whatever image the VM is
already running, read from the `deployed_image` output.

Leaving `apply` unchecked runs the tests and build only — useful for confirming
a change is deployable without deploying it.

## Destroying

Run **Infra destroy** manually. It requires:

1. `confirm_project` — the project id, typed exactly
2. `confirm_phrase` — `DESTROY`
3. `dry_run` unchecked, which it is not by default

`dry_run` is **checked by default**: the first run produces a destroy plan and
deletes nothing. Uncheck it only when you have read that plan.

A live destroy tears down the VM, network and secrets — and then **stops at the
Postgres data disk**, which carries `prevent_destroy` in `compute.tf`. That is
the only irreplaceable state in the project, so deleting it takes three
deliberate steps rather than one command: snapshot the disk, remove the
lifecycle block, re-run the workflow. The run summary spells this out.

**Deliberately left behind:** the data disk, the state bucket and its version
history, everything in `tofu/bootstrap`, and the project itself.

## Safety properties worth knowing

- **No keys.** OIDC federation only, restricted to this repository.
- **State is locked.** The GCS backend locks, and `deploy.yml` and
  `infra-destroy.yml` share one concurrency group, so an apply and a destroy can
  never run at once.
- **No public SSH.** Port 22 is open only to Google's IAP range; the pipeline
  tunnels through it with its own federated credentials.
- **Applies are never cancelled mid-flight.** `cancel-in-progress: false` on the
  deploy group; interrupting an apply is how state drifts from reality.
- **Plans are reviewable before they run.** `infra-plan.yml` posts to the PR and
  holds no apply permission.
- **Deploys are verified.** A green deploy means the service answered
  `/healthz`, `/readyz`, rendered the landing page, and redirected an anonymous
  request away from the dashboard.
- **The deployer is powerful.** Close to project administrator, because the
  config creates networks, IAM bindings and a database. The mitigation is that
  it has no downloadable key and one repository can assume it. The role list is
  a plain list in `tofu/bootstrap/main.tf` if you want to narrow it.
