# Adding an app

The platform is a thin core plus independently addable apps (constitution
Principle II). Adding one touches **three files**: your app's own package, and
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

- **Navigation.** Built from your `Meta()`. Nothing to register.
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

## If you need something the core does not offer

Principle VII says the extension point stays minimal until a second real app
proves otherwise — which is exactly what your app is. Widening `AppMeta` or
`Deps` is fair game when you have a concrete need; update
`contracts/app-registration.md` and its tests in the same change so the contract
and the code stay honest.
