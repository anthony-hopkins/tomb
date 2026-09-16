# Feature Specification: Officer tools — navigation order, officer gating, audit log, Logs, Calendar

**Feature Branch**: `002-officer-tools`
**Status**: Approved — requested directly by the guild master; defaults assumed.
**Depends on**: 001-battlenet-sso-character-dashboard (sessions, the guild gate, the
roster snapshot).

## Why

The guild's officers need two things the site has no notion of yet: a place to keep the
guild's schedule, and a record of who changed it. Both need the site to know who is an
officer. Membership today is a yes/no derived from a character's guild; rank is not
part of it, because the character profile does not carry rank — only the roster does.

## Requirements

- **FR-019 (navigation order)**: Apps MUST declare their position in the navigation
  bar. The bar MUST NOT be ordered by label. My Characters comes first, then Coming
  Soon, then Calendar, then Logs (for those who can see it).

- **FR-020 (the viewer's rank)**: For a signed-in member the system MUST resolve their
  guild rank: the highest rank (lowest index; 0 is the guild master) held by any of
  their characters that appears on the roster. An officer is a member whose rank is at
  or above a configured threshold, `TOMB_GUILD_OFFICER_RANK`, which defaults to 1 —
  guild master and officers, which in the game's own numbering are the first two
  ranks. If the roster cannot be consulted, the viewer is NOT an officer: the failure
  closes the door rather than opening it.

  The roster is held between requests and refreshed in the background on the same
  interval as the guild page's snapshot (FR-018); rank resolution costs no call of its
  own on a request.

- **FR-021 (officer-only apps)**: An app MAY declare itself officer-only. Such an app
  MUST NOT appear in the navigation of a viewer who is not an officer, and a request
  for any of its routes from such a viewer MUST be refused with a page saying so — not
  the members-only page, which would be a lie about why. Officer-only implies
  guild-gated; the core refuses to mount an app that claims the former without the
  latter.

- **FR-022 (audit log)**: Every change an officer makes through the site MUST be
  recorded persistently: who (account and battletag), when, what was done, to what,
  and enough detail to see what changed. Sign-ins and sign-outs are recorded too, so
  the log answers "who was here" as well as "who did what". The log is append-only:
  no route edits or deletes it.

- **FR-023 (Logs app)**: An officer-only app that lists the audit log, newest first,
  filterable by kind (sign-ins, calendar), paged. It shows the log; it does not
  interpret it.

- **FR-024 (Calendar app)**: A guild schedule every member can read and officers can
  edit: events with a title, a start, an optional end, an optional place and notes.
  Upcoming events are shown by day. Creating, editing and deleting an event is a
  POST guarded by the site's CSRF token, permitted only to officers, and each writes an
  audit entry describing the change. Deleting hides an event rather than destroying
  the row, so the audit trail always has something to point at.

- **FR-025 (the administrator)**: One Battle.net account MAY be configured as the
  site's administrator, `TOMB_ADMIN`, by its subject claim (preferred: the stable
  identity key) or its battletag. On every request the administrator is treated as
  a member and as an officer, whatever the roster says, so every service is open
  to them. Nothing else changes: their rank, and everything the interface draws
  from rank, is what the roster says, and no page, card, entry or log ever says
  who the administrator is. Administration is a fact about running the site, not
  a standing in the guild.

- **FR-026 (recurring events)**: An event MAY repeat: every day, every week or every
  two weeks, on chosen days of the week — raid days are one event, Tuesday and
  Thursday, not one event a week — until a last day, or until it is removed. The
  event holds the first time it happens; the schedule works out the rest when it is
  read, showing a repeating event eight weeks ahead where a one-off is shown however
  far off it is. Times hold to the wall clock in the guild's zone across a
  daylight-saving change: a raid at 20:00 is at 20:00 in November too. An officer MAY
  skip one occurrence of a repeating event without touching the rest; a skip names
  a day, so it survives the event's time being changed. Editing or removing acts on
  the whole series. Creating, editing, removing and skipping each write an audit
  entry, and each says how the event repeats.

## Registration and gating (constitution, Development Workflow)

Calendar registers as an ordinary guild-gated app (`RequiresGuild`) and decides
per-request, from the viewer's resolved rank, whether to show and accept edits. Logs
registers officer-only (`OfficerOnly`, which implies `RequiresGuild`); the core hides
and refuses it for everyone else. Neither app touches authentication or another app.

## Out of scope

Rank names still come from `TOMB_GUILD_RANKS`. Reminders and Discord integration are
the roadmap's business, not this feature's. One occurrence of a repeating event can be
skipped but not moved or edited on its own; a raid that moves for one week is a skip
and a one-off.
