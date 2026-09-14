// Package comingsoon is the roadmap: what TOMB is being pointed at, none of
// which exists yet.
//
// It is a whole app rather than a strip at the foot of the dashboard because
// that is what the platform's extension point is for -- it owns a route, a nav
// entry and a template, and the dashboard no longer has to carry copy that has
// nothing to do with characters. Adding it took one line in cmd/tomb
// (Principle II).
package comingsoon

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"

	"github.com/anthony-hopkins/tomb/internal/platform"
)

//go:embed templates/comingsoon.html
var templateFS embed.FS

// Item is one entry on the roadmap.
//
// Data rather than markup so the list is testable, and so AI is a field rather
// than a hand-repeated span -- mislabelling a calendar as AI-driven would be a
// claim about the product, not a styling slip.
type Item struct {
	Title string
	Blurb string
	AI    bool
}

// Roadmap is placeholder copy. Nothing here is wired to anything and none of it
// carries a date.
var Roadmap = []Item{
	{
		Title: "Guildmates' characters",
		Blurb: "See what the rest of TOMB is playing, not just your own roster.",
	},
	{
		Title: "Guild calendar",
		Blurb: "Events and plans in one place, so raid nights stop living in Discord scrollback.",
	},
	{
		Title: "Ask TOMB Bot",
		AI:    true,
		Blurb: "Ask about the guild, the schedule, what is running this week, or just for advice.",
	},
	{
		Title: "Combat log analysis",
		AI:    true,
		Blurb: "Compare your logs against the top performers of your class, see exactly where " +
			"the differences are and what each one costs you, with suggested fixes.",
	},
	{
		Title: "Gear analysis",
		AI:    true,
		Blurb: "Compare your gear to the top performers and get the path of least resistance to " +
			"your next upgrades, prioritised so your resources always go where they matter most.",
	},
}

// App is the Coming Soon page.
type App struct {
	deps platform.Deps
	tmpl *template.Template
}

var _ platform.App = (*App)(nil)

// New builds the app from the dependencies the core lends it.
func New(deps platform.Deps) (*App, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/comingsoon.html")
	if err != nil {
		return nil, err
	}
	return &App{deps: deps, tmpl: tmpl}, nil
}

// Meta describes the app to the core (contracts/app-registration.md).
func (a *App) Meta() platform.AppMeta {
	return platform.AppMeta{
		Slug:        "coming-soon",
		NavLabel:    "Coming Soon",
		RoutePrefix: "/app/coming-soon",
		// The whole site is members-only, and a roadmap is guild business.
		RequiresGuild: true,
	}
}

// Routes registers the app's handlers relative to its own prefix.
func (a *App) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(a.show))
}

type view struct {
	Roadmap []Item
}

func (a *App) show(w http.ResponseWriter, r *http.Request) {
	var body bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&body, "comingsoon.html", view{Roadmap: Roadmap}); err != nil {
		a.deps.Logger.Error("render coming soon", "error", err)
		http.Error(w, "Something went wrong rendering this page.", http.StatusInternalServerError)
		return
	}

	a.deps.RenderInLayout(w, r, http.StatusOK, "Coming Soon", template.HTML(body.String()))
}
