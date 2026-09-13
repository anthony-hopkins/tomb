# TOMB Guild Platform

A guild website for TOMB. Members sign in with their Battle.net account and land
on a dashboard showing the World of Warcraft character they most recently
played — so you can see who is playing what without asking in Discord.

Built as a thin Go core plus independently addable **apps**. The Character
Dashboard is the first one; see [docs/adding-an-app.md](docs/adding-an-app.md)
for the second.

## Quick start

```sh
cp .env.example .env      # then fill in your Battle.net client id and secret
docker compose up --build
```

That is the whole setup — no local Go or Postgres install needed. The site comes
up on <http://localhost:8080>.

You need a Battle.net OAuth client from <https://develop.battle.net> with
`http://localhost:8080/auth/callback` registered as a redirect URI.

## This machine has a split toolchain

| Tool | Runs from | Notes |
|---|---|---|
| `docker` | **Windows** (PowerShell or Git Bash) | Docker Desktop's WSL integration is off for the Ubuntu distro, so `docker` resolves but fails inside WSL |
| `go` | Windows | Also present in WSL at `~/.local/go` |
| `tofu` | **WSL only** | `~/.local/bin/tofu`; see [tofu/README.md](tofu/README.md) |

Enabling Docker Desktop → Resources → WSL Integration → Ubuntu would collapse
this into one shell, and is also a prerequisite if integration tests ever need
testcontainers.

## Development

```sh
make check      # the full pre-merge gate: build, vet, test, docker build
make test       # go test ./...
make run        # docker compose up --build
```

The constitution requires all four `make check` steps to pass before merge.

## Layout

```
cmd/tomb/            Composition root: config, database, deps, app list, serve
internal/platform/   The thin core — routing, sessions, guild gate, layout, health
internal/auth/       Battle.net OAuth2 and session management
internal/blizzard/   Blizzard API client (behind one narrow interface)
internal/apps/       One directory per app; dashboard is the first
tofu/                OpenTofu: Cloud Run, Cloud SQL, Artifact Registry, Secret Manager
docs/                How to add an app
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

## Specification

This feature was built spec-first. The spec, plan, research, data model,
interface contracts and task list live in
[.specify/specs/001-battlenet-sso-character-dashboard/](.specify/specs/001-battlenet-sso-character-dashboard/),
and the project's engineering principles are in
[.specify/memory/constitution.md](.specify/memory/constitution.md).

Requirement identifiers (FR-001, FR-013a, …) referenced throughout the code
point back to
[spec.md](.specify/specs/001-battlenet-sso-character-dashboard/spec.md).
