<!--
Sync Impact Report
- Version change: N/A (initial) → 1.0.0
- Modified principles: none (initial ratification)
- Added sections: Core Principles (I–VII), Technology Constraints, Development Workflow, Governance
- Removed sections: none
- Templates requiring updates: .specify/templates/plan-template.md (⚠ pending — align "Constitution Check"
  gate with Principles I, III, V once template is generated), .specify/templates/spec-template.md (✅ no
  conflicts), .specify/templates/tasks-template.md (⚠ pending — add task categories for app-registration,
  IaC plan/apply, container build)
- Follow-up TODOs: TODO(DB_ENGINE) — confirm PostgreSQL is the chosen datastore before /plan for the first
  feature; TODO(GUILD_VERIFICATION) — decide how TOMB membership is verified (Blizzard guild roster API vs.
  manual allow-list) before implementation of guild-gated apps.
-->

# TOMB Guild Platform Constitution

## Core Principles

### I. Standard-Library-First (NON-NEGOTIABLE)
The Go standard library MUST be the default tool for every capability: HTTP routing and
serving (`net/http`), JSON handling (`encoding/json`), templating (`html/template`),
structured logging (`log/slog`), and testing (`testing`). A third-party dependency MAY be
introduced only when the standard library cannot deliver the required functionality
maturely, securely, or without substantial reinvention (e.g., a SQL database driver, or an
OAuth2/OIDC client for Battle.net). Every new dependency MUST be justified in writing in the
relevant `plan.md` (what it replaces, why the standard library is insufficient, and the
exit cost if it must be removed later). Dependencies that merely offer convenience or
stylistic preference over the standard library are rejected.

**Rationale**: minimizes supply-chain surface area, upgrade churn, and vendor lock-in for a
small guild-run project that must remain maintainable by volunteers over years.

### II. Modular "Apps" Architecture
The platform is a thin core (routing, session/auth, shared layout, shared data access)
plus independently addable **apps** (e.g., "Character Dashboard", "Roster", "Raid
Planner"). Every app MUST register itself into the core through a single, documented
extension point (route registration + navigation entry) and MUST NOT require edits to
unrelated apps or to core authentication logic to be added, removed, or disabled. Each app
owns its own package, its own templates, and its own data-access code; cross-app
coupling MUST go through explicit shared interfaces, never through reaching into another
app's internals.

**Rationale**: the site is explicitly scoped to grow ("several apps... extensible with
more in the future"); the core must stay stable while the app surface grows.

### III. Battle.net Identity as the Root of Trust
All authentication MUST flow through Blizzard's Battle.net OAuth2 SSO. The platform MUST
NOT collect, prompt for, or store a user's Blizzard password under any circumstance. Only
OAuth2 tokens (and data derived from them) may be persisted, and only for as long as
needed to maintain a session or refresh character data. Character and account data MUST be
sourced from Blizzard's official Game Data / Profile APIs, never guessed, scraped, or
hand-entered on the user's behalf.

**Rationale**: this is a guild identity platform; trust in "who is this person" must rest
entirely on Blizzard's own identity provider, not a homegrown credential store.

### IV. Container-First Delivery
Every deployable component MUST ship with a `Dockerfile`, and the full local development
stack MUST come up with a single `docker compose up` — no undocumented host-machine
dependencies (no "install Go 1.x and Postgres locally first"). The same images built for
local development are the images promoted to production; environment-specific behavior is
controlled by configuration (env vars), never by rebuilding with different source.

**Rationale**: guarantees "works on my machine" parity and makes the deployment pipeline
testable before it ever touches Google Cloud.

### V. Infrastructure as Code via OpenTofu (NON-NEGOTIABLE)
All Google Cloud resources MUST be defined and changed exclusively through OpenTofu
configuration checked into the repository. Manual changes made directly in the Google
Cloud Console ("ClickOps") are forbidden except for one-time, documented bootstrap steps
that OpenTofu itself cannot perform (e.g., initial billing account linkage). Deployments to
any environment MUST occur through an automated pipeline that runs `tofu plan` for review
and `tofu apply` only after that plan is approved (human approval for production; may be
automatic for lower environments if the constitution is amended to say so). Secrets MUST
NOT be committed to the repository or hardcoded in OpenTofu files; they are injected via a
secrets manager or CI-provided variables.

**Rationale**: reproducible, auditable infrastructure is required for a project maintained
by volunteers who will rotate over time; state drift from manual changes is the primary
failure mode this principle prevents.

### VI. Test Discipline & Observability
Business logic (character-selection logic, session handling, app registration, guild
membership checks) MUST have Go table-driven tests. All services MUST log with
`log/slog` in a structured (key/value) form, and MUST expose a liveness/readiness (health)
endpoint suitable for container orchestration and uptime checks. Untested business logic
MUST NOT be merged; UI/template rendering may rely on lighter smoke tests where full
coverage is impractical.

**Rationale**: keeps a volunteer-maintained codebase debuggable in production without
requiring the original author to be present.

### VII. Simplicity & YAGNI
Prefer the smallest, most explicit solution that satisfies the current spec. Frameworks,
abstraction layers, or generalized "plugin systems" beyond what Principle II already
requires MUST NOT be introduced speculatively — only once a second concrete app or use
case proves the abstraction is needed. Every feature spec MUST be traceable to an actual
guild need, not a hypothetical future one.

**Rationale**: this is a guild hobby project; complexity must earn its place.

## Technology Constraints

- **Language/runtime**: Go, latest stable minor version, pinned in `go.mod`. No other
  application languages in the request/response path (build tooling and IaC languages are
  exempt).
- **Approved dependency exceptions** (still require the justification note in Principle I,
  but are pre-approved as categories): a SQL database driver; a Battle.net-compatible
  OAuth2 client; `golang.org/x/*` sub-repositories (maintained by the Go team, held to the
  same stability bar as the standard library). Web UI frameworks, ORMs, and general-purpose
  "batteries included" HTTP frameworks are NOT pre-approved and require explicit
  case-by-case justification against Principle I.
- **Frontend rendering**: server-rendered HTML via `html/template` by default; JavaScript is
  added only where a specific interaction genuinely requires it, and a full SPA framework
  is out of scope unless a future amendment says otherwise.
- **Identity provider**: Battle.net OAuth2/OIDC (Blizzard Developer Portal application),
  scoped to the WoW profile API.
- **Datastore**: TODO(DB_ENGINE) — to be confirmed in the first implementation plan;
  whatever is chosen MUST be reachable only via a driver that fits the Principle I
  exception, and MUST run as a container in the compose stack for local development.
- **Containerization**: Docker + Docker Compose for all environments below production.
- **Cloud & IaC**: Google Cloud Platform, provisioned exclusively via OpenTofu.

## Development Workflow

- Features proceed through the Spec Kit flow: `/constitution` → `/specify` → `/plan` →
  `/tasks` → implementation. No implementation work begins without an approved spec.
- Every pull request MUST pass: `go build`, `go vet`, `go test ./...`, and a successful
  Docker image build before merge.
- Any change touching Google Cloud infrastructure MUST include the OpenTofu diff (`tofu
  plan` output) in the pull request for review before `tofu apply` runs.
- New apps (Principle II) MUST include a short note in their spec on how they register
  with the core and what, if any, guild-membership gating they require.

## Governance

This constitution supersedes ad hoc practice for the TOMB guild platform. All specs,
plans, and task lists MUST be checked against these principles before implementation, and
any deliberate deviation MUST be recorded and justified in the relevant `plan.md`'s
Complexity Tracking section rather than silently ignored.

**Amendment procedure**: amendments are proposed via the `/constitution` command (or a
direct edit reviewed like any other change), must state the rationale for the change, and
take effect once merged. Amendments that remove or redefine a principle require a MAJOR
version bump; adding a principle or materially expanding guidance requires MINOR; wording
or clarification fixes are PATCH.

**Compliance review**: each `/plan` invocation MUST include a Constitution Check section
confirming the proposed design does not violate any principle above, or explicitly
justifying any exception.

**Version**: 1.0.0 | **Ratified**: 2026-09-13 | **Last Amended**: 2026-09-13
