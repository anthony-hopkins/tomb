// Package dashboard is the Character Dashboard: the first registrable app on
// the TOMB platform (FR-011).
//
// It implements platform.App and owns nothing but its own route, its own
// template, and the FR-006 selection rule. Authentication, session handling and
// the FR-013a guild gate all belong to the core, which is why this package
// imports no auth code and reads its character data from the request context.
package dashboard

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

//go:embed templates/dashboard.html
var templateFS embed.FS

// App is the Character Dashboard.
type App struct {
	deps platform.Deps
	tmpl *template.Template
}

// Compile-time proof this satisfies the platform's extension point.
var _ platform.App = (*App)(nil)

// New builds the app from the dependencies the core lends it.
func New(deps platform.Deps) (*App, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/dashboard.html")
	if err != nil {
		return nil, err
	}
	return &App{deps: deps, tmpl: tmpl}, nil
}

// Meta describes the app to the core (contracts/app-registration.md).
func (a *App) Meta() platform.AppMeta {
	return platform.AppMeta{
		Slug:        "dashboard",
		NavLabel:    "My Character",
		RoutePrefix: "/app/dashboard",
		// FR-013a: guild members only. The core enforces this before the
		// handler below ever runs.
		RequiresGuild: true,
	}
}

// Routes registers the app's handlers relative to its own prefix.
func (a *App) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(a.show))
}

// view is what the dashboard template renders.
type view struct {
	Character *characterView
	// Partial reports that some characters could not be loaded, so the page
	// can say so rather than silently showing a possibly-wrong winner
	// (research.md D9).
	Partial bool
}

type characterView struct {
	Name             string
	RealmName        string
	Class            string
	ActiveSpec       string
	Level            int
	AverageItemLevel int
	LastLogin        string
}

// show renders the member's most recently played character (FR-007).
//
// The characters were fetched once by the core for this request, so this
// handler makes no Blizzard calls of its own — a view still costs exactly one
// 1+N fetch (FR-016).
func (a *App) show(w http.ResponseWriter, r *http.Request) {
	profile, ok := platform.ProfileFrom(r.Context())
	if !ok {
		// Unreachable in production: the guild gate always attaches a profile
		// before this handler runs. Treated as an error rather than silently
		// rendering an empty page.
		a.deps.Logger.Error("dashboard reached with no profile in context")
		http.Error(w, "Character data unavailable.", http.StatusInternalServerError)
		return
	}

	v := view{Partial: profile.Partial}

	if current := SelectCurrent(profile.Characters); current != nil {
		v.Character = &characterView{
			Name:             current.Name,
			RealmName:        realmLabel(current),
			Class:            current.Class,
			ActiveSpec:       current.ActiveSpec,
			Level:            current.Level,
			AverageItemLevel: current.AverageItemLevel,
			LastLogin:        current.LastLogin.Format("2 Jan 2006, 15:04 MST"),
		}
	}

	var body bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&body, "dashboard.html", v); err != nil {
		a.deps.Logger.Error("render dashboard", "error", err)
		http.Error(w, "Something went wrong rendering your dashboard.", http.StatusInternalServerError)
		return
	}

	a.deps.RenderInLayout(w, r, http.StatusOK, "My Character", template.HTML(body.String()))
}

// realmLabel prefers the display name, falling back to the slug.
func realmLabel(c *blizzard.Character) string {
	if c.RealmName != "" {
		return c.RealmName
	}
	return c.RealmSlug
}
