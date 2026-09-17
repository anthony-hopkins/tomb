# Contract: HTTP Routes — Combat logs app and card additions

**Feature**: `003-ai-log-comparison`
**Surface**: server-rendered HTML, plus three small JSON routes used only by the
uploader script
**Registered by**: the `combatlogs` app through the `App` extension point; the card
additions by the existing `dashboard` app

All routes are `member` level (valid session and verified guild membership); the
core enforces it before any handler runs. Every state-changing request carries the
site's CSRF token: as the usual hidden form field, or — for the uploader's
`fetch` calls — as the `X-CSRF-Token` header, which the core's verifier accepts
as equivalent (a small addition to `platform/csrf.go`).

---

## Combat logs app — `/app/combatlogs`

Meta: `Slug: combatlogs`, `NavLabel: "Combat logs"`, `NavOrder: 35` (after Calendar
at 30, before Logs), `RequiresGuild: true`.

### `GET /app/combatlogs`

The member's uploads, newest first, each with its state and its fights; the upload
form; the privacy line (FR-032) and the advanced-logging note.

| Condition | Status | Result |
|---|---|---|
| Any member | 200 | Page. Uploads in `receiving` show "resume" (the script picks the file again and continues); `queued`/`parsing` rows make the response carry a `Refresh: 5` header, which every browser honours like a meta refresh (D7) |

### `POST /app/combatlogs/uploads` — begin *(JSON)*

Body: `{"filename":"WoWCombatLog.txt","size":2147483648,"fingerprint":"<hex sha-256>"}`.

| Condition | Status | Result |
|---|---|---|
| New file | 201 | `{"id":17,"pieces_total":256,"pieces_received":0,"piece_bytes":8388608}` |
| Same fingerprint, this member, an upload not `removed` | 200 | `{"id":12,"state":"parsed","url":"/app/combatlogs/uploads/12"}` — the client sends nothing and goes to the page (FR-028) |
| Same fingerprint, `receiving` | 200 | `{"id":12,"state":"receiving","pieces_received":40,…}` — resume |
| `size` over the raw ceiling (piece count × limit assumptions) | 413 | `{"error":"…limit…"}` |
| Bad JSON / missing fields | 400 | `{"error":"…"}` |

### `PUT /app/combatlogs/uploads/{id}/pieces/{n}` — one piece *(gzip member body)*

Body: one complete gzip member, at most 8 MiB compressed (a raw 8 MiB piece never
compresses larger; the server refuses more). `Content-Type: application/gzip`.

| Condition | Status | Result |
|---|---|---|
| `n == pieces_received`, within limit | 200 | `{"pieces_received": n+1}`; appended, `stored_size` and `updated_at` updated |
| `n < pieces_received` | 200 | Same body; the piece is ignored (a retried request) |
| `n > pieces_received` | 409 | `{"pieces_received": …}` — client rewinds |
| Upload not `receiving`, or not this member's | 404 | |
| Would exceed the 500 MB compressed limit | 413 | Upload marked `failed` ("larger than the site accepts"); file deleted |
| Body is not a gzip member | 400 | Piece not stored |

The handler reads the body with a hard cap and writes it straight to the file; no
buffering beyond the HTTP server's own. Each request is small enough for the server's
existing 15 s / 45 s timeouts on any usable connection.

### `POST /app/combatlogs/uploads/{id}/finish` — all pieces sent *(JSON)*

| Condition | Status | Result |
|---|---|---|
| `pieces_received == pieces_total` | 200 | `{"state":"queued","url":"/app/combatlogs/uploads/17"}` |
| Pieces missing | 409 | `{"pieces_received": …}` |

### `GET /app/combatlogs/uploads/{id}`

One upload: its state and, once parsed, its fights grouped as the night went, each
with the member's characters and their numbers (FR-029). Carries the `Refresh: 5` header
while `queued` or `parsing`.

| Condition | Status | Result |
|---|---|---|
| This member's upload | 200 | Page |
| `failed` | 200 | Page with the reason and "upload again" |
| Not this member's, or `removed` | 404 | |

### `POST /app/combatlogs/uploads/{id}/remove`

Form, CSRF field. Sets `removed`, cascades fights, summaries and analyses, writes
`combatlogs.remove`, redirects to `/app/combatlogs` (FR-031).

### `POST /app/combatlogs/analyses` — run a comparison *(amended three times, 2026-09-16)*

Form fields: `character` (`realm-slug/name`, one of the account's characters),
`source` (`wcl` — the default: the character's latest ranked kills on Warcraft
Logs — or `upload:<id>`, one of the member's parsed uploads), CSRF field. Posted
from the character card.

| Condition | Status | Result |
|---|---|---|
| Valid, allowance available | 303 | Analysis created `pending` with its source; → `/app/dashboard?c=<character key>` where the card shows "analysing" with the `Refresh` header |
| `wcl`: Warcraft Logs knows no such character, or no ranked kill in the current raid | 303 | A **showcase** is created `pending` instead (source `showcase`): the top parses of the character's class and spec, from Blizzard's profile, on every boss of the current raid; → the card as for a valid run |
| `wcl`, showcase: Blizzard's profile has no specialization for the character | 303 | → the card with `msg=nospec`; nothing created |
| `wcl`, showcase: the current raid cannot be read, or its top parses of the class and spec cannot be read | 303 | → the card with `msg=nologs` ("…and the top parses of its class could not be read just now"); nothing created |
| `upload`: the upload has no raid pulls for the character | 303 | → the card with `msg=nopulls`; nothing created |
| The log recorded no specialization (advanced logging off) | 303 | → the card with `msg=nospec`; nothing created |
| Nobody of that class and spec is ranked on the main boss at that difficulty | 303 | → the card with `msg=norank`; nothing created; allowance untouched |
| Warcraft Logs cannot be reached, or no client configured | 303 | → the card with `msg=unavailable`; nothing created (FR-041) |
| Inside the 120-minute window (member, not officer) | 303 | → the card with `msg=wait&min=N` (FR-039) |
| Upload not this member's or not parsed; character not on the account; a `source` of neither form | 404 | |

The character's standing (`wcl`) or the upload's pulls, and the leaderboard
lookup for the top player, happen **in the request** (a second or two) so the
refusals above can be given before anything is created; the character's latest
kill per boss (`wcl`), the top player's parses on the other bosses (cached a day
each) and the model call run in the background worker. A showcase looks the
top player up on the raid's first boss in the request (Mythic, then Heroic);
the worker finds the top player on every other boss and fetches the
character's current equipment and build from Blizzard as the site; nothing
about play is read. Every outcome that creates a row writes
`combatlogs.analyse` when the worker finishes (FR-040).

---

## Dashboard additions — `/app/dashboard`

### `GET /app/dashboard?c=<key>` (existing)

The selected character's card gains:

- a **Talents** block under Equipped: from Blizzard's active loadout, or from the
  character's latest parsed pull with the date, or "unavailable" (FR-033, D8);
- an **Analyse** form inside the Armory panel, under the render (fourth
  amendment): a source picker — "My latest raid on
  Warcraft Logs" first, then each of this member's uploads with raid pulls for the
  character (file, date, pull count) — a hidden character field, and the button;
- the **result column** to the right of the card (fourth amendment): the upgrade
  table, the talent difference and the write-up, from the
  newest `done` analysis for this character, with "Analysed <time> against <name>"
  (a showcase says instead "No logs of yours yet, so this is the other way round:
  the top <spec> <class> parses, broken down, against your current gear");
  a `pending` newest analysis shows "analysing…" and the meta refresh; a `failed`
  newest analysis shows its reason above the previous result (FR-038, FR-041).

Messages from the analyse route arrive as a one-shot query flag
(`?c=<key>&msg=<code>`), rendered by the card and never echoed verbatim.

---

## Static

### `GET /static/upload.js`

The uploader. Progressive: the upload form's plain `<input type="file">` and
`<button>` exist without it, but submitting without the script does nothing useful
— the page says a current browser is needed. This is the one interaction the spec's
constraint admits scripting for.

Behaviour: fingerprint → begin → for each piece: slice, gzip via
`CompressionStream`, `PUT`; on any network error, re-`begin` (which returns
`pieces_received`) and continue; on `413`/`400` stop and show the server's message;
after the last piece, `finish` and navigate to `url`. Progress is
`pieces_received / pieces_total`.

CSP: `script-src 'self'` already permits it; `connect-src 'self'` is added
explicitly so the `fetch` calls are allowed if the policy ever gains a
`default-src`.
