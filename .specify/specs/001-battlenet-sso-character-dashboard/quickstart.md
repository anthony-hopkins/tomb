# Quickstart & Validation: Battle.net SSO Login & Character Dashboard

**Feature**: `001-battlenet-sso-character-dashboard`

How to bring the stack up and prove the feature works end to end. Every scenario below
maps to a numbered acceptance scenario in `spec.md`. Implementation belongs in
`tasks.md` — this is the run-and-verify guide.

> [!IMPORTANT]
> **Partly superseded — the setup half no longer works.**
>
> This was written when Principle IV required the whole stack to come up with a
> single `docker compose up`. Constitution 2.0.0 reversed that: there is no
> supported local runtime environment, and the root `compose.yaml` and
> `.env.example` this guide asks for have been deleted. **Prerequisites**,
> **Environment file** and **Bring it up** below describe a stack you can no
> longer run, and `http://localhost:8080` answers nothing.
>
> **The validation scenarios are still current.** Run them against the develop
> environment — <https://dev.tombguild.com> — substituting that origin wherever
> a scenario says `localhost:8080`. They still map to the numbered acceptance
> scenarios in `spec.md`, and develop exists precisely so they can be run
> against something that behaves like production: real TLS, a real reverse
> proxy, and the real Battle.net callback.
>
> For current setup and deployment see
> [docs/deployment.md](../../../docs/deployment.md).
>
> Everything below is left unedited, as the record of how this feature was
> specified and validated at the time.

---

## Prerequisites

| Requirement | Status on this machine | Notes |
|---|---|---|
| Docker + Compose | ✅ Docker 29.7.2 — **Windows side only** | The only requirement for running the app (Principle IV). Not reachable from WSL: fix is Docker Desktop → Resources → WSL Integration → enable Ubuntu |
| Go 1.27.x | ✅ go1.27.1 | Needed only for `go test` / `go vet` outside the container |
| OpenTofu | ✅ v1.12.6 (in WSL) | Run `tofu` from the WSL distro, not Windows — see below. Needed only for deployment tasks, not local dev |

### Battle.net client registration (one-time, manual)

1. Create a client at the Blizzard Developer Portal (`https://develop.battle.net`).
2. Add redirect URI `http://localhost:8080/auth/callback` for local development.
3. Note the client ID and secret.

This is genuine bootstrap that no automation can perform, so it is the documented
exception Principle V allows.

### Running OpenTofu

`tofu` lives in the WSL Ubuntu distro (`~/.local/bin/tofu`), not on the Windows PATH.
Run infrastructure commands from there; the repository is visible to WSL at
`/mnt/c/Users/antho/development/WOW/Guilds/tomb`:

```sh
wsl -e bash -lc 'cd /mnt/c/Users/antho/development/WOW/Guilds/tomb/tofu && tofu init'
```

Or open a WSL shell and work in that path directly. Note that `tofu fmt` and state
files on a `/mnt/c` path are subject to Windows line endings — keep `.gitattributes`
authoritative for `*.tf` if formatting churn appears.

### Environment file

Copy `.env.example` to `.env` and fill in:

```sh
BNET_CLIENT_ID=<from the developer portal>
BNET_CLIENT_SECRET=<from the developer portal>
BNET_REDIRECT_URL=http://localhost:8080/auth/callback
BNET_REGION=us
TOMB_GUILD_NAME=TOMB
TOMB_GUILD_REALM=<your-realm-slug>
DATABASE_URL=postgres://tomb:tomb@db:5432/tomb?sslmode=disable
SESSION_COOKIE_SECURE=false
```

`SESSION_COOKIE_SECURE=false` exists solely because local development is plain HTTP.
It MUST be `true` everywhere else.

---

## Bring it up

```sh
docker compose up --build
```

**Run this from Windows (PowerShell or Git Bash), not from WSL.** Docker Desktop's
WSL integration is currently disabled for the Ubuntu distro, so `docker` resolves but
fails inside WSL. This machine therefore has a split workflow: `docker` and `go` on
Windows, `tofu` in WSL. Enabling WSL Integration in Docker Desktop would let both run
from one shell, and is also a prerequisite if any future task wants
testcontainers-backed integration tests.

Per Principle IV this is the entire setup — no local Go or Postgres install needed.
Expect the app on `http://localhost:8080` and Postgres in the `db` container.

Verify the platform is healthy before testing the feature:

```sh
curl -fsS http://localhost:8080/healthz   # -> ok
curl -fsS http://localhost:8080/readyz    # -> ok (503 if the database is unreachable)
```

---

## Automated checks

Every one of these must pass before merge (constitution, Development Workflow):

```sh
go build ./...
go vet ./...
go test ./...
docker build -t tomb-platform .
```

`go test ./...` covers the business logic the constitution requires to be tested:
character selection and the FR-006 tiebreaker, guild matching, session issue/expiry,
and app registration. No test makes a live Blizzard call — see
[contracts/blizzard-api.md](contracts/blizzard-api.md) for the fixture approach.

---

## Manual validation scenarios

These need a real Battle.net account, because the OAuth consent screen cannot be
automated. Run them against `docker compose up`.

### 1. Login redirect → spec scenario 1

Open `http://localhost:8080`, click **Sign in with Battle.net**.
**Expect**: redirect to `oauth.battle.net/authorize`, with `scope=wow.profile` and a
`state` parameter present in the URL.

### 2. Successful login as a guild member → scenarios 2 and 3

Approve the consent screen using an account with a TOMB character.
**Expect**: land on `/app/dashboard`, no password ever typed on the guild site, and a
card showing name, realm, class, level and average item level for the character you
played most recently. Confirm the character shown is genuinely your latest — that is
FR-006 working against live data.

### 3. Non-member denial → scenario 7

Log in with an account that has no TOMB character.
**Expect**: the "this site is for TOMB members" page, a log-out action, and no app
reachable. Try `/app/dashboard` directly — it must not render (FR-013a).

### 4. Declined authorization → scenario 5

Start login, then cancel on Blizzard's consent screen.
**Expect**: a friendly "login not completed" message with a retry action, and **no**
session cookie set (FR-009).

### 5. Logout → scenario 4

From the dashboard, click **Log out**.
**Expect**: back on the public landing page, no character data visible, `tomb_session`
cookie gone. Pressing Back must not re-render the dashboard from cache.

### 6. Session expiry → scenario 8

Expire the session without waiting 24 hours:

```sh
docker compose exec db psql -U tomb -d tomb \
  -c "UPDATE sessions SET expires_at = now() - interval '1 minute';"
```

Reload `/app/dashboard`.
**Expect**: treated as unauthenticated and prompted to sign in again — never stale
character data (FR-015).

### 7. Revoked authorization → FR-012

Revoke the application from Battle.net account settings, then reload the dashboard.
**Expect**: the next action detects the dead token, deletes the session, and prompts
re-login rather than showing stale data.

### 8. Blizzard unavailable → FR-010

Point `BNET_API_HOST` at an unroutable host (or block it) and reload the dashboard.
**Expect**: a clear, retry-able error — never a stack trace or a blank page.

### 9. Live-fetch behaviour → FR-016

Log in on a character, then log into a *different* character in-game, log out, and
reload the dashboard.
**Expect**: the newly played character appears without any manual cache clearing —
there is no cache to clear. Also confirm via the app logs that each dashboard reload
issues a fresh set of Blizzard calls (`1 + N`), since always-live is the explicit
FR-016 decision.

### 10. Extensibility → scenario 6

The real proof is in `go test` (see
[contracts/app-registration.md](contracts/app-registration.md)): a stub second app
mounts and serves with **zero** changes to authentication, session handling, or the
dashboard app. If adding that stub required touching any auth file, FR-011 has
regressed.

---

## Validation checklist

| Scenario | Covered by | Type |
|---|---|---|
| 1 — redirect to Blizzard | Manual 1 | Manual |
| 2 — session created, no password | Manual 2 | Manual |
| 3 — most-recent character with all fields | Manual 2 + unit tests | Both |
| 4 — logout clears everything | Manual 5 | Manual |
| 5 — declined authorization | Manual 4 | Manual |
| 6 — new app needs no core changes | `go test` | Automated |
| 7 — non-member denied | Manual 3 + unit tests | Both |
| 8 — session expiry | Manual 6 + unit tests | Both |
| FR-006 tiebreaker | Unit tests | Automated |
| FR-010 API failure handling | Manual 8 + unit tests | Both |
| FR-012 revoked token | Manual 7 | Manual |
| FR-016 live fetch, no cache | Manual 9 | Manual |
