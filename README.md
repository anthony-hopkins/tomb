<p align="center">
  <img src="docs/assets/banner.jpg" alt="TOMB" width="640">
</p>

<h1 align="center">TOMB Guild Platform</h1>

<p align="center">
  <em>Sign in with Battle.net. See who you are playing right now.</em>
</p>

---

A guild website for TOMB. Members sign in with their Battle.net account and land
on a dashboard showing the World of Warcraft character they most recently
played — so you can see who is playing what without asking in Discord.

Built as a thin Go core plus independently addable **apps**. The Character
Dashboard is the first one; see [docs/adding-an-app.md](docs/adding-an-app.md)
for the second.

## Working on this

**There is no local stack to bring up.** Constitution Principle IV recognises no
supported local runtime environment: a workstation stack terminates no TLS, runs
no reverse proxy, performs no ACME and answers a different OAuth callback, so its
verdict on whether something works is not worth much. Running software is tested
in a deployed Google Cloud environment.

What runs on your machine is the merge gate, and nothing that serves:

```sh
make check      # go build, go vet, go test ./..., docker build
make test       # go test ./...
```

All four `make check` steps must pass before merge.

## How a change reaches a running site

| Branch | Environment | How it moves |
|---|---|---|
| feature branch | — | pull request into `develop` |
| `develop` | develop, <https://dev.tombguild.com> | merge the pull request — deploys itself, and starts the VM if it is stopped |
| `main` | production, <https://tombguild.com> | pull request from `develop`, then run **Deploy** |

Merging into `develop` builds the image and applies it automatically; production
keeps a manual gate. Nothing is pushed directly to `main`, and no commit reaches
`main` without having been exercised in develop first. The pipeline refuses to
apply production from any branch but `main`.

Develop stops itself overnight and is started by the next deploy, or by
`gh workflow run dev-lifecycle.yml -f action=start`, so it costs compute only
while it is in use — roughly $4-8/month idle against $17 running.

Both environments come from one OpenTofu configuration, selected by workspace,
and run the same image and the same Compose project — they differ in hostname
and whether the database disk is snapshotted, and nothing else. See
[tofu/environments.tf](tofu/environments.tf) and
[docs/deployment.md](docs/deployment.md).

> **The develop environment is configured but not yet applied.** Its first deploy
> takes two passes — infrastructure, then DNS, then deploy again — because Caddy
> cannot get a certificate for a name that does not resolve yet. The procedure is
> in [docs/deployment.md](docs/deployment.md#standing-up-the-develop-environment).

## This machine has a split toolchain

| Tool | Runs from | Notes |
|---|---|---|
| `go` | Windows or WSL | Windows PATH, and WSL at `~/.local/go` |
| `docker` | Windows or WSL | Docker Desktop, WSL integration enabled for Ubuntu |
| `tofu` | **WSL only** | `~/.local/bin/tofu`; see [tofu/README.md](tofu/README.md) |
| `gcloud`, `gh` | **WSL only** | not on the Windows PATH at all |

## Layout

```
cmd/tomb/            Composition root: config, database, deps, app list, serve
internal/platform/   The thin core — routing, sessions, guild gate, layout, health
internal/auth/       Battle.net OAuth2 and session management
internal/blizzard/   Blizzard API client (behind one narrow interface)
internal/apps/       One directory per app; dashboard is the first
deploy/              Production Compose project, Caddyfile, VM startup and deploy scripts
tofu/                OpenTofu: the VM, network, disks and secrets
docs/                Adding an app, the deployment pipeline, project art
```

Templates, the stylesheet and SQL migrations are embedded with `go:embed`, so
the binary is self-contained and does not depend on its working directory.

## How it works, and why

Three findings shaped the design. All three are written up with sources in
[research.md](.specify/specs/001-battlenet-sso-character-dashboard/research.md).

**Character data is fetched live on every dashboard view.** Blizzard exposes
`last_login_timestamp` only on the per-character profile endpoint, not on the
account summary or the guild roster, so finding "most recently played" costs
`1 + N` API calls for `N` characters. There is no cache by design (FR-016), so
that cost is re-paid per view. The fan-out is bounded at 8 concurrent requests,
well inside Blizzard's 36,000/hour and 100/second limits, and a single
character's failure degrades to a partial-data notice rather than an error page.

**A session lasts exactly as long as the Blizzard token.** Battle.net issues no
refresh token and its access tokens last about 24 hours. A longer site session
would leave a member nominally signed in while the site can no longer fetch
anything on their behalf, so expiry is taken from the token's own `expires_in`
and is never extended by activity.

**Guild membership costs no extra API calls.** The per-character profile
responses already carry a `guild` object, so membership is read from data the
dashboard fetch has in hand. It is re-derived on every request and never cached,
which means someone who leaves TOMB loses access on their next page view.

## Security posture

- No Blizzard password is ever collected, transmitted or stored. There is no
  password column in the schema at all.
- The Blizzard access token lives only in a server-side session row. It never
  reaches a cookie, a template, a URL, or a log line — there is a test that
  fails if it does (`internal/platform/redaction_test.go`).
- Sessions are opaque 256-bit tokens; only their SHA-256 hash is stored, so a
  database disclosure yields no usable cookies.
- `HttpOnly`, `Secure`, `SameSite=Lax` cookies; CSRF protection on logout; a
  strict Content-Security-Policy; and `no-store` on authenticated pages.

## Deployment

GitHub Actions deploys to Google Cloud with OpenTofu, authenticating keylessly
via Workload Identity Federation — there is no service-account key anywhere.

The whole stack runs as a Compose project on a single Compute Engine VM: Caddy
for automatic TLS, the Go app, and self-hosted Postgres on its own persistent
disk. That is a deliberate choice over managed Cloud Run plus Cloud SQL, which
cost roughly $55/month for a database holding two small tables; this runs at
about $17–21. Applying requires a manual workflow run, and every deploy is
verified against the live site afterwards. Teardown is a separate, guarded,
dry-run-by-default workflow.

See [docs/deployment.md](docs/deployment.md) for setup and
[tofu/README.md](tofu/README.md) for what gets created and what it costs.

## Specification

This feature was built spec-first. The spec, plan, research, data model,
interface contracts and task list live in
[.specify/specs/001-battlenet-sso-character-dashboard/](.specify/specs/001-battlenet-sso-character-dashboard/),
and the project's engineering principles are in
[.specify/memory/constitution.md](.specify/memory/constitution.md).

Requirement identifiers (FR-001, FR-013a, …) referenced throughout the code
point back to
[spec.md](.specify/specs/001-battlenet-sso-character-dashboard/spec.md).
