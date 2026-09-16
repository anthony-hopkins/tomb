# Tasks: The front door

**Spec**: spec.md | **Plan**: plan.md | **Branch**: `004-public-landing`

## Phase 1: Shared code

- [X] T076 `internal/armory/boards.go`: move `MemberDetail`, `MemberKey`, `Builder.Details`, `Boards`, `Standing`, `BarFloor` out of the guild app; `boards_test.go` keeps the standing table test.
- [X] T077 `internal/armory/templates/armory.html`: the `rank-mark` partial moves here from `guild.html`; the guild app uses the shared code through aliases and its tests stay green.
- [X] T078 `internal/blizzard/model.go`: `AppTokenSource`, the optional interface `HTTPClient` already satisfies.

## Phase 2: The core

- [X] T079 `internal/platform/app.go`, `mount.go`: `AppMeta.Public` and `AppMeta.Landing`; Mount refuses Public with a gate, Landing without Public, and two Landing apps; Public routes skip the session gate; `/` is served by the Landing app for an anonymous visitor with `LandingMessage` carrying the core's notice; tests in `landing_test.go`.
- [X] T080 `internal/platform/config.go`: `DiscordInvite` from `TOMB_DISCORD_INVITE`, `https://` only; the five hops (compose, configure.sh ×2, compute.tf, variables.tf) and the two workflows' `-var` lines.

## Phase 3: The app

- [X] T081 `internal/apps/welcome/app.go`: Meta, Routes, `Warm`, the snapshot (load one at a time, refresh on the roster interval, never on the request path), the view: officers, top five of each board, totals, Discord, the core's notice, signed-in variant.
- [X] T082 `internal/apps/welcome/templates/welcome.html`: welcome, sign-in, Discord, officers, top lists, TOMB Cares, the public-information note; `style.css` `.welcome` layout.
- [X] T083 Tests: `app_test.go` (officers, boards, warm, stale-once, no token source, no BattleTag) and `integration_test.go` (anonymous `/`, notices, signed-in redirect, `/app/welcome` signed in).
- [X] T084 `cmd/tomb/main.go`: register the app; `go Warm` at start.

## Phase 4: Docs

- [X] T085 `001/contracts/app-registration.md` and `http-routes.md`, `docs/adding-an-app.md` (public apps), `docs/deployment.md` (the variable), `README.md`.
- [ ] T086 Validate on develop: `/` anonymous shows the numbers within a minute of start; set `TOMB_DISCORD_INVITE` and see the link; sign in and land on the roster.
