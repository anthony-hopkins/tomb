# Contract: Blizzard API Consumption

**Feature**: `001-battlenet-sso-character-dashboard`
**Direction**: outbound — this is a dependency contract, not a surface we expose
**Source**: research.md D1, D3, D4, D9

This pins exactly which Blizzard endpoints are called, what is read from each, and
how every failure maps to user-visible behaviour. It is the boundary the fake in tests
must reproduce.

## Internal interface

The whole dependency sits behind one narrow interface, so handlers are tested without
network access (Principle VI, research D11):

```go
type BlizzardClient interface {
    // UserInfo resolves the account identity behind an access token.
    UserInfo(ctx context.Context, token string) (Identity, error)

    // AccountCharacters lists the account's characters in the configured region.
    AccountCharacters(ctx context.Context, token string) ([]CharacterRef, error)

    // CharacterProfile fetches one character's full summary.
    CharacterProfile(ctx context.Context, token string, ref CharacterRef) (Character, error)
}
```

## Endpoints

### 1. Identity — `GET https://oauth.battle.net/userinfo`

Auth: `Authorization: Bearer <user access token>`

| Read | Used for |
|---|---|
| `sub` | `users.bnet_sub`, the stable identity key |
| `battletag` | `users.battletag`, display only |

### 2. Account characters — `GET /profile/user/wow`

Host: `https://{region}.api.blizzard.com`
Query: `namespace=profile-{region}`, `locale`
Auth: Bearer, requires the `wow.profile` scope

Returns the account's WoW characters grouped by account. Read per character: `name`,
`realm.slug`, `realm.name`, `id`, `level`, `playable_class`.

**Does not return `last_login_timestamp`** — hence endpoint 3 (research D3).

### 3. Character profile — `GET /profile/wow/character/{realmSlug}/{characterName}`

Host: `https://{region}.api.blizzard.com`
Query: `namespace=profile-{region}`, `locale`
Auth: Bearer
Called: **once per character**, per dashboard view (FR-016)
`characterName` MUST be lowercased and URL-escaped.

| Read | Used for |
|---|---|
| `last_login_timestamp` | FR-006 primary selection key (epoch **milliseconds**) |
| `level` | FR-007 display; FR-006 tiebreaker 2 |
| `average_item_level` | FR-007 display; FR-006 tiebreaker 3 |
| `name`, `realm.name` | FR-007 display |
| `character_class.name` | FR-007 display |
| `active_spec.name` | Optional display; may be absent |
| `guild.name`, `guild.realm.slug` | FR-013 membership check (research D4) |

**`guild` is omitted entirely for an unguilded character.** Absent `guild` and "not in
TOMB" are the same outcome: not a member.

### 4. Mythic+ rating — `GET /profile/wow/character/{realmSlug}/{characterName}/mythic-keystone-profile`

Host, query and auth as endpoint 3.
Called: once per character shown on a card (dashboard: per view; guild: per
snapshot refresh).

| Read | Used for |
|---|---|
| `current_mythic_rating.rating` | FR-007 display, rounded to a whole number |

**404 means "has never run a key", not failure.** It is read as unrated and the
row is omitted.

### 5. Raid progression — `GET /profile/wow/character/{realmSlug}/{characterName}/encounters/raids`

Host, query and auth as endpoint 3. Called as endpoint 4 is.

| Read | Used for |
|---|---|
| `expansions[].expansion.id` | Picks the current expansion: the highest id |
| `expansions[].instances[].instance.name` | FR-007 display: the raid's name |
| `expansions[].instances[].modes[].difficulty.type` | `LFR`, `NORMAL`, `HEROIC`, `MYTHIC`; ordered easiest first |
| `...modes[].progress.completed_count`, `total_count` | "3/8" |

Difficulties with no kills, and raids with none at any difficulty, are omitted.
**404 means "has never raided"** and is read as no progress.

### Guild roster — `GET /data/wow/guild/{realmSlug}/{nameSlug}/roster`

Used by the guild overview (FR-018), not by the membership check: the check
still reads `guild` off each character's profile (research D4). See the
`GuildRoster` method.

## Rate limits and concurrency

Documented limits: **36,000 requests/hour, 100 requests/second** per client.

| Rule | Value |
|---|---|
| Max concurrent character fetches | 8 (`errgroup.SetLimit`) |
| Deadline for the whole fan-out | Context deadline on the request |
| Cost per dashboard view | `1 + N` calls for `N` characters |

## Failure mapping

| Blizzard response | Internal handling | User sees |
|---|---|---|
| `401` on any call | Delete the session row; treat as revoked | Re-login prompt (FR-012) |
| `403` | Log; treat as unavailable | Retry-able error (FR-010) |
| `404` on a character | Skip that character; continue selection | Card for the best remaining character, plus a partial-data notice |
| `429` | Honour `Retry-After`; do not auto-retry inside the request | Retry-able error (FR-010) |
| `5xx` | No blind retry within the request | Retry-able error (FR-010) |
| Timeout / transport error | Cancel siblings via context | Retry-able error (FR-010) |
| Malformed JSON | Log with the endpoint, never the token | Retry-able error (FR-010) |

**Partial success is a success**: if the account list succeeds and at least one
character profile succeeds, the dashboard renders. Only a failure of endpoint 2, or of
*every* character fetch, becomes a full error page (research D9).

## Secrets discipline

- The access token appears only in the `Authorization` header. Never in a query
  string, log line, error message, template, or metric label.
- Error logs record endpoint, status, and duration — never request or response bodies,
  which carry account data.
- The client secret is used only at the token endpoint, from Secret Manager in GCP.

## Test obligations

- Fixtures: captured (account-anonymised) JSON for endpoints 1–3, replayed via
  `net/http/httptest`, including an unguilded character and a character missing
  `active_spec`.
- One table-driven case per row of the failure-mapping table above.
- A concurrency test asserting in-flight character fetches never exceed 8.
- No test performs a live Blizzard call.
