# Phase 0 Research: Battle.net SSO Login & Character Dashboard

**Feature**: `001-battlenet-sso-character-dashboard`
**Date**: 2026-09-13
**Status**: Complete — no `NEEDS CLARIFICATION` items remain

All findings below were verified against Blizzard's live OIDC discovery document and
current developer documentation/forums (see Sources). Where a fact could not be
confirmed authoritatively it is marked **UNCONFIRMED** with the fallback behaviour.

---

## D1: Battle.net OAuth2 endpoints and flow

**Decision**: Use the authorization-code flow against the unified `oauth.battle.net`
host as a **confidential client** (client ID + secret held server-side), with a
cryptographically random `state` parameter bound to the pre-login session for CSRF
protection.

Verified from `https://oauth.battle.net/.well-known/openid-configuration`:

| Purpose | Endpoint |
|---|---|
| Issuer | `https://oauth.battle.net` |
| Authorization | `https://oauth.battle.net/authorize` |
| Token | `https://oauth.battle.net/token` |
| UserInfo | `https://oauth.battle.net/userinfo` |
| JWKS | `https://oauth.battle.net/jwks/certs` |

`grant_types_supported` includes `authorization_code`. Scope required for this
feature: `wow.profile` — the only scope requested, satisfying FR-002's "only the
scopes needed".

**UNCONFIRMED**: the discovery document does **not** advertise
`code_challenge_methods_supported`, so PKCE support cannot be assumed. Because this
is a confidential server-side client, `state` plus the client secret is sufficient;
PKCE can be added if a probe confirms support, and nothing in the design depends on it.

**Rationale**: `oauth.battle.net` is the current unified host; the older
per-region `{region}.battle.net/oauth` paths are legacy.

**Alternatives considered**: treating Battle.net as a full OIDC provider and
validating an `id_token` against JWKS. Rejected as unnecessary — `response_types`
does include `code id_token`, but the `userinfo` endpoint yields the same identity
claims with less code and no JWT verification to write (Principle VII).

---

## D2: No refresh tokens — session lifetime is capped by the token

**Decision**: The site session expires at the same absolute moment as the Blizzard
access token. Session lifetime is taken from `expires_in` in the token response. No
sliding renewal, no background refresh.

**Finding**: Battle.net's authorization-code flow does **not** issue a refresh
token, and access tokens carry an `expires_in` of ~86399 seconds (24 hours). Tokens
are additionally invalidated early if the user changes their password, revokes the
application's authorization, or the account is locked.

**Consequence**: this is the mechanism behind the spec's FR-015 decision. A longer
site session would leave a user nominally "logged in" while the site can no longer
call Blizzard on their behalf — which FR-016 (fetch on every view) would surface as a
hard failure on the very next page load. Deriving expiry from `expires_in` rather
than hardcoding 24h also means an early-expiring token is honoured correctly.

**Alternatives considered**: a 7- or 30-day sliding site session with re-auth only on
API failure. Rejected — it converts an expected, predictable event into an error
path, and contradicts FR-012.

---

## D3: Where character data actually lives (this drives the call pattern)

**Decision**: Two-stage fetch per dashboard view:

1. `GET /profile/user/wow` (namespace `profile-{region}`, user's access token) —
   returns the account's WoW characters: name, realm, id, level, class, faction.
   **Does not include `last_login_timestamp`.**
2. `GET /profile/wow/character/{realmSlug}/{characterName}` per character — returns
   `last_login_timestamp`, `average_item_level`, `equipped_item_level`, `level`,
   `character_class`, `active_spec`, `realm`, **and `guild`**.

**Finding**: `last_login_timestamp` is exposed only on the per-character profile
summary — not on the account summary, and not on the guild roster. Selecting the
"most recently played" character (FR-006) therefore *requires* one call per
character. This is a long-standing shape of the API; a guild-scale consumer in
Blizzard's own forums confirms the per-character fan-out is the only route to
last-login data, and the standing feature request is to add the field to the roster
response precisely because it is absent there.

**Consequence**: a dashboard view costs `1 + N` API calls for a user with `N`
characters. Combined with FR-016 (no caching), that cost is re-paid on every view and
every browser refresh. See D9 for how the fan-out is bounded.

**Alternatives considered**: deriving "most current" from the account summary alone.
Rejected — the required field is not present there, so it is not implementable.

---

## D4: Guild verification without a separate roster call

**Decision**: Verify TOMB membership from the `guild` object already present in each
character's profile summary (fetched in D3 step 2): a user is a member if any of
their characters reports a guild matching the configured TOMB guild name and realm,
compared on Blizzard's slug/id rather than raw display text.

**Rationale**: the per-character profile summary is already being fetched for
`last_login_timestamp`, and it carries `guild` in the same response. Verification
therefore costs **zero additional API calls**, needs no second client-credentials
token, and touches no separate Game Data endpoint. Both sources are equally
authoritative, because both are Blizzard's own data.

**Relationship to FR-013 — please read**: FR-013 specifies checking the user's
characters "against the TOMB guild roster retrieved from Blizzard's Profile API,
using a configured guild name and realm". This decision satisfies the requirement's
intent and every constraint it places on the outcome — Blizzard-sourced, configured
guild name plus realm, never hand-entered (Principle III) — but it inverts the
direction of the lookup: it reads the guild off each character instead of reading the
roster and searching it. The answer is identical for this feature. It is flagged here
rather than silently substituted; if you want the literal roster lookup, say so and
FR-013's implementation changes to add one Game Data call plus a client-credentials
token.

**One behavioural difference worth knowing**: a character's `guild` field is omitted
entirely when the character is in no guild, so "no guild" and "guild absent from
response" are the same case and must both be treated as "not a member".

**Note for a future Roster app**: that app *will* need
`/data/wow/guild/{realmSlug}/{nameSlug}/roster` plus a client-credentials token. This
decision defers that work rather than ruling it out (Principle VII).

**Alternatives considered**: fetch the full TOMB roster once per login and match
against it. Rejected for this feature — an extra endpoint, an extra token type, and
an extra failure mode, for an identical answer.

---

## D5: Datastore — resolves constitution `TODO(DB_ENGINE)`

**Decision**: **PostgreSQL**, accessed through `database/sql` with the `pgx/v5`
driver. Pin one major version (e.g. 18) identically in `compose.yaml` and OpenTofu.
Locally: a `postgres` container in the compose stack. In Google Cloud: Cloud SQL for
PostgreSQL.

**What is actually stored** — very little, because FR-016 removed character data from
the persistence picture entirely:

- `users` — internal id, Blizzard `sub`, battletag, first/last site login
- `sessions` — session token hash, user id, Blizzard access token, absolute expiry

No character table. No cache table.

**Rationale**: Postgres fits the Principle I pre-approved "SQL database driver"
exception, runs as a container for local parity (Principle IV), and has a managed
Cloud SQL equivalent that OpenTofu can provision (Principle V). `pgx/v5` is the
de-facto Go Postgres driver and is usable through the stdlib `database/sql`
interface, so no ORM enters the project (Principle I).

**Alternatives considered**:

- *SQLite* — genuinely tempting given how little is stored, and simpler locally.
  Rejected because Cloud Run instances are ephemeral and horizontally scaled, so a
  file-backed database either loses every session on each revision deploy or forces
  single-instance hosting.
- *No database at all (in-memory sessions)* — the smallest possible thing, and it
  would work for one instance. Rejected because every deploy would sign out every
  member, and the `users` table is where the spec's "first/last login to the site"
  attributes live.
- *Firestore / Datastore* — would pull in a large cloud SDK dependency surface for a
  relational workload of two tables.

---

## D6: Session mechanics

**Decision**: Server-side sessions keyed by an opaque random token.

- 256 bits from `crypto/rand`, base64url-encoded, set as an `HttpOnly`, `Secure`,
  `SameSite=Lax` cookie with `Path=/`.
- Only a SHA-256 **hash** of the token is stored in `sessions`, so database
  disclosure does not yield usable session cookies.
- `SameSite=Lax` rather than `Strict`, because the OAuth callback is a cross-site
  top-level redirect back from Blizzard and must arrive with the pre-login cookie
  intact.
- Cookie `Max-Age` and the row's `expires_at` both come from the token's `expires_in`
  (D2). Expiry is enforced server-side on every request; the cookie lifetime is a
  convenience, never the authority.
- The Blizzard access token lives only in the session row, server-side. It is never
  written to a cookie, a template, a URL, or a log line.
- Logout deletes the session row and clears the cookie (FR-008).

**Rationale**: opaque server-side sessions let logout and revocation take effect
immediately, which FR-008 and FR-012 both require. A self-contained signed cookie
could not be invalidated server-side without adding a revocation list — i.e. the
same database row, with extra steps.

**Alternatives considered**: an encrypted cookie carrying the access token
(stateless). Rejected — it puts a live Blizzard credential in the browser and makes
FR-008's "terminate the session" unenforceable.

---

## D7: Dependency justifications (Principle I gate)

Every non-stdlib dependency, what it replaces, and its exit cost:

| Dependency | Replaces | Why the stdlib is insufficient | Exit cost |
|---|---|---|---|
| `golang.org/x/oauth2` | Hand-rolled code exchange and token plumbing | No stdlib OAuth2 client exists. Pre-approved twice over: "an OAuth2/OIDC client for Battle.net" and the `golang.org/x/*` exception. Battle.net's endpoints are supplied as a literal `oauth2.Endpoint`. | Low — one config struct and one exchange call; roughly 50 lines of `net/http` to replace. |
| `github.com/jackc/pgx/v5` | Postgres wire protocol | No SQL driver ships with Go; `database/sql` is an interface that requires one. Pre-approved "SQL database driver" category. | Low — used via `database/sql`, so swapping drivers is an import change. |
| `golang.org/x/sync/errgroup` | Bounded concurrent character fetches with first-error cancellation (D9) | Achievable with `sync.WaitGroup` and channels, but `SetLimit` is exactly the needed primitive. Covered by the `golang.org/x/*` exception. | Very low — about 20 lines of stdlib. |

No web framework, no router library, no ORM, no template engine. Routing uses
`net/http.ServeMux` method-and-wildcard patterns (Go 1.22+), templating uses
`html/template`, logging uses `log/slog`, tests use `testing`.

---

## D8: Modular apps extension point (Principle II)

**Decision**: one narrow interface in the platform core, satisfied by each app:

```go
type App interface {
    Meta() AppMeta      // slug, nav label, route prefix, RequiresGuild
    Routes(r Registrar) // registers handlers under its own prefix
}
```

Apps are registered in a single explicit slice at startup in `main.go`. The core owns
authentication, session lookup, guild gating, shared layout and the database handle,
and injects them as `platform.Deps`. An app never imports another app.

**Rationale**: satisfies Principle II's "single, documented extension point" with a
two-method interface and an explicit registration list — no reflection, no plugin
discovery, no init-time side effects. Principle VII forbids generalising further
until a second app exists; the dashboard is the first, so the interface stays minimal
and honest.

**Alternatives considered**: `init()`-based self-registration into a global registry.
Rejected — import-order dependent, invisible in code review, and untestable without
global state.

---

## D9: Living within Blizzard's rate limits

**Finding**: Blizzard's documented per-client limits are **36,000 requests/hour and
100 requests/second**; exceeding either returns HTTP 429.

**Decision**: bound the per-view fan-out and degrade gracefully.

- Fetch character profiles concurrently with `errgroup.SetLimit(8)` — enough to keep
  dashboard latency near a single round-trip, far under 100/s.
- Apply a context deadline to the whole fan-out, so one slow character response
  cannot hang the page.
- Treat 429 and 5xx from Blizzard as the retry-able error state FR-010 and FR-016
  require, honouring `Retry-After` when present.
- One character's failure must not fail the whole dashboard: selection proceeds over
  the characters that did return, and the page states that the data is partial.

**Headroom**: at 36,000/hour, a heavy user with ~30 alts costing ~31 calls per view
still leaves room for well over a thousand dashboard views per hour — ample for a
single guild, which is why FR-016's no-cache choice is viable on quota grounds even
though it is expensive per view.

**Risk accepted (flagged; not a constitution violation)**: dashboard latency scales
with alt count, and FR-016 forbids caching the result. Bounded concurrency holds this
to roughly one round-trip plus overhead, but a member with many alts will notice on a
slow day. Always-live data is the explicit product decision recorded in the spec's
Clarifications. If latency proves unacceptable in practice, FR-016 is the lever to
revisit, and nothing in this design blocks adding a cache later.

**Alternatives considered**: `If-Modified-Since` conditional requests, which the
forums recommend for exactly this fan-out. Rejected *for now* — it requires retaining
prior response metadata per character, which is a cache, which FR-016 forbids.

---

## D10: Hosting and IaC (Principles IV and V)

**Decision**: Cloud Run service plus Cloud SQL for PostgreSQL, images from Artifact
Registry, the Blizzard client secret in Secret Manager, all provisioned by OpenTofu
under `tofu/`. Configuration strictly by environment variable; the same image is
promoted local → production.

**Local toolchain**: OpenTofu **v1.12.6 is installed in WSL** (Ubuntu, at
`~/.local/bin/tofu`) — current as of the latest upstream release. It is not on the
Windows PATH, and that is the intended arrangement: infrastructure commands run from
the WSL distro, which reaches this repository at
`/mnt/c/Users/antho/development/WOW/Guilds/tomb`. Verified working there with
`tofu init` and `tofu validate`. `docker` 29.7.2 and `go` 1.27.1 are available on
Windows.

**Rationale**: Cloud Run is the smallest GCP compute surface that runs a container
with no orchestration to manage, it matches "the same images built for local
development are promoted to production", and it is fully expressible in OpenTofu.

**Alternatives considered**: GKE — far more infrastructure than one Go service needs.
App Engine — less container-native, weaker local parity.

---

## D11: Testing and observability (Principle VI)

**Decision**:

- Table-driven `testing` tests for the four pieces of business logic the constitution
  names: character selection including the FR-006 tiebreaker, guild membership
  matching, session issue/expiry, and app registration.
- The Blizzard client sits behind a narrow interface so handlers are tested against a
  fake; the HTTP-level client is tested against `net/http/httptest` servers replaying
  captured JSON fixtures. No live Blizzard calls in tests.
- `log/slog` JSON handler with structured key/value output. The access token and
  session token are never logged.
- `GET /healthz` for liveness (process up) and `GET /readyz` for readiness (database
  reachable), for Cloud Run and uptime checks.

**Tiebreaker test note**: FR-006's ordering (timestamp → level → item level → name)
is a *total* order, which is exactly what makes it expressible as a fixture table
with one deterministic expected winner per row — the reason the spec pinned it.

---

## Sources

- [Battle.net OIDC discovery document](https://oauth.battle.net/.well-known/openid-configuration) — endpoints, grant types, response types (fetched 2026-09-13)
- [Using OAuth | Battle.net Developer Documentation](https://community.developer.battle.net/documentation/guides/using-oauth)
- [WoW Last Login Timestamp and API Performance — Blizzard Forums](https://us.forums.blizzard.com/en/blizzard/t/wow-last-login-timestamp-and-api-performance/9028) — `last_login_timestamp` is per-character only; per-character fan-out confirmed
- [next-auth #6853 — Battle.net refresh token and expiration](https://github.com/nextauthjs/next-auth/issues/6853) — no refresh token issued; `expires_in` ~86399
- [Applications, Rate Limits & Throttling — Blizzard](https://www.bluetracker.gg/wow/topic/us-en/8796351117-applications-rate-limits-throttling/) and [API Access - Clients - Rate Limits](https://us.forums.blizzard.com/en/blizzard/t/api-access-clients-rate-limits/5602) — 36,000/hour, 100/second
- [WoW Character API specification](https://www.drupal.org/node/1698752) — character summary fields including `guild`, `last_login_timestamp`, average item level
- [Go downloads](https://go.dev/dl/) — Go 1.27.1 current stable
