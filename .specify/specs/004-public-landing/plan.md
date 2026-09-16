# Implementation Plan: The front door

**Branch**: `004-public-landing` | **Date**: 2026-09-16 | **Spec**: spec.md

## Summary

A Public, Landing app (`internal/apps/welcome`) draws `/` for anonymous visitors.
The core gains two `AppMeta` flags and a way to hand its landing notices to the
app. The guild's numbers come from the roster cache and per-member profiles
fetched with the site's own token, through board code moved out of the guild
app into `internal/armory` so both pages compute the same lists.

## Technical Context

- Go 1.27, stdlib plus the existing `golang.org/x/sync` (errgroup, singleflight).
  No new dependency.
- `html/template`, one embedded template plus the shared armory partial (for
  the rank mark). No JavaScript.
- Blizzard: `HTTPClient.AppToken` (client credentials, already used by Game
  Data) exposed as the optional `blizzard.AppTokenSource` interface; guild
  roster and character profile endpoints accept that token.
- Storage: none.
- Tests: in-package unit tests over a fake client; an external test mounting
  the app behind the real core anonymously; platform tests for the two flags.

## Constitution Check

- I (stdlib-first): no new modules.
- II (modular apps): one app, one line in main.go; the `AppMeta` widening is the
  documented escape hatch (docs/adding-an-app.md, "If you need something the
  core does not offer"); the shared board code lives in `internal/armory`, the
  place for blocks two apps draw.
- VI (tests): board ordering keeps its table test (moved with the code);
  officer selection, snapshot warming and staleness, the page for each
  configuration, and no-BattleTag are tested.
- VII (YAGNI): two flags because the front door needs both; nothing for a
  second public app that does not exist.

## Project Structure

```
internal/apps/welcome/           app.go, templates/welcome.html, app_test.go, integration_test.go
internal/armory/boards.go        MemberDetail, MemberKey, Builder.Details, Boards, Standing (from guild)
internal/armory/templates/armory.html   gains the "rank-mark" partial (from guild.html)
internal/platform/app.go         AppMeta.Public, AppMeta.Landing
internal/platform/mount.go       validation, no session gate for Public, "/" handed to the Landing app, LandingMessage
internal/platform/config.go      DiscordInvite (TOMB_DISCORD_INVITE)
deploy/, tofu/, .github/workflows  the variable's five hops
internal/platform/static/style.css  .welcome layout
```

## Decisions

- **D1: an app, not a bigger core page.** The core's landing template stays as
  the fallback when no app claims Landing (tests mount stub apps without one).
- **D2: the page never waits.** The first view after a start renders without
  numbers and starts the load; main warms the snapshot at start so that view is
  rare. A load is one at a time (a flag under the mutex), and a failure keeps
  the last snapshot.
- **D3: the core rewrites `/` to the app's root.** The landing handler clones
  the request with its notice in the context and serves it through the mux at
  the app's prefix, so the app registers nothing outside its prefix and the
  core keeps owning `/`.
- **D4: no links on the top lists.** Anonymous visitors have nowhere to go.

## Complexity Tracking

None: no deviation from the constitution.
