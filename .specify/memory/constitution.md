<!--
Sync Impact Report
- Version change: 1.0.0 → 2.0.0 (MAJOR — a principle was redefined, per the amendment procedure)
- Modified principles: IV. Container-First Delivery → IV. Container-First Delivery and Environment
  Promotion. The mandated local development stack (`docker compose up` on a workstation) is REMOVED and
  replaced with an explicit prohibition on a supported local runtime environment; testing of running
  software moves to a deployed Google Cloud environment. V gains a concrete rule for lower environments
  in place of the "if the constitution is amended to say so" placeholder it carried.
- Modified sections: Technology Constraints (datastore, containerization, cloud); Development Workflow
  (adds the develop → main branch promotion model and an environment-parity requirement)
- Added sections: Development Workflow → Transitional exception, recording that the develop environment
  does not exist yet and production is currently the only deployed environment
- Removed sections: none
- Resolved TODOs: TODO(DB_ENGINE) — PostgreSQL 18, self-hosted on the VM (f8b374d), now stated outright in
  Technology Constraints. TODO(GUILD_VERIFICATION) — settled by the shipped implementation: membership is
  read from the `guild` object already present on the character profile response, re-derived per request
  and never cached.
- Repository changes this amendment requires: DELETE root `compose.yaml` and `.env.example`; remove the
  `run` target from `Makefile`; update `README.md` and `docs/deployment.md`.
- Follow-up TODOs: TODO(DEVELOP_ENV) — the configuration for the develop environment now exists
  (`tofu/environments.tf` selects it by OpenTofu workspace; `deploy.yml` applies it on a push to
  `develop`), but it has not been APPLIED yet, so the Transitional exception below still stands.
  Delete that exception once develop's first deploy has completed and DNS for its hostname resolves.
  Procedure: docs/deployment.md, "Standing up the develop environment".
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

### IV. Container-First Delivery and Environment Promotion
Every deployable component MUST ship with a `Dockerfile`. The image built from a commit is
the artifact promoted to every environment, byte for byte; environment-specific behaviour
MUST come from configuration (environment variables and instance metadata), never from
rebuilding with different source, a different Dockerfile, or a different Compose file.

**There is no supported local runtime environment.** A stack assembled on a workstation
cannot be 1:1 with production — it terminates no TLS, runs no reverse proxy, performs no
ACME, and answers a different OAuth callback — and a lower environment that quietly differs
from production manufactures false confidence, which is worse than having no lower
environment at all. Testing of *running software* therefore happens in a deployed Google
Cloud environment, never on a developer machine.

This constrains environments, not tooling. Compiling, `go vet`, `go test`, and building the
image on a workstation are not environments and are unrestricted; Principle VI still
requires those tests to exist and to pass.

**Rationale**: the divergence between a convenient local stack and the real thing is
precisely where deploy-time surprises come from. Promoting one immutable image through
environments that are genuinely alike makes a deploy predictable, and makes the lower
environment's verdict worth something.

### V. Infrastructure as Code via OpenTofu (NON-NEGOTIABLE)
All Google Cloud resources MUST be defined and changed exclusively through OpenTofu
configuration checked into the repository. Manual changes made directly in the Google
Cloud Console ("ClickOps") are forbidden except for one-time, documented bootstrap steps
that OpenTofu itself cannot perform (e.g., initial billing account linkage). Deployments to
any environment MUST occur through an automated pipeline that runs `tofu plan` for review
and `tofu apply` only after that plan is approved. Production requires human approval; the
develop environment MAY apply automatically on a push to `develop`, since its whole purpose
is to be deployed to without ceremony. Secrets MUST
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
- **Datastore**: PostgreSQL 18, self-hosted as a container on the deployment VM with its
  data on a separate persistent disk. Reachable only via a driver that fits the Principle I
  exception. The major version MUST be identical in every environment.
- **Containerization**: Docker + Docker Compose in every deployed environment, production
  included. The Compose project travels inside the application image so a deployed host
  needs no checkout of this repository.
- **Cloud & IaC**: Google Cloud Platform, provisioned exclusively via OpenTofu. Every
  environment is stood up from the same OpenTofu configuration, parameterised — never from
  a hand-edited copy.

## Development Workflow

- Features proceed through the Spec Kit flow: `/constitution` → `/specify` → `/plan` →
  `/tasks` → implementation. No implementation work begins without an approved spec.
- **Branch promotion.** `develop` is the integration branch; `main` is production. Work
  lands on a feature branch, opens a pull request into `develop`, and is exercised as
  running software in the **develop environment** — the only place that testing happens,
  per Principle IV. Promotion to production is a pull request from `develop` into `main`.
  Nothing is pushed directly to `main`, and no commit reaches `main` that has not been
  deployed to and exercised in develop first.
- The two environments MUST be stood up from the same OpenTofu configuration and run the
  same Compose project, differing only in parameters (project or hostname, machine size).
  A difference that cannot be expressed as a parameter is a defect in the configuration,
  not an acceptable environment quirk.
- Every pull request MUST pass: `go build`, `go vet`, `go test ./...`, and a successful
  Docker image build before merge.
- Any change touching Google Cloud infrastructure MUST include the OpenTofu diff (`tofu
  plan` output) in the pull request for review before `tofu apply` runs.
- New apps (Principle II) MUST include a short note in their spec on how they register
  with the core and what, if any, guild-membership gating they require.

**Transitional exception (from 2026-09-13, until the develop environment exists).** The
develop environment has not been built yet, so today `main` is the only deployed
environment and changes are exercised in production. This is a knowingly accepted risk of a
greenfield project with no users, not a standing permission: it expires the moment the
develop environment is stood up, and until then production deploys carry the full weight of
Principle V's human approval. The branch promotion rule above is in force regardless —
`develop` → `main` is how code moves, whether or not develop has an environment attached.

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

**Version**: 2.0.0 | **Ratified**: 2026-09-13 | **Last Amended**: 2026-09-13
