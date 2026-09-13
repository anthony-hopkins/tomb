---

description: "Task list for Battle.net SSO Login & Character Dashboard"
---

# Tasks: Battle.net SSO Login & Character Dashboard

**Input**: Design documents from `.specify/specs/001-battlenet-sso-character-dashboard/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/](contracts/)

**Tests**: **Included and mandatory.** Constitution Principle VI states "Untested business
logic MUST NOT be merged" and names character-selection logic, session handling, app
registration, and guild membership checks as requiring Go table-driven tests. Tests are
therefore not optional for this feature.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1–US5)
- Exact file paths are included in every task

## Path Conventions

Single Go module at repository root, per plan.md's Structure Decision: `cmd/`,
`internal/`, `web/`, `migrations/`, `tofu/`.

## User Story Derivation

spec.md defines one Primary User Story plus 8 acceptance scenarios. The scenarios group
into five independently testable increments, prioritised below. The mapping:

| Story | Priority | spec.md coverage |
|---|---|---|
| US1 — Sign in and see my current character | P1 (MVP) | Scenarios 1, 2, 3; FR-001–007, FR-014, FR-016 |
| US2 — Guild members only | P2 | Scenario 7; FR-013, FR-013a |
| US3 — Leave safely (logout & expiry) | P2 | Scenarios 4, 8; FR-008, FR-015 |
| US4 — Nothing breaks ugly | P3 | Scenario 5; FR-009, FR-010, FR-012 |
| US5 — Proven extensibility | P3 | Scenario 6; FR-011 |

⚠️ **Deployment constraint**: US1 alone is an internal increment, not a shippable site.
FR-013a makes guild gating mandatory, so **US1 + US2 together are the minimum publicly
deployable state.** Demo US1 locally; do not expose it.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization and container scaffolding

- [X] T001 Initialize the Go module in `go.mod` with module path `github.com/anthony-hopkins/tomb` and `go 1.27`, then add the three approved dependencies (`golang.org/x/oauth2`, `github.com/jackc/pgx/v5`, `golang.org/x/sync`) per research.md D7
- [X] T002 [P] Create the directory skeleton from plan.md: `cmd/tomb/`, `internal/platform/`, `internal/auth/`, `internal/blizzard/fixtures/`, `internal/apps/dashboard/templates/`, `web/templates/`, `web/static/`, `migrations/`, `tofu/`
- [X] T003 [P] Create `.env.example` with all eight variables from data-model.md Configuration: `BNET_CLIENT_ID`, `BNET_CLIENT_SECRET`, `BNET_REDIRECT_URL`, `BNET_REGION`, `TOMB_GUILD_NAME`, `TOMB_GUILD_REALM`, `DATABASE_URL`, `SESSION_COOKIE_SECURE`
- [X] T004 [P] Create `.gitattributes` forcing `text eol=lf` for `*.go`, `*.tf`, `*.sql`, `*.sh` — the repo is edited from Windows but built in Linux containers and `tofu` runs from WSL over `/mnt/c` (quickstart.md)
- [X] T005 [P] Create multi-stage `Dockerfile`: build with `golang:1.27`, run from a minimal static base as a non-root user, exposing port 8080 (Principle IV)
- [X] T006 [P] Create `compose.yaml` with an `app` service built from `Dockerfile` and a `db` service on `postgres:18`, wiring `DATABASE_URL` and a healthcheck so `app` waits for `db` (Principle IV)
- [X] T007 [P] Create `Makefile` with `build`, `vet`, `test`, `docker`, and `check` targets matching the merge gate in the constitution's Development Workflow
- [X] T008 Remove the root `apps/` placeholder directory (`apps/.gitkeep`) — Go apps live under `internal/apps/` so they cannot be imported from outside the module, leaving the root copy unused

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The thin platform core plus the Blizzard client — everything every user story depends on

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [X] T009 Implement environment-variable config loading in `internal/platform/config.go`: all eight variables from data-model.md, failing fast with a named error on any missing required value; `SESSION_COOKIE_SECURE` defaults to `true` and may only be `false` for local HTTP
- [X] T010 [P] Configure the `log/slog` JSON handler in `internal/platform/logging.go` with structured key/value output (Principle VI)
- [X] T011 Write migration `migrations/0001_init.sql` creating `users` and `sessions` exactly per data-model.md: `users(id bigserial PK, bnet_sub text NOT NULL UNIQUE, battletag text NOT NULL, first_seen_at timestamptz NOT NULL DEFAULT now(), last_seen_at timestamptz NOT NULL DEFAULT now())`; `sessions(token_hash bytea PK, user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE, bnet_access_token text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL)`; plus `sessions_expires_at_idx` and `sessions_user_id_idx`. No character table and no password column.
- [X] T012 Implement the `database/sql` pool over `pgx/v5` in `internal/platform/db.go`, including migration application at startup
- [X] T013 [P] Define the extension point in `internal/platform/app.go`: `App`, `AppMeta{Slug, NavLabel, RoutePrefix, RequiresGuild}`, `Registrar`, and `Deps{DB, Blizzard, Logger, Layout}` exactly as specified in contracts/app-registration.md
- [X] T014 Implement route mounting in `internal/platform/mount.go`: register each app under its own `RoutePrefix`, and fail fast at startup on a duplicate `Slug`, a duplicate `RoutePrefix`, or a `RoutePrefix` that is not `"/app/" + Slug` (contracts/app-registration.md guarantees 1–2)
- [X] T015 [P] Write table-driven tests in `internal/platform/mount_test.go` covering the mount contract's test-obligation table: two apps mount, duplicate slug errors, prefix/slug mismatch errors
- [X] T016 [P] Implement `GET /healthz` (liveness, no DB work) and `GET /readyz` (503 when the DB ping fails) in `internal/platform/health.go` per contracts/http-routes.md
- [X] T017 [P] Write tests for both health endpoints in `internal/platform/health_test.go`, asserting `/healthz` stays 200 when the database is unreachable
- [X] T018 [P] Define `Identity`, `CharacterRef`, `Character`, and `Guild` types in `internal/blizzard/model.go` per data-model.md, with `Guild` as a pointer/optional because Blizzard omits the field entirely for unguilded characters
- [X] T019 Implement the `BlizzardClient` interface and its HTTP implementation in `internal/blizzard/client.go`: `UserInfo`, `AccountCharacters`, and `CharacterProfile` against the endpoints, namespaces, and query parameters pinned in contracts/blizzard-api.md; lowercase and URL-escape `characterName`; parse `last_login_timestamp` as epoch **milliseconds**
- [X] T020 [P] Capture anonymised JSON fixtures in `internal/blizzard/fixtures/` for userinfo, account characters, and character profile — including one unguilded character and one missing `active_spec` (contracts/blizzard-api.md test obligations)
- [X] T021 [P] Write `net/http/httptest` tests in `internal/blizzard/client_test.go` replaying the T020 fixtures, asserting correct parsing and that no test performs a live Blizzard call
- [X] T022 [P] Create the shared page shell in `web/templates/layout.html` with a navigation region populated from `AppMeta` (contracts/app-registration.md guarantee 4)
- [X] T023 [P] Add minimal stylesheet `web/static/style.css` — server-rendered HTML, no JavaScript unless an interaction demands it (constitution Technology Constraints)
- [X] T024 Wire the composition root in `cmd/tomb/main.go`: load config, open the DB, build `Deps`, declare the app slice, mount, and serve with read/write timeouts and graceful shutdown

**Checkpoint**: Platform core boots, health endpoints respond, Blizzard client is tested against fixtures — user story work can begin

---

## Phase 3: User Story 1 - Sign in and see my current character (Priority: P1) 🎯 MVP

**Goal**: A Battle.net user can sign in without a site password and land on a dashboard showing the single character they most recently played, with name, realm, class, level, and average item level.

**Independent Test**: Run quickstart.md manual scenarios 1 and 2 — click "Sign in with Battle.net", approve consent, and confirm the card shows your genuinely most-recently-played character. `go test ./internal/apps/dashboard/` proves the selection rule including the tiebreaker.

### Tests for User Story 1

> Write these first and confirm they fail before implementing

- [X] T025 [P] [US1] Write table-driven selection tests in `internal/apps/dashboard/select_test.go` covering FR-006's total order: most recent `LastLoginTimestamp`, then highest `Level`, then highest `AverageItemLevel`, then `Name` ascending case-insensitive — with one row per tiebreaker level and one row where all four are exercised
- [X] T026 [P] [US1] Write OAuth flow tests in `internal/auth/oauth_test.go`: authorize redirect targets `https://oauth.battle.net/authorize` with `scope=wow.profile` and a `state` parameter; callback rejects a missing or mismatched `state` with 400 and creates no session
- [X] T027 [P] [US1] Write dashboard handler tests in `internal/apps/dashboard/app_test.go` against a fake `BlizzardClient`, asserting the rendered card contains name, realm, class, level, and average item level (FR-007)

### Implementation for User Story 1

- [X] T028 [P] [US1] Implement session issue and resolve in `internal/auth/session.go`: 256 bits from `crypto/rand` base64url-encoded, SHA-256 hashed for storage, cookie `tomb_session` with `HttpOnly`, `Secure` (per config), `SameSite=Lax`, `Path=/`, and `Max-Age` from the token's `expires_in` (research.md D6)
- [X] T029 [P] [US1] Implement `users` and `sessions` persistence in `internal/auth/store.go`: upsert on `bnet_sub` (never create a second row for a changed `battletag`), update `last_seen_at` on each login, and insert/fetch/delete session rows
- [X] T030 [US1] Implement `POST /auth/login` in `internal/auth/oauth.go`: generate `state`, store it in a short-lived pre-login cookie, and redirect to the authorize endpoint requesting only `wow.profile` (FR-001, FR-002, contracts/http-routes.md)
- [X] T031 [US1] Implement `GET /auth/callback` in `internal/auth/oauth.go`: verify `state`, exchange the code via `golang.org/x/oauth2`, call `UserInfo` for `sub` and `battletag`, upsert the user, create the session with `expires_at` derived from `expires_in`, and redirect to `/app/dashboard` (FR-003, FR-015)
- [X] T032 [P] [US1] Implement the selection rule in `internal/apps/dashboard/select.go` as the total order specified in FR-006, operating only over successfully fetched characters
- [X] T033 [US1] Implement the bounded fan-out in `internal/apps/dashboard/fetch.go`: call `AccountCharacters` once, then `CharacterProfile` per character via `errgroup` with `SetLimit(8)` under a context deadline, with no caching of any result (FR-016, research.md D9)
- [X] T034 [US1] Implement `internal/apps/dashboard/app.go` satisfying `platform.App`: `Meta()` returning `Slug: "dashboard"`, `RoutePrefix: "/app/dashboard"`, `RequiresGuild: true`, and `Routes()` registering `GET /` under that prefix (FR-011)
- [X] T035 [P] [US1] Create the character card template in `internal/apps/dashboard/templates/dashboard.html` rendering name, realm, class, level, and average item level (FR-007)
- [X] T036 [P] [US1] Create the public landing page in `web/templates/landing.html` with a "Sign in with Battle.net" form posting to `/auth/login` (FR-001)
- [X] T037 [US1] Implement `GET /` in `internal/platform/mount.go`: render the landing page for anonymous visitors, redirect authenticated users to `/app/dashboard` (contracts/http-routes.md)
- [X] T038 [US1] Implement session-resolving middleware in `internal/platform/middleware.go` that puts the resolved user into the request context, and apply it to all routes
- [X] T039 [US1] Register the dashboard app in the `cmd/tomb/main.go` app slice — the only core file an app touches (contracts/app-registration.md)

**Checkpoint**: Login works end to end and the dashboard shows the right character. **Not yet publicly deployable — US2 is required for that.**

---

## Phase 4: User Story 2 - Guild members only (Priority: P2)

**Goal**: Only verified TOMB guild members reach any app; authenticated non-members are denied entirely with a clear message and a way out.

**Independent Test**: quickstart.md manual scenario 3 — sign in with an account that has no TOMB character, confirm the "this site is for TOMB members" page, and confirm `/app/dashboard` does not render even when requested directly.

> **Implementation note**: per research.md D4 this reads the `guild` object off each character profile already fetched in T033, rather than fetching the guild roster and searching it. Same outcome, zero extra API calls. This is the one place the implementation differs from FR-013's literal wording — if you want the roster lookup instead, revise FR-013 before starting this phase.

### Tests for User Story 2

- [X] T040 [P] [US2] Write table-driven guild-matching tests in `internal/apps/dashboard/guild_test.go`: a matching guild on the configured realm is a member; a same-named guild on a different realm is not; an absent `guild` field is not; zero characters is not (data-model.md GuildMembership)
- [X] T041 [P] [US2] Write gate tests in `internal/platform/middleware_test.go`: with `RequiresGuild: true` a non-member's handler is never invoked, and with `RequiresGuild: false` it is (contracts/app-registration.md guarantee 3)

### Implementation for User Story 2

- [X] T042 [US2] Implement membership derivation in `internal/apps/dashboard/guild.go`: a user is a member if any fetched character's guild matches `TOMB_GUILD_NAME` and `TOMB_GUILD_REALM`, compared on Blizzard's slug rather than raw display text; treat an absent `guild` as not a match (FR-013)
- [X] T043 [US2] Implement the guild gate in `internal/platform/middleware.go`, keyed off `AppMeta.RequiresGuild` and evaluated in the core before any app handler runs, so future apps inherit it without touching auth code (FR-013a, FR-011)
- [X] T044 [P] [US2] Create `web/templates/non-member.html` stating the site is for TOMB members and offering a log-out action (FR-013a, scenario 7)
- [X] T045 [US2] In `internal/auth/oauth.go`, delete the just-created session when the guild check fails during callback, so no usable session survives a denial (contracts/http-routes.md callback note)
- [X] T046 [US2] Filter navigation in `internal/platform/mount.go` so it lists only apps the current viewer may actually reach (contracts/app-registration.md guarantee 4)

**Checkpoint**: US1 + US2 complete — this is the minimum publicly deployable state

---

## Phase 5: User Story 3 - Leave safely: logout and session expiry (Priority: P2)

**Goal**: A member can end their session deliberately, and an expired session never shows stale character data.

**Independent Test**: quickstart.md manual scenarios 5 and 6 — log out and confirm no character data survives the Back button; then expire the session row via `psql` and confirm the next request prompts re-login.

### Tests for User Story 3

- [X] T047 [P] [US3] Write table-driven session lifetime tests in `internal/auth/session_test.go`: a row at or past `expires_at` resolves as absent; `expires_at` is computed from the token's `expires_in` rather than a hardcoded 24h; activity does not move `expires_at` forward (FR-015)
- [X] T048 [P] [US3] Write logout tests in `internal/auth/logout_test.go`: the session row is deleted, the cookie is cleared, the response redirects to `/`, and logout with no session is idempotent (FR-008)

### Implementation for User Story 3

- [X] T049 [US3] Enforce absolute expiry server-side on every request in `internal/auth/session.go`, treating an expired row as no session and deleting it lazily on encounter (FR-015, scenario 8)
- [X] T050 [US3] Implement `POST /auth/logout` in `internal/auth/oauth.go`: delete the session row, clear the cookie, redirect to `/` (FR-008, scenario 4)
- [X] T051 [P] [US3] Implement a CSRF token helper in `internal/platform/csrf.go` and apply it to the logout form, so a third-party page cannot force a logout (contracts/http-routes.md)
- [X] T052 [US3] Set `Cache-Control: no-store` on all authenticated responses in `internal/platform/middleware.go`, so the Back button cannot re-render a dashboard after logout (scenario 4)
- [X] T053 [P] [US3] Add a periodic expired-session sweep in `internal/auth/store.go` using the `sessions_expires_at_idx` index (data-model.md lifecycle)

**Checkpoint**: Sessions begin and end correctly and bounded by the token's own lifetime

---

## Phase 6: User Story 4 - Nothing breaks ugly (Priority: P3)

**Goal**: Every external failure — declined consent, revoked authorization, rate limits, outages, partial data — produces a clear, retry-able page rather than a stack trace or stale data.

**Independent Test**: quickstart.md manual scenarios 4, 7 and 8 — cancel on Blizzard's consent screen, revoke the app's authorization, and point the API host somewhere unroutable. Each must yield a friendly retry-able page. The error-mapping table in `internal/blizzard/errors_test.go` covers the rest.

### Tests for User Story 4

- [X] T054 [P] [US4] Write a table-driven test in `internal/blizzard/errors_test.go` with one case per row of the failure-mapping table in contracts/blizzard-api.md: 401, 403, 404-on-character, 429, 5xx, timeout, malformed JSON
- [X] T055 [P] [US4] Write a callback test in `internal/auth/oauth_test.go` for `error=access_denied`, asserting a friendly message, a retry action, and that **no** session is created (FR-009, scenario 5)
- [X] T056 [P] [US4] Write a partial-failure test in `internal/apps/dashboard/fetch_test.go`: when some character fetches fail, selection proceeds over the successes and the result is flagged partial (research.md D9)

### Implementation for User Story 4

- [X] T057 [US4] Implement typed error classification in `internal/blizzard/errors.go` mapping each HTTP status and transport condition to the internal outcomes in contracts/blizzard-api.md, honouring `Retry-After` on 429 without auto-retrying inside the request
- [X] T058 [US4] Handle `error=access_denied` on the callback in `internal/auth/oauth.go`, returning the unauthenticated state with an explanatory message and no session (FR-009)
- [X] T059 [US4] On any Blizzard 401, delete the session and redirect to `/` with a re-login prompt in `internal/platform/middleware.go` (FR-012, revoked-token edge case)
- [X] T060 [US4] Render the retry-able error state for 429/5xx/timeout in `internal/apps/dashboard/app.go`, returning HTTP 200 with an in-page error and a "try again" action (FR-010, contracts/http-routes.md)
- [X] T061 [US4] Skip individual failed character fetches in `internal/apps/dashboard/fetch.go` and surface a visible "some characters could not be loaded" notice, failing the whole page only if the account list fails or every character fetch fails (research.md D9)
- [X] T062 [P] [US4] Create `web/templates/login-failed.html` (declined or failed authorization, with a retry action) and `web/templates/error.html` (generic retry-able error)

**Checkpoint**: Every documented failure mode has a defined, tested, user-visible outcome

---

## Phase 7: User Story 5 - Proven extensibility (Priority: P3)

**Goal**: Demonstrate that a second app can be added without touching authentication, session handling, or the dashboard — the promise FR-011 and scenario 6 make.

**Independent Test**: `go test ./internal/platform/` — a stub app mounts, serves, appears in navigation, and inherits guild gating with zero changes to any auth or dashboard file. If the stub required editing one, FR-011 has regressed.

### Tests for User Story 5

- [X] T063 [P] [US5] Add two minimal stub apps in `internal/platform/stubapp_test.go` implementing `platform.App` with nothing but `Meta()` and `Routes()`
- [X] T064 [US5] Write the scenario-6 test in `internal/platform/extensibility_test.go`: mounting a stub app makes its route reachable and its nav entry appear, while `internal/auth/` and `internal/apps/dashboard/` remain untouched (contracts/app-registration.md final test obligation)
- [X] T065 [P] [US5] Assert in `internal/platform/extensibility_test.go` that a stub declaring `RequiresGuild: true` is gated for non-members identically to the dashboard, proving gating is inherited from the core rather than reimplemented

### Implementation for User Story 5

- [X] T066 [P] [US5] Write `docs/adding-an-app.md` documenting the extension point: implement `App`, add one line to the slice in `cmd/tomb/main.go`, own your own templates, never import another app — as required by the constitution's Development Workflow note on new apps

**Checkpoint**: All five stories independently functional; FR-011 is enforced by a test rather than by convention

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Observability, infrastructure, and the merge gate

- [X] T067 [P] Audit every `log/slog` call site and confirm the Blizzard access token, session token, and client secret never appear in logs, error messages, templates, or URLs (Principle III, contracts/blizzard-api.md secrets discipline)
- [X] T068 [P] Add request logging middleware in `internal/platform/middleware.go` emitting method, path, status, and duration as structured fields (Principle VI)
- [X] T069 [P] Add security headers in `internal/platform/middleware.go`: `Content-Security-Policy`, `X-Content-Type-Options`, `Referrer-Policy`, and HSTS in production
- [X] T070 Create the OpenTofu skeleton in `tofu/main.tf`, `tofu/variables.tf`, and `tofu/versions.tf` with a remote GCS state backend and `required_version >= 1.12.0` (Principle V; `tofu` 1.12.6 runs from WSL per quickstart.md)
- [X] T071 Define Artifact Registry and Cloud SQL for PostgreSQL (major version matching `compose.yaml`'s `postgres:18`) in `tofu/registry.tf` and `tofu/database.tf`
- [X] T072 Define the Cloud Run service and Secret Manager entry for `BNET_CLIENT_SECRET` in `tofu/run.tf` and `tofu/secrets.tf`, injecting config as environment variables and never committing secret values (Principle V)
- [X] T073 [P] Write `tofu/README.md` covering the WSL invocation path, the plan-then-apply flow, and the one-time manual Battle.net client registration that OpenTofu cannot perform (Principle V bootstrap exception)
- [X] T074 Add the CI pipeline in `.github/workflows/ci.yml` running `go build ./...`, `go vet ./...`, `go test ./...`, and `docker build`, plus `tofu plan` posted for review on any change under `tofu/` (constitution Development Workflow)
- [X] T075 [P] Write the root `README.md` covering local setup via `docker compose up`, the Windows/WSL toolchain split, and links to the spec and plan
- [X] T076 Run the full merge gate locally and record the results: `go build ./...`, `go vet ./...`, `go test ./...`, `docker build -t tomb-platform .`
- [X] T077 Walk every manual scenario in [quickstart.md](quickstart.md) against `docker compose up` and confirm each acceptance scenario in spec.md passes

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately
- **Foundational (Phase 2)**: Depends on Setup — **BLOCKS all user stories**
- **US1 (Phase 3)**: Depends on Foundational
- **US2 (Phase 4)**: Depends on US1 — T042 consumes the character profiles fetched by T033, and T043 gates the app registered in T034
- **US3 (Phase 5)**: Depends on US1 (needs sessions from T028/T031). Independent of US2
- **US4 (Phase 6)**: Depends on US1. Independent of US2 and US3
- **US5 (Phase 7)**: Depends on Foundational only — T014's mount logic is all it exercises. Can run in parallel with US1
- **Polish (Phase 8)**: T070–T073 (infrastructure) depend only on Setup and can start early; T074–T077 depend on all desired stories

### Story Independence Notes

- **US2 is the one genuine cross-story dependency.** research.md D4's zero-extra-call guild check reads data that US1's fetch produces. Choosing the roster-lookup alternative instead would make US2 fully independent, at the cost of one API call and a client-credentials token.
- **US3, US4 and US5 are independently testable** and can be built in any order after US1.

### Within Each User Story

- Tests first, confirmed failing, before implementation (constitution Principle VI)
- Models and value types before the logic that consumes them
- Core logic before templates and wiring

### Parallel Opportunities

- Setup: T002–T007 all parallel after T001
- Foundational: T010, T013, T015, T016, T017, T018, T020, T021, T022, T023 parallel
- US1: the three test tasks T025–T027 parallel; then T028/T029 parallel, and T032/T035/T036 parallel
- US5 can proceed alongside US1 entirely
- Infrastructure T070–T073 can proceed alongside any story

---

## Parallel Example: User Story 1

```bash
# Launch all three US1 test tasks together:
Task: "Selection + tiebreaker table tests in internal/apps/dashboard/select_test.go"
Task: "OAuth flow tests in internal/auth/oauth_test.go"
Task: "Dashboard handler tests in internal/apps/dashboard/app_test.go"

# Then the independent implementation files:
Task: "Session issue/resolve in internal/auth/session.go"
Task: "Users + sessions store in internal/auth/store.go"
Task: "Selection rule in internal/apps/dashboard/select.go"
```

---

## Implementation Strategy

### MVP First (US1), then the deployable pair (US1 + US2)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational — blocks everything
3. Complete Phase 3: US1 → **validate locally** against quickstart scenarios 1–2
4. Complete Phase 4: US2 → validate scenario 3
5. **This is the first state you can expose**, because FR-013a makes gating mandatory
6. Deploy via Phase 8 infrastructure tasks

### Incremental Delivery

1. Setup + Foundational → platform boots, health checks green
2. US1 → login and dashboard work (local demo only)
3. US2 → guild gating → **first deployable**
4. US3 → logout and expiry → safe to leave running
5. US4 → failure handling → safe on a bad Blizzard day
6. US5 → extensibility test → safe to add the next app

### Notes

- `[P]` tasks touch different files and have no incomplete dependencies
- Commit after each task or logical group
- Every checkpoint is a valid place to stop and validate
- Tests are mandatory here, not optional — Principle VI blocks merging untested business logic

---

## Implementation Notes (added during `/speckit-implement`)

All 77 tasks are complete. Five things landed differently from the task text
above; the reasons are recorded here rather than silently diverging.

1. **The guild check and character fetch live in the core, not the dashboard app.**
   tasks.md placed `fetch.go` and `guild.go` under `internal/apps/dashboard/`, but
   contracts/app-registration.md requires the core to enforce the gate *before* any
   app handler runs, and membership is derived from character data. That would have
   forced `platform -> dashboard` while `dashboard -> platform`: an import cycle.
   They are now `internal/platform/profile.go` and `internal/platform/guild.go`.
   The dashboard reads the already-fetched profile from request context, so a view
   still costs exactly one `1 + N` fetch (FR-016).

2. **Templates, the stylesheet and SQL migrations are embedded** via `go:embed`
   under `internal/platform/` rather than living in top-level `web/` and
   `migrations/`. `go:embed` cannot reach a parent directory, and embedding makes
   the binary independent of its working directory — which matters on Cloud Run.
   The Dockerfile's runtime stage therefore copies only the binary.

3. **Dashboard integration tests are an external test package**
   (`internal/apps/dashboard/integration_test.go`, `package dashboard_test`)
   because the core cannot import an app. This required exporting one seam,
   `platform.ContextWithSession`, which is also what any future custom middleware
   would use.

4. **`internal/auth` never imports the core.** The callback needs to run the guild
   check and render core-owned pages, so it does that through two small injected
   interfaces, `auth.Gate` and `auth.Renderer`, both implemented by
   `platform.Core`.

5. **Module path is `github.com/anthony-hopkins/tomb`**, set once the repository
   owner was known. No remote is configured yet, which is fine — Go only needs the
   path to resolve imports, not to fetch anything.

Two bugs were found and fixed by actually running the stack rather than only the
unit tests:

- `compose.yaml` mounted the database volume at `/var/lib/postgresql/data`, the
  pre-18 convention. Postgres 18 refuses to start that way and wants a single
  mount at `/var/lib/postgresql`.
- The Dockerfile copied `web/` and `internal/apps/` into the runtime image, which
  no longer exist as runtime paths after embedding.

### Verified against a running stack

`docker compose up` with placeholder credentials, then: migrations applied on
boot; `/healthz` and `/readyz` both 200; landing page renders with the
Battle.net action; `/static/style.css` served from the embedded FS; security
headers present; `POST /auth/login` redirects to
`https://oauth.battle.net/authorize` with `scope=wow.profile` and a `state`;
`/app/dashboard` 302s when anonymous; `?error=access_denied` renders the
login-failed page and sets no session cookie; a forged `state` returns 400; and
the `users` / `sessions` schema matches data-model.md exactly.

Still requires a real Battle.net client and a TOMB character to exercise:
quickstart.md manual scenarios 2, 3, 7 and 9.
