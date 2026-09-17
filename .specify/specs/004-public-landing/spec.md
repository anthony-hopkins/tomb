# Feature Specification: The front door — a public landing page

**Feature Branch**: `004-public-landing`

**Created**: 2026-09-16

**Status**: Approved — requested directly by the guild master; defaults assumed
and recorded under Assumptions.

**Depends on**: 001-battlenet-sso-character-dashboard (the landing route, the
app extension point, the roster cache), 002-officer-tools (ranks and the
officer threshold), 003-ai-log-comparison (the site's own Blizzard token).

**Input**: User description: "We need a landing page that is for non guild
members for recruiting and interest purposes. There should be a landing page for
unauthenticated users that both provide a login option as well as general guild
information such as our top lists as well as TOMB discord info, guild master and
officer list/info, as well as a heartwarming yet witty welcome message. There
should also be some focus on TOMB Cares as can be seen in the discord."

## Why

Today an anonymous visitor to the site sees a sign-in button and nothing else.
Somebody who heard of the guild, or was whispered a link, learns nothing: not who
runs it, not how good it is, not where its Discord is, not what it stands for.
The guild recruits and the site should help, and it should say up front what
makes the guild more than a raid team: TOMB Cares, the members' mental-health
initiative that already has its own place in the Discord.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A stranger learns who TOMB is (Priority: P1)

A player who is not signed in opens the site and reads a welcome, who the guild
master and officers are, how the guild's best characters are doing, where the
Discord is, what TOMB Cares means, and how to sign in if they are a member.

**Independent Test**: open `/` with no session; every section renders; the
"Sign in with Battle.net" action is present.

**Acceptance Scenarios**:

1. **Given** no session, **When** `/` is opened, **Then** the page shows a
   welcome, a sign-in form, the Discord link or the way to get one, the guild
   master and officers, the top lists, and the TOMB Cares section.
2. **Given** a valid session, **When** `/` is opened, **Then** the viewer is sent
   to their home as before; the front door is still reachable at its own path.
3. **Given** the site was just started and the guild's numbers are not in yet,
   **When** `/` is opened, **Then** the page renders at once without officers
   or top lists, saying they are on their way, and a later visit has them.
4. **Given** Blizzard cannot be reached, **When** `/` is opened, **Then** the
   page renders with whatever numbers were last held, or without them; never
   an error page.

### User Story 2 - A member finds the way in (Priority: P1)

A member opens the site, sees the sign-in action first, signs in, and lands on
the roster as before. A sign-out or a re-authorization message still shows on
the front door.

**Acceptance Scenarios**:

1. **Given** `/?signed_out=1`, **When** opened anonymously, **Then** the page
   carries the signed-out notice above the sign-in action.
2. **Given** the deploy pipeline's landing check, **When** it fetches `/`,
   **Then** the literal "Sign in with Battle.net" is in the body.

### User Story 3 - The officers point recruits somewhere (Priority: P2)

A recruit is told "go to the site". They find the Discord invite when the guild
has configured one, and otherwise are told to whisper an officer named on the
page.

**Acceptance Scenarios**:

1. **Given** `TOMB_DISCORD_INVITE` is set, **When** `/` is opened, **Then** a
   "Join the TOMB Discord" link leads there.
2. **Given** it is unset, **When** `/` is opened, **Then** the page says to ask
   an officer in game, and the officers are listed beside it.

### Edge Cases

- A rank name list (`TOMB_GUILD_RANKS`) that is empty: the guild master is
  labelled "Guild Master" and others "Rank N", as the roster does.
- A member whose profile Blizzard will not serve: they keep their place on the
  officer list without class colour or item level, and are absent from lists
  that need a number.
- A Discord invite that is not an `https://` link: refused at start, like other
  misconfiguration.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-042 (a public app)**: An app MAY declare itself Public: the core applies no
  session gate to its routes, and its handlers run with no viewer in the context.
  Public MUST NOT be combined with guild or officer gating; the core refuses to
  mount such an app. One Public app MAY declare itself the Landing app: its root
  page answers `GET /` for an anonymous visitor in place of the core's plain
  sign-in page, and the core's own notices (signed out, re-authorize) reach it.
  A signed-in viewer is still sent from `/` to their home.
- **FR-043 (the front door)**: The landing page MUST carry: a welcome; the
  sign-in action, worded "Sign in with Battle.net"; the guild's Discord invite
  when configured, else how to get one; the guild master and officers, by
  character name, class, specialization and rank; the guild's top lists; and the
  TOMB Cares section. It MUST render for an anonymous visitor with no call to
  Blizzard on the request path.
- **FR-044 (the guild's numbers, as the site)**: The roster and each member's
  public profile, item level, Mythic+ rating and raid progress MUST be fetched
  with the site's own Battle.net token (client credentials), never a member's,
  held between requests and refreshed in the background on the roster interval
  (`TOMB_GUILD_ROSTER_TTL`, an hour by default), and warmed once at start. A
  failed refresh keeps what is held.
- **FR-045 (the officers)**: The guild master and officers are the roster members
  at or above the configured officer rank (`TOMB_GUILD_OFFICER_RANK`, default 1),
  in roster order, labelled by `TOMB_GUILD_RANKS`.
- **FR-046 (top lists)**: The same three leaderboards the guild overview draws --
  top item level, top Mythic+ rating, most raid bosses down -- computed by the
  same code, cut to five places, names not linked (there is nothing an anonymous
  visitor may open).
- **FR-047 (what is public)**: The page shows character names and the public
  Armory facts about them, the same information Blizzard's own Armory shows for
  any guild. It MUST NOT show BattleTags, account identities, sign-in history,
  the calendar, the logs, or anything from a member's combat logs.
- **FR-048 (TOMB Cares)**: The section states what TOMB Cares is, what it
  provides, what it is not, and the responsibility "Listen. Support. Connect.",
  in the guild's own words from the Discord announcement, with a line on where
  to turn in a crisis.
- **FR-049 (configuration)**: `TOMB_DISCORD_INVITE`, optional, an `https://`
  link, plumbed like every other `TOMB_*` variable (tofu variable, metadata,
  configure.sh, .env, compose).

### Key Entities

- **Front-door snapshot**: the roster with each member's detail, as of one
  refresh, held by the welcome app; the same shape the guild overview holds,
  built by shared code in `internal/armory`.

## Constraints

- No JavaScript, no external embeds: the CSP allows no Discord widget, so the
  Discord is a link. No inline styles; bars are SVG attributes as on the guild
  page.
- Nothing on the page needs a database.

## Success Criteria *(mandatory)*

- **SC-009**: `/` renders for an anonymous visitor in under 200 ms on a warm
  snapshot, with no outbound call.
- **SC-010**: After a start, the guild's numbers are on the page within the
  time one roster load takes (under a minute for a guild of two hundred).
- **SC-011**: No BattleTag appears in the page body for any roster and any
  configuration (asserted by test).

## Assumptions

- The welcome copy and the TOMB Cares text are the site's; the guild master
  edits them in the template, not in configuration.
- Top lists cut at five: a front door is a glance, not the guild page.
- The Discord invite is a public link, so it is a variable, not a secret.
- A crisis line is named (988 in the US and Canada; findahelpline.com elsewhere).

## Out of scope

- A recruitment form or application flow; a public calendar; per-character
  public pages; search engines' structured data.

## Registration note (constitution, Development Workflow)

The app is `internal/apps/welcome`, slug `welcome`, `Public: true`,
`Landing: true`, no nav entry. It needs no guild gate. It widens `AppMeta` by
the two flags in FR-042, recorded in
`001/contracts/app-registration.md` and `http-routes.md` in the same change.

## Amendment, 2026-09-17: one officer line per person

The officer list showed the guild master's every alt, one line each, because
Blizzard's roster is a list of characters and says nothing about who plays
them. The site does learn that: a member's sign-in fetches their account's
characters. Those are now recorded (`character_owners`, migration 0008; only
members who have signed in, only the names the roster already shows), and the
front door folds an account's officer characters into one line -- the best
rank, then the highest item level, standing for the rest, with "and N more
characters". Characters of members who have never signed in stay one line each;
nothing can tell their alts apart. **FR-045** reads accordingly.
