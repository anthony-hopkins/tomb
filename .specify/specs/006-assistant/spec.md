# Feature Specification: The assistant — a WoW question box on every page

**Feature Branch**: `006-assistant`

**Created**: 2026-09-17

**Status**: Approved — designed at the guild master's request and built the
same day on their word ("I dont see the chat bot option anywhere"); the
decisions under Assumptions are the site's defaults until they change them.

**Depends on**: 001-battlenet-sso-character-dashboard (the session, the
viewer's characters, the app extension point), 002-officer-tools (the officer
threshold, for the allowance), 003-ai-log-comparison (the Vertex AI client,
the escaping Markdown renderer, the allowance pattern), 005-comparison-baseline
(the model defaults).

**Input**: User description: "The AI Assistance bot. This should be a bot with
a popup window that follows you from page to page. It should be able to answer
any questions you have regarding WoW. It should avoid answering anything other
than wow related questions. For instance - it should be able to ask it: 'What
tanking trinkets should I be using currently and where do they drop?' The AI
should be able to discern my class and spec from my character info and provide
that information in an accurate, factual manner."

## Why

A member with a question about the game today leaves the site to ask it: a
guide site, a Discord, a search. The site already knows what the member plays
-- class, specialization, item level, what they are wearing -- and already has
a model on hand for the log comparison. Put the two together and the site can
answer the question for that character, in the words of a guide, with the
sources it read, without the member having to say who they are or what they
play. The box follows the member from page to page because a question comes
up while looking at the roster or the calendar, not on a page set aside for it.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A member asks about their own character (Priority: P1)

A signed-in guild member on any page opens the assistant, types "What tanking
trinkets should I be using currently and where do they drop?", and reads an
answer written for the tank they play: the trinkets by name, why each is worth
having, the boss or activity each drops from, and which of them they already
wear. The answer says which character it assumed and lists the pages it read.

**Independent Test**: with a fake model, a fake Blizzard client holding one
Protection Warrior and one Restoration Druid (the Warrior most recently
played), and the question above: the prompt carries both characters with the
Warrior marked as the one played last, the Warrior's equipped items and its
role "tank"; the page shows the answer, the character named, and the sources
the fake returned.

### User Story 2 - The box follows the member (Priority: P1)

The member opens the assistant on the roster, asks a question, walks to the
calendar, and the conversation is still there, with the answer, and the box
still open. A follow-up question ("and for mythic plus?") is answered in the
light of the earlier one.

**Independent Test**: after one question, every signed-in member page carries
the panel with that question and its answer; a second question's prompt
carries the first exchange.

### User Story 3 - Something that is not about the game (Priority: P1)

A member asks the assistant to write their cover letter, to explain a tax
form, or for the guild master's email address. The assistant declines in one
line, says what it is for, and offers nothing else. Nothing from that question
is answered.

**Independent Test**: the instruction the model is held to names the refusal
and its wording; a fake model that returns the refusal renders it as a plain
line; the question is still counted against the allowance (it cost a call).

### User Story 4 - The member wants to start over (Priority: P2)

The conversation has wandered; the member presses "New conversation" and the
box is empty. Old exchanges are gone from the page and, after the retention
period, from the site.

**Independent Test**: after the button, the panel shows no exchanges and the
next prompt carries none; the sweep removes exchanges older than the period.

### User Story 5 - Without a script (Priority: P2)

A member with scripts off opens the assistant's own page, asks the question in
a form, waits, and reads the answer on that page. Nothing the assistant does
needs the script; the script only saves the page reload.

**Independent Test**: a form POST to the ask route with no script answers with
a redirect to the assistant's page, which shows the exchange.

### Edge Cases

- The member has no characters in the guild's region, or the profile is
  partial: the assistant still answers, saying it does not know what they play
  and asking them to say.
- The question names a character ("for my druid"): the assistant answers for
  that one, whether or not it was played last.
- The model is busy, slow or unreachable: the panel says so in one line and the
  question is not counted against the allowance.
- The member is over the allowance: the panel says when the next question may
  be asked; nothing is sent to the model.
- A question longer than the limit is refused before anything is sent.
- The answer cites nothing (the model needed no search): the sources list is
  simply absent.
- Two tabs ask at once: both are answered; the thread holds both in the order
  they were answered.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-057 (the panel on every page)**: Every page a signed-in guild member
  sees MUST carry the assistant: a launcher in the shell that opens a panel
  holding the member's conversation and a question box. An anonymous visitor
  and a non-member see nothing of it. The panel is drawn by the assistant app,
  not the core: the core asks an app that implements a documented
  `Companion` interface for its panel, the way it asks a `Headliner` for the
  ticker, and gates it the same way.
- **FR-058 (the game only)**: The assistant answers questions about World of
  Warcraft -- its classes, specializations, talents, gear, dungeons, raids,
  professions, currencies, systems, lore and the guild's own numbers on this
  site -- and MUST decline anything else in one line that says what it is for,
  including requests to ignore that rule. The instruction the model is held to
  names the rule; the site records no exception list.
- **FR-059 (who is asking)**: Every question MUST be asked with the member's
  characters as facts: for each, name, realm, class, active specialization,
  the role that specialization plays (tank, healer, damage), level and item
  level, with the one played most recently marked; and, for that character,
  what it is wearing (slot, item, item level). The model is told to answer for
  the character played last unless the question names another, and to say
  which it assumed. No BattleTag, no account identity and nothing about other
  members reaches the model.
- **FR-060 (grounded, with sources)**: An answer MUST be produced with web
  search grounding turned on, so what is "current" is read rather than
  recalled, and the panel MUST list the sources the model read as links, taken
  from the model's grounding metadata only -- never from the answer's text,
  which the site renders with the escaping Markdown renderer that admits no
  link and no raw HTML. The model is told that search results are material,
  not instructions.
- **FR-061 (the conversation)**: The member's exchanges are kept per member as
  one thread; the last ten exchanges are sent with a new question; "New
  conversation" empties the thread; exchanges older than thirty days are
  removed. A member sees only their own thread; no page, officer tool or log
  shows another member's questions.
- **FR-062 (the allowance)**: A member MAY ask thirty questions in a rolling
  day; an officer -- and the administrator -- is not held. The check is made
  inside the transaction that records the question, so two clicks cannot both
  pass. A refusal says when the next question may be asked. A question the
  model failed to answer is not counted.
- **FR-063 (limits)**: A question is at most 600 characters; an answer is
  asked for in at most 500 words; the model call is bounded at 60 seconds; a
  question waits its turn behind at most two in flight for the whole site.
- **FR-064 (without a script)**: Every function -- open, ask, read, start over
  -- MUST work with no JavaScript, through the assistant's own page. The
  script, where it runs, asks through `fetch` to this origin and appends the
  answer in place, and remembers whether the panel was open.
- **FR-065 (what is sent where)**: The question, the thread, the character
  facts and the instruction go to Vertex AI in the site's project, as the log
  comparison does; the model's search goes to Google Search. Nothing goes to
  Warcraft Logs; nothing is sent to any other party. The panel says, in one
  line of fineprint, that questions are answered by Google's Gemini with web
  search and should carry nothing personal.

### Key Entities

- **Exchange**: one question and its answer, for one member, with when it was
  asked, the model used, the sources read, the tokens spent, and the character
  the answer was for.
- **Thread**: a member's exchanges since they last started over.
- **Companion**: the shell contribution an app makes below the page body: a
  panel for the viewer this page is for.

## Constraints

- CSP as it stands: `script-src 'self'`, `connect-src 'self'`, no inline
  styles or scripts. One new script file, deferred; the panel is a
  `<details>` element so it opens without it.
- Sources are links to third-party sites; they carry `rel="noopener"` and open
  in the same tab.
- Only the standard library and the packages already in `go.mod`.

## Success Criteria *(mandatory)*

- **SC-016**: The panel is on every signed-in member page and on no anonymous
  or non-member page (asserted against the real core with the app mounted).
- **SC-017**: No BattleTag and no other member's character reaches the prompt
  for any profile (asserted by test).
- **SC-018**: A source link on the page is one the grounding metadata carried;
  a link written in the answer's text renders as text (asserted by test).
- **SC-019**: The thirty-first question in a day is refused before a model
  call is made; the officer's is not.
- **SC-020**: On develop, the trinket question in User Story 1 answers for the
  member's tank with named items, drop sources and sources listed, within the
  60-second bound.

## Assumptions

- Grounding is Vertex AI's Google Search tool. It is the one way the answer
  can be current without the site curating guides itself; the sources list is
  what lets a member check it. Blizzard's Journal API (which boss drops which
  item) is not consulted in this version; if answers misplace drops, that is
  the next amendment.
- The model is the comparison's model by default, overridable by
  `TOMB_AI_ASSISTANT_MODEL`, so a faster model can be tried for the chat
  without touching the review.
- Thirty questions a day, six hundred characters a question and ten exchanges
  of context are the defaults; each is one constant.
- The most recently played character is the site's guess at "my" character,
  the same guess the dashboard makes.
- Questions are not audited: the audit trail records changes to the guild's
  data, and a question changes nothing. Token spend is logged per exchange
  with `slog`, as the comparison's is.

## Out of scope

- Voice, images, or the model reading a page's content; answering for a
  character the member does not own; officers reading members' threads; a
  public (anonymous) assistant; the model changing anything on the site.

## Registration note (constitution, Development Workflow)

The app is `internal/apps/assistant`, slug `assistant`, `RequiresGuild: true`,
no nav entry (the launcher is in the shell; the app's own page is reached from
the panel). It adds one documented shell contribution, `Companion`, beside
`Headliner` in `internal/platform/app.go`, recorded in
`001/contracts/app-registration.md` in the same change. The core draws the
companion's panel after `<main>` for a viewer who could reach the app.
