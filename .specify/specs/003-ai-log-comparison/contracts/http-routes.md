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

### `POST /app/combatlogs/analyses` — run a comparison

Form fields: `summary` (a `fight_summaries.id` of one of the member's characters),
`link` (a Warcraft Logs character URL), CSRF field. Posted from the character card.

| Condition | Status | Result |
|---|---|---|
| Valid, allowance available | 303 | Analysis created `pending`; → `/app/dashboard?c=<character key>` where the card shows "analysing" with the meta refresh |
| Link not a Warcraft Logs character link | 303 | → the card with the message "…looks like https://www.warcraftlogs.com/character/us/area-52/name" (FR-034); nothing created |
| Comparison player has no rank on this boss and difficulty | 303 | → the card with the message; nothing created; allowance untouched (FR-035, scenario 3) |
| Warcraft Logs cannot be reached | 303 | → the card with "could not be completed, try later"; nothing created (FR-041) |
| Inside the 120-minute window (member, not officer) | 303 | → the card with "you can run another in N minutes" (FR-039) |
| `summary` not one of this member's | 404 | |

The Warcraft Logs fetch happens **in the request** (about a second, cached a day)
so the two refusals above can be given before anything is created; only the model
call runs in the background worker. Every outcome that creates a row writes
`combatlogs.analyse` when the worker finishes (FR-040).

---

## Dashboard additions — `/app/dashboard`

### `GET /app/dashboard?c=<key>` (existing)

The selected character's card gains:

- a **Talents** block under Equipped: from Blizzard's active loadout, or from the
  character's latest parsed pull with the date, or "unavailable" (FR-033, D8);
- an **Analyse** form in the left panel: a fight picker listing this character's
  parsed fights (boss, difficulty, kill/wipe, date), a link field, and the button.
  With no parsed fights the panel says so and links to Combat logs;
- the **upgrade table** in the left panel and the **write-up** in the right, from the
  newest `done` analysis for this character, with "Analysed <time> against <name>";
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
