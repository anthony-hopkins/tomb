# Feature Specification: Battle.net SSO Login & Character Dashboard

**Feature Branch**: `001-battlenet-sso-character-dashboard`
**Created**: 2026-09-13
**Status**: Clarified — ready for `/plan`
**Input**: User description: "I am building a web site for my WoW guild (TOMB). Currently
I want the site to feature the Blizzard SSO login portal. Once logged in it should show
the user's most current logged-in character. The web app will have several apps and be
extensible with more in the future."

## Clarifications

### Session 2026-09-13

- Q: How should the site decide whether a signed-in Battle.net user is a TOMB guild member, and what should someone who isn't a member be able to see? → A: Verify membership via a Blizzard guild roster lookup (configured guild name + realm); authenticated non-members are denied access entirely with a "this site is for TOMB members" message.
- Q: Should the site look at World of Warcraft characters from just one region (for example US), or gather a user's characters from every region their Battle.net account has? → A: One configured region (initially US); characters in other regions are out of scope for this feature.
- Q: How long should a user stay signed in on the guild site before they have to log in through Battle.net again? → A: An absolute lifetime aligned with the Blizzard access-token lifetime (~24 hours, read from the token response), with no activity-based extension.
- Q: When a member opens their dashboard, should the site call Blizzard for fresh character data every single time, or reuse recently fetched data for a while? → A: Live fetch on every dashboard view; no caching of character data between views.
- Q: If two of a user's characters report the exact same last-played time, which one should the dashboard show as their current character? → A: Highest level, then highest average item level, then character name ascending alphabetically.

## User Scenarios & Testing *(mandatory)*

### Session 2026-09-14

- Q: Should the dashboard show only the most recently played character, or every character on the account? → A: Every character, as a card each, ordered by the FR-006 rule with the most recent first and visibly marked. The 1+N fetch already retrieves them all, so showing one and discarding the rest spent the cost without taking the benefit.
- Q: Blizzard's account listing keeps returning characters whose profiles it no longer serves — deleted, renamed, or transferred away. How should those be treated? → A: Omitted silently. They are stale listing entries, not failures, and warning about them on every visit trains members to ignore the warning that matters. A character that fails for any other reason still counts as incomplete data.
- Q: FR-013 configures a guild "name and realm". Which realm is that? → A: The realm the guild was founded on, as Blizzard reports it on each character profile. On a connected-realm cluster that is routinely not the realm the members' characters occupy.

### Primary User Story
A TOMB guild member visits the guild website and signs in using their Battle.net
account instead of creating a separate username/password. After authorizing the
site with Blizzard, they land on their personal dashboard, which shows their World
of Warcraft characters, most recently played first — so they (and, in later apps,
their guildmates) can see "who they're currently playing" without asking in
Discord.

### Acceptance Scenarios
1. **Given** a visitor on the public landing page, **When** they click "Sign in with
   Battle.net", **Then** they are redirected to Blizzard's official Battle.net
   authorization page.
2. **Given** a user who approves the authorization request on Blizzard's site,
   **When** they are redirected back to the guild site, **Then** a session is
   created and they land on their dashboard without needing to enter any password
   on the guild site itself.
3. **Given** an authenticated user whose account has multiple WoW characters across
   one or more realms, **When** their dashboard loads, **Then** the site displays
   every retrieved character, each including at minimum: character name, realm,
   class, level, and average item level; the character with the most recent login
   time appears first and is visibly identified as the most recently played.
4. **Given** a user who clicks "Log out", **When** the logout completes, **Then**
   their session is terminated and they are returned to the public landing page,
   with no cached character data remaining visible.
5. **Given** a user who declines/cancels the authorization on Blizzard's consent
   screen, **When** they are redirected back to the guild site, **Then** the site
   shows a clear, friendly message explaining login was not completed and offers a
   way to try again, without creating a session.
6. **Given** the platform's app framework, **When** a new app (e.g., a roster
   viewer) is added in a future feature, **Then** it can be registered and appear
   in navigation without any code changes to the login flow, session handling, or
   other existing apps.
7. **Given** a Battle.net user who authenticates successfully but has no character in
   the TOMB guild roster, **When** their post-login redirect completes, **Then** they
   are shown a message stating the site is for TOMB members, are offered a log-out
   action, and are granted access to no app.
8. **Given** a user whose session has passed its absolute expiry, **When** they next
   request any page, **Then** they are treated as unauthenticated and prompted to
   sign in with Battle.net again rather than being shown previously fetched
   character data.

### Edge Cases
- What happens when a user's Battle.net account has zero WoW characters in the
  configured region (e.g., a brand-new account)? Guild verification cannot succeed,
  so the user receives the non-member message rather than an empty or broken
  character card.
- What happens when Blizzard's API is unavailable or rate-limits the request during
  or after login? The user MUST see a clear retry-able error, not a raw failure.
- What happens when a user revokes the site's authorization from their Battle.net
  account settings after already logging in? Their next action on the site MUST
  detect the invalid/revoked token and prompt re-login rather than showing stale
  data indefinitely.
- What happens when two of a user's characters show the exact same last-played
  timestamp (e.g., data granularity)? The tiebreaker is deterministic and total:
  highest level, then highest average item level, then character name in ascending
  alphabetical order (see FR-006).
- What happens when an authenticated user is **not** a member of the TOMB guild?
  Access is denied entirely — no dashboard and no app are reachable; the user sees a
  message stating the site is for TOMB members, plus a log-out action.

## Requirements *(mandatory)*

### Functional Requirements
- **FR-001**: The site MUST present a "Sign in with Battle.net" action on its
  public landing page.
- **FR-002**: The system MUST initiate Blizzard's OAuth2 authorization flow when
  the sign-in action is used, requesting only the scopes needed to read WoW
  profile/character data.
- **FR-003**: The system MUST handle the OAuth2 redirect callback, exchange the
  authorization code for tokens, and establish an authenticated session for the
  user.
- **FR-004**: The system MUST NOT collect, transmit, or store the user's Blizzard
  account password at any point.
- **FR-005**: After authentication, the system MUST retrieve the user's list of WoW
  characters via Blizzard's Profile API.
- **FR-006**: The system MUST rank the user's characters by last-login timestamp,
  most recent first, and MUST identify the first as the user's "most current"
  character. When two or more characters share the same timestamp, the system MUST
  break the tie deterministically in this order: highest level, then highest
  average item level, then character name ascending alphabetically. This ordering
  MUST be total, so both the ranking and the selected character are stable across
  repeated views of identical data. One ordering serves both purposes: the "most
  current" character is defined as the first of the ranked list, not computed
  separately from it.
- **FR-007**: The system MUST display every retrieved character on the user's
  dashboard, each showing at minimum its name, realm, class, level, and average
  item level, ordered per FR-006. The most current character MUST appear first and
  MUST be visibly identified as the most recently played.
- **FR-008**: The system MUST allow the user to explicitly log out, which MUST
  terminate the session and clear any locally displayed character data.
- **FR-009**: The system MUST handle authorization denial or cancellation from
  Blizzard's consent screen by returning the user to an unauthenticated state with
  a clear explanatory message, without creating a session.
- **FR-010**: The system MUST handle Blizzard API errors or unavailability by
  showing the user a clear, retry-able error rather than an unhandled failure.
- **FR-011**: The dashboard MUST be implemented as the first registrable "app" in
  an extensible apps framework, such that future apps can be added without
  modifying authentication, session management, or this app's code.
- **FR-012**: The system MUST detect a revoked or expired Blizzard authorization on
  next use and prompt the user to re-authenticate rather than displaying stale
  character data.
- **FR-013**: The system MUST verify TOMB guild membership by checking the
  authenticated user's characters against a configured guild name and realm, using
  the guild each character reports in Blizzard's Profile API. The configured realm
  is the realm the **guild** was founded on, as Blizzard reports it on the
  character profile — on a connected-realm cluster this is routinely not the realm
  the member's characters occupy, and configuring the member's realm instead
  refuses every member indistinguishably from nobody being in the guild.
- **FR-013a**: The system MUST deny site access to an authenticated user who is not a
  verified TOMB guild member, presenting a message stating the site is for TOMB
  members and a log-out action, and granting access to no app.
- **FR-014**: The system MUST operate against a single configured Blizzard region
  (initially US): only characters in that region are retrieved, considered for "most
  current character", or used for guild-roster verification. Characters in other
  regions are explicitly out of scope for this feature.
- **FR-015**: The system MUST expire a session at an absolute lifetime aligned with
  the Blizzard access-token lifetime (approximately 24 hours; the effective value
  MUST be derived from the token response rather than hardcoded), after which the
  user MUST re-authenticate via Battle.net. Sessions MUST NOT be extended by user
  activity beyond that absolute expiry.
- **FR-016**: The system MUST fetch the user's character data from Blizzard's
  Profile API on every dashboard view, with no caching of character data between
  views, so the displayed character always reflects what Blizzard returned for that
  request. Because every view re-issues the per-character fetches, rate-limit and
  error responses MUST be handled per FR-010.

- **FR-017**: Where Blizzard's account listing includes a character whose profile
  it no longer serves — deleted, renamed, or transferred off the account — the
  system MUST omit that character from the dashboard silently, presenting it
  neither as an error nor as incomplete data. A character that fails to load for
  any other reason MUST still be reported per FR-010, because the roster is then
  genuinely incomplete and likely to be complete on a later view.

### Key Entities
- **Guild Member (User)**: A person who has authenticated via Battle.net. Attributes:
  internal identifier, Blizzard account subject identifier, display name, guild
  membership status (derived from the guild on the member's own characters, never
  hand-entered),
  session state, timestamps for first/last login to the site.
- **Character**: A WoW character belonging to a User's Battle.net account.
  Attributes: name, realm, region (the single configured region), class, active
  specialization (if available), level, average item level, last-played timestamp,
  guild (name and realm, absent when unguilded), "is current character" flag. Per FR-016, Character data is transient — fetched
  from Blizzard per dashboard view and not persisted between views.
- **Session**: Represents an authenticated browser session tied to a User.
  Attributes: token/identifier, issued time, absolute expiry (derived from the
  Blizzard token lifetime), associated User.
- **App (platform concept)**: A registrable module within the extensible platform.
  Attributes: name, route prefix, navigation label, guild-membership requirement
  (if any). The Character Dashboard described in this spec is the first App.

## Review & Acceptance Checklist

### Content Quality
- [x] No implementation detail leaked into requirements (language/framework choices
      live in the constitution and plan, not here)
- [x] Focused on user value (guild members seeing "who's currently playing what")
      and business need (a shared guild identity hub)
- [x] Written for guild stakeholders, not just engineers
- [x] All mandatory sections completed

### Requirement Completeness
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable (specific fields shown, specific flows work)
- [x] Scope is clearly bounded to login + single-character dashboard as the first app
- [x] Dependencies (Blizzard OAuth2/Profile API availability) and assumptions
      identified

## Execution Status
- [x] User description parsed
- [x] Key concepts extracted (SSO login, current-character display, extensible apps)
- [x] Ambiguities resolved (5 clarifications — see Clarifications)
- [x] User scenarios defined
- [x] Requirements generated
- [x] Key entities identified
- [x] Review checklist passed
