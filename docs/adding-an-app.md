# Adding an app

The platform is a thin core plus independently addable apps (constitution
Principle II). Adding one touches **two places**: your app's own package, and
one line in `cmd/tomb/main.go`. You never edit authentication, session handling,
or another app — and there is a test that fails if you have to
(`internal/platform/extensibility_test.go`).

## 1. Create the package

```
internal/apps/roster/
├── app.go                 # implements platform.App
└── templates/
    └── roster.html
```

## 2. Implement two methods

```go
package roster

import (
    "embed"
    "html/template"
    "net/http"

    "github.com/anthony-hopkins/tomb/internal/platform"
)

//go:embed templates/roster.html
var templateFS embed.FS

type App struct {
    deps platform.Deps
    tmpl *template.Template
}

var _ platform.App = (*App)(nil)

func New(deps platform.Deps) (*App, error) {
    tmpl, err := template.ParseFS(templateFS, "templates/roster.html")
    if err != nil {
        return nil, err
    }
    return &App{deps: deps, tmpl: tmpl}, nil
}

func (a *App) Meta() platform.AppMeta {
    return platform.AppMeta{
        Slug:          "roster",
        NavLabel:      "Roster",
        RoutePrefix:   "/app/roster", // must be "/app/" + Slug
        RequiresGuild: true,          // the core enforces the guild gate for you
    }
}

func (a *App) Routes(r platform.Registrar) {
    // Patterns are relative to your RoutePrefix.
    r.Handle("GET /", http.HandlerFunc(a.index))
    r.Handle("GET /{name}", http.HandlerFunc(a.character))
}
```

## 3. Register it

One line in `cmd/tomb/main.go`:

```go
apps := []platform.App{
    characterDashboard,
    rosterApp,          // <- this is the only core change
}
```

Startup fails loudly on a duplicate `Slug`, a duplicate `RoutePrefix`, or a
`RoutePrefix` that does not match `"/app/" + Slug`. That is deliberate: a
mistake here should never reach a request.

## What the core does for you

- **Session resolution.** A viewer without a live session is redirected before
  your handler runs. Read the viewer with `platform.SessionFrom(r.Context())`.
- **The guild gate.** Set `RequiresGuild: true` and non-members never reach your
  handler; they get the members-only page (FR-013a).
- **Character data.** For guild-gated routes the core has already fetched the
  viewer's characters for this request. Read them with
  `platform.ProfileFrom(r.Context())` — do **not** call Blizzard yourself, or
  the page will pay the `1 + N` fan-out twice.
- **The page shell.** Render your body, then hand it over:

  ```go
  a.deps.RenderInLayout(w, r, http.StatusOK, "Roster", template.HTML(body.String()))
  ```

- **Navigation.** Built from your `Meta()`, in `NavOrder` (lowest first; ties
  by label). Nothing to register. An empty `NavLabel` leaves the app out of the
  navigation without making it unreachable. My Characters is 10 and Coming
  Soon 20; pick a number that puts yours where it belongs.
- **Officers.** For a page only the guild master and officers should see, set
  `OfficerOnly: true` alongside `RequiresGuild: true`. The core hides it from
  everyone else's navigation and answers their requests with the officers-only
  page. For a page everyone sees but only officers may change, read
  `platform.ProfileFrom(r.Context()).Membership.IsOfficer` in your handler.
  The threshold is `TOMB_GUILD_OFFICER_RANK` (default 1: the guild master and
  the rank below).
- **Home.** One app may set `Home: true`; that is where `/` sends a signed-in
  viewer, where sign-in lands, and where the brand link goes. Give that app an
  empty `NavLabel`, or it is listed beside a link that already goes there.
  `Mount` refuses two apps claiming it.
- **The header ticker.** An app with something worth saying on every page
  (the calendar: the next two events) implements `platform.Headliner`
  alongside `App`:

  ```go
  func (a *App) Headlines(r *http.Request) []platform.Headline
  ```

  The core asks every mounted Headliner while it draws the shell and shows
  the lines in the header, between the navigation and the sign-out. It is
  gated the way your pages are: a viewer who could not reach your app is not
  shown your headlines. Return a handful at most, already worded for a glance
  (`When: "Today 20:00"`), and return nothing on an error — the page is about
  something else, so say so on your own page instead.
- **Outside services.** `Deps.WCL` reads Warcraft Logs (one method, a query;
  nil when the site has no client, which an app reads as "unavailable") and
  `Deps.AI` writes through Vertex AI as the VM. Both are lent like `Blizzard`
  so a test hands in a fake. The Blizzard client also offers two optional
  interfaces, `blizzard.SpecializationsReader` and `blizzard.GameData`, which
  the live client satisfies and a fake need not: type-assert, and fall
  through when they are absent.
- **A scripted request.** A `fetch` call carries the CSRF token in the
  `X-CSRF-Token` header instead of a form field; the core's verifier treats
  the two alike. The uploader is the one such caller.
- **Work in the background.** Create a row, run the work in a goroutine
  started from `main.go`, and while the row is unsettled set a `Refresh: 5`
  header on the page that shows it. No polling script; the header goes away
  with the state.
- **`Cache-Control: no-store`,** security headers, and structured request
  logging. All automatic.

## Rules

1. Register only under your own prefix. The `Registrar` enforces this.
2. Never import another app's package. Shared logic belongs in the core or in
   its own package.
3. Never read or write the session cookie, the OAuth state, or the Blizzard
   access token directly. The core resolves identity; you receive it.
4. Own your own templates and data access. The core owns the shell and the
   database handle.

## Tests you should write

Follow the dashboard's example (Principle VI requires table-driven tests for
business logic):

- Unit tests for your app's own logic, with no HTTP involved.
- An external-package test (`package roster_test`) that mounts your app behind
  the real core with a faked `blizzard.Client`. See
  `internal/apps/dashboard/integration_test.go`; it cannot be an internal test,
  because the core cannot import an app.

## Shared building blocks

`internal/armory` renders one character the way the in-game Armory does:
portrait, summary, and every equipped item with its tooltip. Both existing apps
use it, which is why a tooltip fix lands on both pages at once. To render it:

```go
tmpl, err := template.ParseFS(templateFS, "templates/roster.html")
// ...
tmpl, err = tmpl.ParseFS(armory.FS, "templates/armory.html")
```

then `{{template "armory-panel" .Panel}}` in your page with an `*armory.Panel`,
built by `armory.Builder`: `Of` when you already hold the `blizzard.Character`,
`For` when you know only the name and realm and need the profile fetched first.

`internal/fights` is the other shared package: the store the Combat logs app
writes (uploads, fights, summaries, analyses) and the character card reads.
Two apps needing one store is what makes a shared package rather than an app's
own; neither app imports the other. `internal/combatlog` (the parser),
`internal/wcl` and `internal/ai` are leaf packages the same way.

Parse the partial in the same function your tests use to build the template. A
test that parses only your page renders a template the app never uses, and goes
on passing while the real page fails on the missing partial.

## If you need something the core does not offer

Principle VII says the extension point stays minimal until a second real app
proves otherwise — which is exactly what your app is. Widening `AppMeta` or
`Deps` is fair game when you have a concrete need; update
`contracts/app-registration.md` and its tests in the same change so the contract
and the code stay honest.
