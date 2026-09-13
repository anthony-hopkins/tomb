# Phase 1 Data Model: Battle.net SSO Login & Character Dashboard

**Feature**: `001-battlenet-sso-character-dashboard`
**Date**: 2026-09-13
**Source**: spec.md Key Entities + FR-004/006/013/013a/014/015/016; research.md D5/D6

## Overview

FR-016 (no caching of character data) collapses this model to **two persisted
tables** plus **two in-memory value types**. Character data is fetched from Blizzard
per request and deliberately never written to storage.

```
users (persisted)
  1 ──── n  sessions (persisted)

Character        (in-memory only, per request — never persisted)
GuildMembership  (in-memory only, derived per request — never persisted)
```

---

## Persisted entities

### `users`

One row per Battle.net account that has ever completed login. Created on first
successful authentication, then updated on each subsequent login.

| Field | Type | Constraints | Notes |
|---|---|---|---|
| `id` | `bigserial` | PK | Internal identifier; never exposed to Blizzard |
| `bnet_sub` | `text` | NOT NULL, UNIQUE | Blizzard account subject claim from `/userinfo`. The stable identity key. |
| `battletag` | `text` | NOT NULL | Display name (e.g. `Player#1234`). Mutable by the user, so never used as a key. |
| `first_seen_at` | `timestamptz` | NOT NULL, default `now()` | Spec: "first login to the site" |
| `last_seen_at` | `timestamptz` | NOT NULL | Spec: "last login to the site"; updated on each successful auth |

**Validation rules**
- `bnet_sub` is the sole identity key. A changed `battletag` MUST update the existing
  row, never create a second one.
- No password column exists anywhere in this schema, by construction (FR-004,
  Principle III).

**Deliberately absent**: `is_guild_member`. Guild membership is re-derived from
Blizzard on every request (FR-013, FR-016) and is never cached on the user row — a
stored flag would let a departed member keep access until something invalidated it.

---

### `sessions`

One row per authenticated browser session. Holds the Blizzard access token for the
life of the session, because FR-016 needs it on every dashboard view.

| Field | Type | Constraints | Notes |
|---|---|---|---|
| `token_hash` | `bytea` | PK | SHA-256 of the opaque session token. The raw token exists only in the user's cookie (research D6). |
| `user_id` | `bigint` | NOT NULL, FK → `users.id`, `ON DELETE CASCADE` | |
| `bnet_access_token` | `text` | NOT NULL | Server-side only. Never rendered, logged, or sent to the browser. |
| `created_at` | `timestamptz` | NOT NULL, default `now()` | Spec: "issued time" |
| `expires_at` | `timestamptz` | NOT NULL | Absolute expiry, computed as `created_at + token.expires_in` (FR-015) |

**Indexes**: PK on `token_hash`; index on `expires_at` for expiry sweeps; index on
`user_id` for "log out everywhere" and cascade performance.

**Validation rules**
- `expires_at` MUST be derived from the token response's `expires_in`, not a
  hardcoded constant (FR-015, research D2).
- `expires_at` MUST be re-checked server-side on **every** request. A row at or past
  `expires_at` is treated as absent (FR-015, acceptance scenario 8).
- No activity-based extension: `expires_at` is written once at creation and never
  moved forward (FR-015).

**Lifecycle**

```
   (no session)
        │  successful OAuth callback + token exchange
        ▼
     ACTIVE  ──── now() >= expires_at ────────────►  EXPIRED  ─┐
        │                                                      │
        ├──── user clicks "Log out" (FR-008) ──────► DELETED ──┤
        │                                                      │
        └──── Blizzard returns 401 (revoked, FR-012) ► DELETED ─┤
                                                               ▼
                                                    row removed; user
                                                    treated as anonymous
                                                    and prompted to re-login
```

Expired rows are deleted lazily on encounter and swept periodically. An expired
session and a deleted session are indistinguishable to the user by design.

---

## In-memory value types (never persisted)

### `Character`

Assembled per dashboard view from Blizzard's account profile summary plus that
character's profile summary (research D3). Discarded when the response is written.

| Field | Source | Notes |
|---|---|---|
| `Name` | character profile summary | |
| `RealmSlug`, `RealmName` | character profile summary | |
| `Region` | configuration | Single configured region (FR-014); not per-character data |
| `Class` | `character_class.name` | |
| `ActiveSpec` | `active_spec.name` | Optional — may be absent |
| `Level` | `level` | Tiebreaker key 2 (FR-006) |
| `AverageItemLevel` | `average_item_level` | Displayed (FR-007); tiebreaker key 3 |
| `LastLoginTimestamp` | `last_login_timestamp` | Primary selection key (FR-006). Blizzard returns epoch milliseconds. |
| `Guild` | `guild` object | **Absent when the character is in no guild** — absent and "no guild" are the same case (research D4) |
| `IsCurrent` | derived | Set on exactly one character by the selection rule below |

**Selection rule (FR-006)** — a total order, applied over successfully fetched
characters only:

1. `LastLoginTimestamp` descending
2. `Level` descending
3. `AverageItemLevel` descending
4. `Name` ascending, case-insensitive

Keys 2–4 apply only on an exact tie of the preceding key. Because key 4 is unique per
realm and the candidate set is one account's characters, the order is total and the
winner is deterministic for identical input — which is what makes this table-testable
(FR-006, research D11).

**Edge cases**
- Zero characters returned → no selection; the user cannot be guild-verified and
  receives the non-member outcome (spec Edge Cases, FR-013a).
- Partial fetch failure → selection runs over what succeeded; the view reports that
  data is partial (research D9).

---

### `GuildMembership`

Derived per request, never stored.

| Field | Type | Notes |
|---|---|---|
| `IsMember` | `bool` | True if **any** fetched character's `guild` matches the configured guild |
| `MatchedCharacter` | `*Character` | Which character established membership; for diagnostics and logging |

**Derivation (FR-013)**: compare each character's `guild.name`/`guild.realm` against
the configured TOMB guild name and realm, matching on Blizzard's slug rather than raw
display text (research D4). Absent `guild` → not a match.

**Gate (FR-013a)**: `IsMember == false` denies access to every app. It is evaluated in
the platform core, not in the dashboard app, so future apps inherit it without
touching this feature's code (Principle II, FR-011).

---

## Configuration (not persisted; environment variables)

| Variable | Purpose | Referenced by |
|---|---|---|
| `BNET_CLIENT_ID` | OAuth client id | FR-002 |
| `BNET_CLIENT_SECRET` | OAuth client secret (Secret Manager in GCP) | FR-002 |
| `BNET_REDIRECT_URL` | Registered callback URL | FR-003 |
| `BNET_REGION` | The single configured region, e.g. `us` | FR-014 |
| `TOMB_GUILD_NAME` | Configured guild name for verification | FR-013 |
| `TOMB_GUILD_REALM` | Configured guild realm slug | FR-013 |
| `DATABASE_URL` | Postgres connection string | research D5 |
| `SESSION_COOKIE_SECURE` | Allows `false` for local HTTP development only | research D6 |

Per Principle IV, environment-specific behaviour comes only from these values — never
from a differently built image.

---

## Schema DDL sketch

Migration mechanics (tooling, ordering, rollback) belong to `tasks.md`; this is the
target shape only.

```sql
CREATE TABLE users (
    id            bigserial   PRIMARY KEY,
    bnet_sub      text        NOT NULL UNIQUE,
    battletag     text        NOT NULL,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash        bytea       PRIMARY KEY,
    user_id           bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bnet_access_token text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    expires_at        timestamptz NOT NULL
);

CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
CREATE INDEX sessions_user_id_idx    ON sessions (user_id);
```
