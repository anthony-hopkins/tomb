# Phase 1 Data Model: AI combat-log comparison

**Feature**: `003-ai-log-comparison`
**Date**: 2026-09-16
**Source**: spec.md Key Entities, FR-028..FR-041; research.md D2, D5, D6, D8, D9, D11
**Migration**: `internal/platform/migrations/0005_combatlogs.sql`

## Overview

```
users (001)
  1 ──── n  uploads ──── n  fights ──── n  fight_summaries
                                              │ (one per member character in the fight)
                                              └──── n  analyses ──── 1  comparison_players
                                                                          (cached, shared)
talent_names, item_names            (ID → name caches, shared, no owner)
audit_log (002)                     (one entry per upload, removal, analysis)
```

Raw log bytes are **not** in the model: they exist as a file under `TOMB_UPLOAD_DIR`
only while an upload is `receiving`, `queued` or `parsing`, and are deleted by the
parser (FR-030). Nothing about any player other than the uploading member's own
characters is ever written (FR-029, SC-008): the parser discards other players'
events as it reads them.

---

## `uploads`

One row per combat log a member sent. State, not bytes.

| Field | Type | Constraints | Notes |
|---|---|---|---|
| `id` | `bigserial` | PK | |
| `user_id` | `bigint` | NOT NULL, FK `users(id)` ON DELETE CASCADE | The uploading member |
| `filename` | `text` | NOT NULL | As chosen in the browser; display only |
| `raw_size` | `bigint` | NOT NULL | Uncompressed bytes, as the browser reported |
| `fingerprint` | `bytea` | NOT NULL | SHA-256 over first 1 MiB + last 1 MiB + size (D4) |
| `characters` | `jsonb` | NOT NULL | `[{"name":"Nekromoo","realm_slug":"area-52"}, …]` — the member's account characters as the site knew them when the upload began, so the parser (which runs with no session) matches against the same list the card shows (D3) |
| `pieces_total` | `int` | NOT NULL | Ceil(raw_size / 8 MiB) |
| `pieces_received` | `int` | NOT NULL DEFAULT 0 | Next expected piece index; the resume point |
| `stored_size` | `bigint` | NOT NULL DEFAULT 0 | Compressed bytes on disk so far; capped at the limit |
| `state` | `text` | NOT NULL, CHECK IN (`receiving`,`queued`,`parsing`,`parsed`,`failed`,`removed`) | See lifecycle |
| `failure` | `text` | NOT NULL DEFAULT '' | Plain-language reason when `failed` |
| `advanced_logging` | `boolean` | nullable | From the version header; NULL until parsed |
| `log_version` | `int` | nullable | `COMBAT_LOG_VERSION`; NULL until parsed |
| `created_at` | `timestamptz` | NOT NULL DEFAULT now() | |
| `updated_at` | `timestamptz` | NOT NULL DEFAULT now() | Touched on every state change and every piece; the 24-hour stale sweep keys on it |
| `parsed_at` | `timestamptz` | nullable | When parsing ended, either way |

**Indexes**: `(user_id, created_at DESC)` for the member's list;
`UNIQUE (user_id, fingerprint) WHERE state <> 'removed'` so a repeat is recognised
(FR-028) and a removed upload may be uploaded again; `(state, created_at)` for the
parser's queue scan.

**Lifecycle**:

```
receiving ──(all pieces)──▶ queued ──(worker)──▶ parsing ──▶ parsed
    │                                              │            │
    │ (24 h idle: sweep)                           └──▶ failed  │
    ▼                                                           ▼
  failed                                     removed ◀──(member)──┘
```

- `receiving → queued` when the client posts `finish` and `pieces_received ==
  pieces_total`.
- Only the parser moves `queued → parsing → parsed | failed`; the file is deleted on
  leaving `parsing`, whichever way.
- `removed` is set by the member (FR-031); the row stays for the audit trail's
  subject, and its fights, summaries and analyses are deleted (cascade).
- On startup any `parsing` row becomes `failed` ("the site restarted while parsing;
  upload again").
- 90-day retention (FR-031): a daily sweep sets `removed` on rows whose `created_at`
  is older than 90 days, with the cascade.

---

## `fights`

One boss encounter found in an upload.

| Field | Type | Constraints | Notes |
|---|---|---|---|
| `id` | `bigserial` | PK | |
| `upload_id` | `bigint` | NOT NULL, FK `uploads(id)` ON DELETE CASCADE | |
| `encounter_id` | `int` | NOT NULL | The game's journal encounter ID; Warcraft Logs uses the same |
| `encounter_name` | `text` | NOT NULL | As logged |
| `difficulty_id` | `int` | NOT NULL | The game's: 17 LFR, 14 Normal, 15 Heroic, 16 Mythic |
| `group_size` | `int` | NOT NULL | |
| `kill` | `boolean` | NOT NULL | `ENCOUNTER_END` success |
| `started_at` | `timestamptz` | NOT NULL | From the log line's timestamp |
| `duration_ms` | `int` | NOT NULL | From `ENCOUNTER_END`, or start→end when absent |
| `ordinal` | `int` | NOT NULL | Pull number within the upload, 1-based |

**Indexes**: `(upload_id, ordinal)`.

An encounter with a start and no end (the log stopped mid-pull) is recorded as a
wipe with the duration to the last line seen, so the night reads as it was.

---

## `fight_summaries`

One of the uploading member's characters in one fight (D2).

| Field | Type | Constraints | Notes |
|---|---|---|---|
| `id` | `bigserial` | PK | |
| `fight_id` | `bigint` | NOT NULL, FK `fights(id)` ON DELETE CASCADE | |
| `character_name` | `text` | NOT NULL | As on the member's account |
| `realm_slug` | `text` | NOT NULL | Blizzard slug |
| `spec_id` | `int` | nullable | From `COMBATANT_INFO`; NULL without advanced logging |
| `damage` | `bigint` | NOT NULL | Effective damage done, including pets attributed |
| `healing` | `bigint` | NOT NULL | Effective healing done (amount − overheal) |
| `deaths` | `int` | NOT NULL | |
| `active_ms` | `int` | NOT NULL | First to last event by this character in the fight |
| `casts` | `jsonb` | NOT NULL | `[{"id":123,"name":"Death Strike","count":41,"at":[3.2, 9.8, …]}]` — `at` present only when count < 10 (D2) |
| `talents` | `jsonb` | nullable | `[{"node":1,"entry":2,"rank":1}, …]` from `COMBATANT_INFO` |
| `gear` | `jsonb` | nullable | `[{"slot":0,"item":212345,"ilvl":311,"enchant":7,"bonus":[…],"gems":[…]}, …]` |

**Indexes**: `UNIQUE (fight_id, character_name, realm_slug)`;
`(character_name, realm_slug, fight_id)` for the card's fight picker and "latest
pull with talents" (D8).

**Validation**: `character_name`/`realm_slug` must be one of the uploader's account
characters at parse time (D3); anything else is never written.

---

## `comparison_players`

A player named by a Warcraft Logs link, and what was fetched about them for one
boss and difficulty. Shared across members; a day's cache (FR-035, D9).

| Field | Type | Constraints | Notes |
|---|---|---|---|
| `id` | `bigserial` | PK | |
| `region` | `text` | NOT NULL | `us`, `eu`, … lowercased |
| `realm_slug` | `text` | NOT NULL | Warcraft Logs server slug |
| `name` | `text` | NOT NULL | Lowercased for the key; display case in `payload` |
| `encounter_id` | `int` | NOT NULL | |
| `wcl_difficulty` | `int` | NOT NULL | Warcraft Logs' numbering (3/4/5/1) |
| `metric` | `text` | NOT NULL | `dps` or `hps` |
| `fetched_at` | `timestamptz` | NOT NULL | Cache age |
| `class_id` | `int` | NOT NULL | From `character.classID` |
| `spec` | `text` | NOT NULL | Best rank's spec name |
| `rank_percent` | `numeric(5,2)` | NOT NULL | |
| `amount` | `numeric` | NOT NULL | DPS/HPS of the best rank |
| `payload` | `jsonb` | NOT NULL | The best rank as fetched: gear[], talents[], report code, fight id, duration |

**Indexes**: `UNIQUE (region, realm_slug, name, encounter_id, wcl_difficulty, metric)`.
A fetch newer than 24 hours is reused; older is refetched and the row replaced.

**Validation**: a player with no ranks for the encounter and difficulty is **not**
stored; the analysis is refused before it is created (FR-035, scenario 3).

---

## `analyses`

One run (FR-038..FR-041).

| Field | Type | Constraints | Notes |
|---|---|---|---|
| `id` | `bigserial` | PK | |
| `user_id` | `bigint` | NOT NULL, FK `users(id)` ON DELETE CASCADE | Who ran it; the allowance keys on this |
| `summary_id` | `bigint` | NOT NULL, FK `fight_summaries(id)` ON DELETE CASCADE | The member's character in the fight |
| `character_name` | `text` | NOT NULL | Denormalised so the card finds "latest for this character" without joins |
| `realm_slug` | `text` | NOT NULL | |
| `comparison_id` | `bigint` | NOT NULL, FK `comparison_players(id)` ON DELETE RESTRICT | |
| `state` | `text` | NOT NULL, CHECK IN (`pending`,`done`,`failed`) | |
| `failure` | `text` | NOT NULL DEFAULT '' | |
| `table_json` | `jsonb` | nullable | The computed upgrade table (D11) |
| `talent_diff` | `jsonb` | nullable | `{"theirs_only":[…],"yours_only":[…]}` |
| `writeup` | `text` | nullable | The model's text, as returned |
| `model` | `text` | NOT NULL DEFAULT '' | Model ID used |
| `prompt_tokens` | `int` | nullable | From `usageMetadata`, for the log and cost tracking |
| `output_tokens` | `int` | nullable | |
| `created_at` | `timestamptz` | NOT NULL DEFAULT now() | |
| `started_at` | `timestamptz` | nullable | Set when a worker claims the row; a `pending` row with a start is one a restart interrupted |
| `finished_at` | `timestamptz` | nullable | |

**Indexes**: `(character_name, realm_slug, created_at DESC)` for the card;
`(user_id, created_at DESC) WHERE state <> 'failed'` for the allowance.

**Allowance rule** (FR-039): a member may create an analysis when no row of theirs
with `state <> 'failed'` has `created_at` within the last 120 minutes. A `pending`
row counts (one at a time); a `failed` one never does. Officers and the administrator
skip the check. The check and the insert happen in one transaction.

**Retention**: analyses cascade with their summary; a daily sweep deletes rows older
than 90 days. The card shows the newest `done` row for the character; a `pending`
one on top makes the card refresh (D7); a `failed` newest row shows its reason above
the previous `done` one (FR-041).

---

## `talent_names` and `item_names`

ID → name caches for what the log and Warcraft Logs return as numbers (D8, D11).

| Field | Type | Notes |
|---|---|---|
| `id` | `int` PK | Talent entry ID / item ID |
| `name` | `text` NOT NULL | From Blizzard Game Data |
| `extra` | `jsonb` | Talents: spell ID, tree (class/spec/hero); items: quality, slot type |
| `fetched_at` | `timestamptz` | Static-namespace data changes only with a patch; refetch after 30 days |

Unknown IDs are fetched on first use during an analysis and then cached; an ID
Blizzard does not know keeps its number as its name so a table row is never blank.

---

## Audit entries (existing `audit_log`, 002)

| Action | Subject | Detail |
|---|---|---|
| `combatlogs.upload` | filename | size, fights found, characters matched, or the failure |
| `combatlogs.remove` | filename | fights removed |
| `combatlogs.analyse` | character name | fight (boss, difficulty, date), comparison player, `done` or the failure |

Recorded by the app, never by the workers' callers, so one run is one entry.

---

## In-memory value types (never persisted)

- `combatlog.Fight` and `combatlog.Summary`: the parser's output for one encounter,
  converted to rows by the parser worker.
- `fights.UpgradeRow` and `fights.TalentDiff`: the computed comparison, stored as
  `table_json` / `talent_diff` once made.
- `wcl.Ranking`: the decoded best rank, stored as `comparison_players.payload`.
- `blizzard.Loadout`: the active talent loadout from Blizzard, shown on the card and
  never stored.
