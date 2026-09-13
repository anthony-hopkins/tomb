# Implementation Plan: Battle.net SSO Login & Character Dashboard

**Branch**: `001-battlenet-sso-character-dashboard` | **Date**: 2026-09-13 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `.specify/specs/001-battlenet-sso-character-dashboard/spec.md`

## Summary

Deliver Battle.net OAuth2 SSO plus a dashboard showing the member's most recently
played WoW character, built as the first registrable app on a thin Go platform core.

The technical shape is driven by three findings from Phase 0:

1. **`last_login_timestamp` exists only on the per-character profile summary**, so
   identifying the "most current" character requires `1 + N` Blizzard calls per view.
   With FR-016 forbidding caching, that cost is re-paid on every view — handled with
   bounded concurrency (8 in flight) and partial-failure tolerance.
2. **Battle.net issues no refresh token** and its access tokens last ~24h, which is
   the mechanism behind FR-015: the site session expires exactly when the token does.
3. **The character profile summary already carries `guild`**, so guild verification
   costs zero extra API calls and needs no roster endpoint or second token type.

Together these shrink persistence to two tables — `users` and `sessions`. No character
data is ever stored.

## Technical Context

**Language/Version**: Go 1.27.x (1.27.1 current stable, pinned in `go.mod`)

**Primary Dependencies**: `golang.org/x/oauth2` (Battle.net client),
`github.com/jackc/pgx/v5` (Postgres driver, via `database/sql`),
`golang.org/x/sync/errgroup` (bounded fan-out). Everything else is stdlib:
`net/http` (routing via ServeMux patterns), `html/template`, `log/slog`, `testing`,
`crypto/rand`. Each dependency is justified in [research.md](research.md) D7.

**Storage**: PostgreSQL — resolves the constitution's `TODO(DB_ENGINE)`. Two tables
(`users`, `sessions`). Postgres container locally; Cloud SQL in GCP.

**Testing**: stdlib `testing`, table-driven; `net/http/httptest` with captured JSON
fixtures for the Blizzard client. No live API calls in tests.

**Target Platform**: Linux container on Google Cloud Run; Docker Compose locally.

**Project Type**: Server-rendered Go web service (no SPA, no separate frontend build).

**Performance Goals**: Dashboard p95 under ~2s for a typical member (≤10 characters)
on a warm instance. Latency scales with alt count by design — see Constraints.

**Constraints**:
- No caching of character data (FR-016) — every view re-issues `1 + N` Blizzard calls.
- Blizzard limits: 36,000 requests/hour and 100/second; max 8 concurrent fetches.
- Session lifetime is not ours to choose — it is the token's `expires_in` (FR-015).
- Single configured region (FR-014).
- No secrets in the repo or in OpenTofu files (Principle V).

**Scale/Scope**: One guild — tens of members, low hundreds of dashboard views/day.
Comfortably inside the hourly quota even with no caching (research D9).

## Constitution Check

*GATE: evaluated before Phase 0 and re-evaluated after Phase 1 design.*

| # | Principle | Verdict | Evidence |
|---|---|---|---|
| I | Standard-Library-First | **PASS** | 3 dependencies, each in a pre-approved category, each justified with replacement scope and exit cost in research D7. Routing, templating, logging, testing, crypto all stdlib. No framework, router, ORM, or template library. |
| II | Modular "Apps" Architecture | **PASS** | Two-method `App` interface as the single extension point ([contracts/app-registration.md](contracts/app-registration.md)). Core owns auth, session, gating, layout. Apps never import each other. Adding an app edits only the slice in `main.go`. |
| III | Battle.net Identity as Root of Trust | **PASS** | Authorization-code flow only; no password field exists anywhere in the schema by construction. Only the access token is persisted, only for the session's life, server-side, never logged or sent to the browser. All character data comes from the official Profile API. |
| IV | Container-First Delivery | **PASS** | `Dockerfile` plus `compose.yaml` bringing up app and Postgres; `docker compose up --build` is the whole local setup. Same image promoted to production; behaviour varies only by environment variable. |
| V | Infrastructure as Code via OpenTofu | **PASS** | All GCP resources under `tofu/` (Cloud Run, Cloud SQL, Artifact Registry, Secret Manager). Client secret from Secret Manager, never committed. The one manual step — creating the Battle.net client — is genuine bootstrap that no API can perform, documented in quickstart. |
| VI | Test Discipline & Observability | **PASS** | Table-driven tests for all four named areas: character selection incl. the FR-006 tiebreaker, guild matching, session handling, app registration. `log/slog` structured JSON; `/healthz` and `/readyz`. |
| VII | Simplicity & YAGNI | **PASS** | No cache layer (FR-016 forbids it anyway), no roster endpoint, no second token type, no plugin discovery, no permission model beyond `RequiresGuild`, no multi-region abstraction. The `App` interface stays at two methods until a second real app justifies more. |

**Pre-Phase 0 gate**: PASS — no unresolved spec clarifications (all 5 closed by
`/speckit-clarify`), no principle conflicts in the proposed approach.

**Post-Phase 1 re-evaluation**: PASS — the design as built in
research/data-model/contracts introduced no new dependencies, no new persistence
beyond the two tables, and no new extension surface. Two items are worth your eyes,
neither of which is a violation:

- **FR-013 implementation direction** (research D4): the guild check reads `guild` off
  each character rather than fetching the roster and searching it. Same outcome, zero
  extra calls, but it is not the literal reading of FR-013's wording. Flagged rather
  than silently substituted — say the word and I will switch it to the roster lookup.
- **FR-016's latency cost** (research D9): accepted product decision, recorded with its
  mitigation. Not a Complexity Tracking entry because it violates no principle — it is
  a deliberate tradeoff you chose during clarification.

Also resolved here: the constitution's `TODO(DB_ENGINE)` → PostgreSQL. Its
`TODO(GUILD_VERIFICATION)` is answered by FR-013 (Blizzard-sourced, not a manual
allow-list); clearing both notes in `constitution.md` needs `/speckit-constitution`,
since this command only writes feature artifacts.

## Project Structure

### Documentation (this feature)

```text
.specify/specs/001-battlenet-sso-character-dashboard/
├── plan.md              # This file
├── spec.md              # Feature specification (clarified)
├── research.md          # Phase 0 output — D1..D11
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── http-routes.md
│   ├── app-registration.md
│   └── blizzard-api.md
└── tasks.md             # Phase 2 — created by /speckit-tasks, NOT by this command
```

### Source Code (repository root)

```text
cmd/
└── tomb/
    └── main.go               # Composition root: config, DB, deps, app slice, serve

internal/
├── platform/                 # The thin core (Principle II)
│   ├── app.go                # App, AppMeta, Registrar, Deps  <- extension point
│   ├── mount.go              # Route mounting, prefix/slug validation, nav building
│   ├── middleware.go         # Session resolution, guild gate, request logging
│   ├── config.go             # Environment-variable config loading
│   └── health.go             # /healthz, /readyz
├── auth/                     # Battle.net OAuth2 + sessions
│   ├── oauth.go              # Authorize redirect, state, callback, code exchange
│   ├── session.go            # Issue, resolve, expire, delete; cookie handling
│   └── store.go              # users + sessions persistence
├── blizzard/                 # Outbound API client
│   ├── client.go             # BlizzardClient impl: userinfo, account, character
│   ├── model.go              # Character, CharacterRef, Identity, Guild
│   └── fixtures/             # Captured JSON for tests
└── apps/
    └── dashboard/            # The first app (FR-011)
        ├── app.go            # Meta() + Routes() — implements platform.App
        ├── select.go         # FR-006 selection + total-order tiebreaker
        ├── guild.go          # FR-013 membership matching
        └── templates/        # Dashboard-owned templates

web/
├── templates/                # Shared layout, landing, non-member, error pages
└── static/                   # CSS; minimal to no JavaScript

migrations/                   # SQL schema migrations
tofu/                         # OpenTofu: Cloud Run, Cloud SQL, Artifact Registry, Secret Manager
apps/                         # Pre-existing placeholder (currently .gitkeep only)

Dockerfile
compose.yaml
.env.example
go.mod
```

**Structure Decision**: Single Go module, server-rendered — Option 1 (single project),
since there is no separate frontend build (`html/template` per the constitution) and no
second deployable. The layout mirrors Principle II directly: `internal/platform` is the
thin core, `internal/apps/<name>` is one directory per app, and each app owns its own
templates and data access. `internal/auth` and `internal/blizzard` sit beside the core
rather than inside any app, because both are core concerns that every future app
inherits. `cmd/tomb/main.go` is the only file an added app touches.

Note the repo already contains a top-level `apps/` placeholder (just a `.gitkeep`).
Go apps live under `internal/apps/` so they cannot be imported from outside the module;
the root `apps/` is left untouched for any non-Go app assets you had in mind — worth
confirming during `/speckit-tasks` whether you want it removed or repurposed.

## Complexity Tracking

> Fill ONLY if Constitution Check has violations that must be justified

**No violations.** All seven principles pass without exception, so there is nothing to
justify here. The two flagged items above (FR-013 lookup direction, FR-016 latency) are
recorded in research.md as deliberate, spec-sanctioned decisions rather than deviations
from the constitution.
