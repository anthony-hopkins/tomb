# Contract: HTTP Routes

**Feature**: `001-battlenet-sso-character-dashboard`
**Surface**: server-rendered HTML over HTTP (no JSON API in this feature)
**Registered by**: platform core, except `/app/dashboard` which the dashboard app
registers through the `App` extension point (see `app-registration.md`)

A server-rendered site's contract is its routes: path, method, who may reach it, and
what each outcome renders. All responses are `text/html; charset=utf-8` unless noted.

## Access levels

| Level | Meaning |
|---|---|
| `public` | No session required |
| `session` | Valid unexpired session required; no guild check |
| `member` | Valid session **and** verified TOMB guild membership (FR-013a) |

---

## Platform core routes

### `GET /`

Public landing page.

| Condition | Status | Result |
|---|---|---|
| No session | 200 | Landing page with a "Sign in with Battle.net" action (FR-001). Since spec 004 the page is the welcome app's (`/app/welcome`, `public`), served here by the core with its notice (`?signed_out=1`, `?reauth=1`) handed over; the core's own plain page is the fallback when no app claims `Landing` |
| Valid session | 302 | → the Home app (the guild overview) |

---

### `POST /auth/login`

Begins the OAuth2 authorization-code flow (FR-002).

- `POST`, not `GET`, so a prefetch or crawler cannot start a login.
- Generates a random `state`, stores it in a short-lived pre-login cookie, and
  redirects to `https://oauth.battle.net/authorize` with `scope=wow.profile`.

| Condition | Status | Result |
|---|---|---|
| Always | 302 | → Blizzard authorize URL (acceptance scenario 1) |

---

### `GET /auth/callback`

Blizzard's redirect target (FR-003). Query: `code`, `state`, or `error`.

| Condition | Status | Result |
|---|---|---|
| Valid `code` + matching `state`, member | 302 | Session created → `/app/dashboard` (scenario 2) |
| Valid `code` + matching `state`, non-member | 200 | Renders the "this site is for TOMB members" page with a log-out action. **No app access** (FR-013a, scenario 7) |
| `error=access_denied` (user declined) | 200 | Friendly "login not completed" page with a retry action. **No session created** (FR-009, scenario 5) |
| `state` missing or mismatched | 400 | Generic login-failed page. No session. Logged as a possible CSRF attempt. |
| Token exchange fails | 502 | Retry-able error page (FR-010) |
| Blizzard profile fetch fails | 502 | Retry-able error page (FR-010) |

**Note on the non-member case**: a session row *is* created before the guild check
(the access token is needed to perform the check). A non-member's session is deleted
before the response is written, so no usable session survives the denial.

---

### `POST /auth/logout`

Terminates the session (FR-008).

- `POST` with a CSRF token, so a third-party page cannot force a logout.

| Condition | Status | Result |
|---|---|---|
| Valid session | 302 | Session row deleted, cookie cleared → `/` (scenario 4) |
| No session | 302 | → `/` (idempotent) |

---

### `GET /healthz`

Liveness. Access: `public`. Returns `200` with `text/plain` body `ok` whenever the
process is serving. Performs no database work, so a database outage does not cause
Cloud Run to kill healthy containers (Principle VI).

### `GET /readyz`

Readiness. Access: `public`. `200` when the database responds to a ping within the
timeout; `503` with a plain-text reason otherwise (Principle VI).

---

## Dashboard app routes

### `GET /app/dashboard`

The feature's payload. Access: `member`.

Per FR-016 this **always** fetches live: account profile summary, then one character
profile summary per character, bounded per research D9. No cached result is ever
served.

| Condition | Status | Result |
|---|---|---|
| Member, ≥1 character fetched | 200 | Card for the selected character: name, realm, class, level, average item level (FR-007) |
| Member, partial fetch failure | 200 | Card for the best character among those fetched, plus a visible "some characters could not be loaded" notice (research D9) |
| Zero characters on the account | — | Unreachable: guild verification cannot succeed, so this user is handled as a non-member (spec Edge Cases) |
| Session expired | 302 | → `/` with a re-login prompt; no stale data rendered (FR-015, scenario 8) |
| Blizzard returns 401 (token revoked) | 302 | Session deleted → `/` with a re-login prompt (FR-012) |
| Blizzard returns 429 | 200 | Retry-able error state with a "try again" action, honouring `Retry-After` (FR-010, FR-016) |
| Blizzard 5xx or timeout | 200 | Retry-able error state (FR-010) |
| Session valid but no longer a member | 200 | Non-member page; the check is re-derived per request, so a departed member loses access on their next view (data-model: no cached `is_guild_member`) |

Error states render at `200` with an in-page error rather than an HTTP error status,
because the shell is a successfully rendered page for an authenticated user; only
`/auth/callback` failures, which have no page shell yet, use 4xx/5xx.

---

## Assistant app routes (spec 006)

Access: `member`. No nav entry; the launcher is the Companion panel the core
draws after every member page's body, and the page is linked from it.

| Route | Result |
|---|---|
| `GET /app/assistant` | The member's conversation as a page, with the question form; `?e=<code>` (`empty`, `long`, `allowance`, `unavailable`, `busy`, `failed`) words a refusal, `&at=<unix>` when the allowance reopens |
| `POST /app/assistant/ask` | CSRF-checked. `q` (at most 600 characters) is answered by the model with the member's characters as facts. `Accept: application/json` answers `{question, answer_html, sources, character}` or `{error}` (400 empty/long, 429 over the allowance, 503 busy or unavailable, 502 failed); otherwise a 303 to the page, with the reason as `?e=` on a refusal |
| `POST /app/assistant/new` | CSRF-checked. Starts a new conversation. JSON `{ok}` or a 303 to the page |

## War Room app routes (spec 007)

Access: `officer` (the administrator counts as one). Nav label "War Room".

| Route | Result |
|---|---|
| `GET /app/war-room` | Recent raid nights found through the raiders' Warcraft Logs pages, a field for a report link or code, and the reviews done or running; `?e=` words a refusal (`code`, `unavailable`, `failed`) |
| `POST /app/war-room/reviews` | CSRF-checked. `code` (a link or code) starts a review, audited as `warroom.review`; a review of the same report younger than six hours is shown instead unless `again=1`; 303 to the review |
| `GET /app/war-room/reviews/{id}` | The review: while running, the page refreshes itself every 5 s; failed, the reason and "Run again"; done, the overview, each boss's sections, "do these first" and "what to verify" |

## Cross-cutting contracts

**Session cookie** (research D6): name `tomb_session`; `HttpOnly`; `Secure` (unless
`SESSION_COOKIE_SECURE=false` locally); `SameSite=Lax`; `Path=/`; `Max-Age` from the
token's `expires_in`.

**Guild gate** (FR-013a): enforced by core middleware keyed off `AppMeta.RequiresGuild`,
so a future app opts in by declaring one field and inherits identical behaviour
without touching auth code (FR-011, Principle II).

**Never in a response**: the Blizzard access token, the raw session token, and the
client secret — not in HTML, headers, URLs, or logs (Principle III, research D6).
