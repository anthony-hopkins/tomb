# Deployment pipeline

Infrastructure and the application are both deployed by GitHub Actions running
OpenTofu against Google Cloud. Authentication is keyless — there is no
service-account JSON key anywhere, in GitHub secrets or otherwise.

## The five workflows

| Workflow | Trigger | What it does |
|---|---|---|
| `ci.yml` | pull requests only | gofmt, build, vet, `go test -race`, Docker build |
| `infra-plan.yml` | PR touching `tofu/**` | `tofu plan` for the PR's target environment, posted as a comment. Never applies. |
| `dev-lifecycle.yml` | manual only | start / stop / status for the develop VM. Cannot touch production. |
| `deploy.yml` | push to `develop` or `main` builds only; **manual run applies** | test → build and push image → `tofu apply` → roll the VM over SSH → verify the live site |
| `infra-destroy.yml` | manual only | guarded teardown, dry-run by default |

Deploy and upgrade are the same path: an application change and an
infrastructure change both go through `deploy.yml`.

`ci.yml` deliberately does **not** trigger on a push to `develop` or `main`:
`deploy.yml` runs the same tests and the same Docker build there, so triggering
both ran every merge's work twice.

## Branches

Constitution 2.0.0 removed the local development stack — there is no supported
way to run this software on a workstation, because a local stack has no TLS
edge, no reverse proxy and a different OAuth callback, and would not be telling
you the truth. Running software is tested in a deployed environment:

```
feature branch  --PR-->  develop  --PR-->  main
                         (develop env)     (production, tombguild.com)
```

Every commit that reaches `main` has been deployed to and exercised in develop
first. Nothing is pushed straight to `main`.

### How the two environments stay the same

One OpenTofu configuration, one Compose project, one image. The **workspace**
decides which environment is being touched, and `tofu/environments.tf` is the
only file that knows the difference:

| | production | develop |
|---|---|---|
| Workspace | `default` | `develop` |
| Resource prefix | `tomb-platform` | `tomb-platform-develop` |
| Hostname | `tombguild.com` | `dev.tombguild.com` |
| Machine | `e2-small` | `e2-small` |
| Disk snapshots | yes | no |
| Applies when | dispatched from `main` | pushed to `develop` |

Production is workspace `default` because that is where its state already lives;
renaming it would mean migrating live production state for a cosmetic gain.

The workspace is the single source of truth for *both* the state file and every
resource name. That is deliberate — if the environment were a variable passed
alongside the workspace, the two could disagree, and the failure mode of that
disagreement is applying develop's names onto production's state. The `resolve`
job additionally refuses to apply production from any branch but `main`, and
develop from any branch but `develop`.

Everything else — machine type, Postgres tuning, the Caddyfile, the deploy
script — is identical by construction, because both environments run the same
image and the same `deploy/compose.yaml` out of it.

### Standing up the develop environment

It does not exist until its first deploy. Because Caddy cannot obtain a
certificate for a hostname that does not resolve, and the address does not exist
until the infrastructure is applied, the first deploy is two passes:

**1. Create the infrastructure.** Push to `develop`, or run **Deploy** with
environment `develop`. OpenTofu creates the VM, disks, network and reserved
address. The run then *fails* at the TLS check — expected, because DNS does not
point anywhere yet. The step summary prints the reserved address before that
happens, which is the thing you need:

```
### develop address
Reserved public IP: 34.x.x.x
```

**2. Point DNS at it, then deploy again.** Add an `A` record for
`dev.tombguild.com` to that address — it is reserved, so it survives VM
recreation — and register `https://dev.tombguild.com/auth/callback` as a
redirect URI at <https://develop.battle.net>, alongside the production one.
Blizzard accepts several. Then push to `develop` again; Caddy gets its
certificate and verification passes.

**3. Add the Battle.net client secret.** Same one-time step production needed,
against the develop secret container:

```sh
printf '%s' "$BNET_CLIENT_SECRET" |   gcloud secrets versions add tomb-platform-develop-bnet-client-secret --data-file=-
```

Until that version exists the app starts but sign-in fails, which `configure.sh`
warns about rather than treating as fatal.

**4. Delete the transitional exception** from the constitution's Development
Workflow section. It exists only until this environment does.

To override the hostname, set the `TOMB_DEVELOP_DOMAIN` repository variable;
unset, it derives as `dev.` + `TOMB_DOMAIN` so the two cannot drift apart.

### Develop only runs when you need it

The VM is essentially the whole cost of an environment — ~$12.23 of a ~$17
month, with the database's own disk at 40 cents — so develop stops itself
overnight and is started on demand.

| | Running | Stopped |
|---|---|---|
| Compute | ~$12.23/mo | **not billed** |
| Disks + reserved address | ~$4–8/mo | ~$4–8/mo |
| Data, DNS, TLS certificate | kept | **kept** |

`tofu/autostop.tf` attaches a Compute Engine *instance schedule* that stops the
VM on a cron — Google runs it, so there is no Cloud Scheduler job, no function,
and nothing on the VM to rot. There is deliberately **no start schedule**: an
environment that switches itself on every morning whether or not anyone is
working defeats the point. Adjust with `auto_stop_schedule` and
`auto_stop_timezone`; it defaults to 02:00 UTC daily.

Starting is on demand, either way round:

```sh
gh workflow run dev-lifecycle.yml -f action=start    # or stop, or status
```

or just deploy — a push to `develop` starts the VM if it is stopped, then rolls
it onto the new image. Coming back takes about a minute; `startup.sh` runs on
every boot and brings the stack up on its own.

Production cannot auto-stop. `enable_auto_stop` is an ordinary variable and
could be passed `true` by a typo, so the policy is additionally guarded on
`local.is_production` in `autostop.tf` — a structural block, not a convention.

> **Why stopped and not destroyed.** Caddy's certificate lives in the
> `caddy-data` volume on the boot disk, so destroying the VM means a fresh
> Let's Encrypt issuance on every rebuild — and LE allows five duplicate
> certificates per name per week. Cycle develop harder than that and it comes
> back with no working TLS until the window clears. Stopping keeps the disk, the
> certificate and the reserved address. It also saves the same amount as
> destroying the VM but keeping its address, because Google bills an unattached
> static IP at a higher rate than an attached one.
>
> If you do want develop gone entirely for a long break, note that its data disk
> carries the same `prevent_destroy` guard as production's — OpenTofu requires a
> literal there, so it cannot be made conditional — and removing it is a
> deliberate edit.

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

- A push to `develop` or `main` runs the tests, builds the image and pushes it.
  It does **not** apply.
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

## Hostname, TLS, and the OAuth callback

HTTPS is mandatory — Blizzard rejects a plain-HTTP redirect URI and the app
issues `Secure` cookies — and Caddy handles certificates automatically.

With `TOMB_DOMAIN` unset the site serves on `<dashed-ip>.sslip.io`, which is
real public DNS pointing at the VM, so Let's Encrypt can validate it.

### Moving to a real domain

The callback URL must match the hostname people actually use, exactly. The chain
is:

```
TOMB_DOMAIN -> tofu var domain -> VM metadata tomb-public-url
            -> container env BNET_REDIRECT_URL = "${PUBLIC_URL}/auth/callback"
            -> the redirect_uri the app sends Blizzard
```

Blizzard requires an exact string match against a registered URI, so switching
hostname means switching the callback too. Order matters:

1. **Point DNS at the VM first.** An `A` record for the domain to
   `tofu output -raw public_ip`. That address is reserved, so it survives VM
   recreation. Let's Encrypt validates over HTTP-01 on port 80, so the name has
   to resolve before Caddy can get a certificate.
2. **Register the new callback at <https://develop.battle.net>** —
   `https://YOUR_DOMAIN/auth/callback`. Keep the old `sslip.io` callback
   registered alongside it during the switch; Blizzard accepts several redirect
   URIs, so sign-in keeps working on both while DNS propagates.
3. **Set the repository variable** and deploy:
   ```sh
   gh variable set TOMB_DOMAIN --body 'tombguild.com'
   ```
   Then run **Deploy** with `apply` checked. The apply updates instance
   metadata; `configure.sh` rewrites `/opt/tomb/.env` from that metadata on
   every deploy, so Caddy requests a certificate for the new name and
   `BNET_REDIRECT_URL` follows.
4. **Remove the old `sslip.io` callback** from Blizzard once the domain works.

Verify the certificate actually covers the new name before trusting it:

```sh
curl -fsS https://YOUR_DOMAIN/healthz
echo | openssl s_client -connect YOUR_DOMAIN:443 -servername YOUR_DOMAIN 2>/dev/null   | openssl x509 -noout -subject -issuer -dates
```

If Caddy cannot get a certificate the site will not serve HTTPS at all, so a
failing `/healthz` right after a domain switch usually means DNS had not
propagated when Caddy tried. `docker compose logs caddy` says so explicitly.

## When you change `tofu/bootstrap`

The bootstrap is applied **by hand**, so editing it changes nothing until you
re-apply it:

```sh
cd tofu/bootstrap && tofu plan -out=tfplan && tofu apply tfplan
```

This bit once already: the VM change added `compute.admin`,
`compute.osAdminLogin` and `iap.tunnelResourceAccessor` to the deployer, and
without a re-apply the next deploy failed with a series of `403 ... permission
... forbidden` errors *partway through* the apply, after it had already
destroyed a resource.

`deploy.yml` now runs a preflight that checks the deployer holds every
permission the apply needs and fails early with the command above, rather than
discovering the gap halfway through.

## Everyday deploys

Merging to `develop` or `main` builds and pushes an image tagged with the commit
SHA, so the artifact is always ready. To ship what is on `main`, run **Deploy**
with `apply` checked.

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
