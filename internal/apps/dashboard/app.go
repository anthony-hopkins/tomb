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
		NavLabel:    "My Characters",
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

// roadmapItem is one entry in the "what's coming" list at the foot of the
// dashboard.
//
// Placeholder copy, deliberately: nothing here is wired to anything, and none
// of it should be read as a promise about dates. It sits in Go rather than in
// the template so the list is data -- testable, and extendable without touching
// markup -- and so that the AI flag is a field rather than a hand-repeated bit
// of styling.
type roadmapItem struct {
	Title string
	Blurb string
	AI    bool
}

var roadmap = []roadmapItem{
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

// view is what the dashboard template renders.
type view struct {
	// Characters is the whole roster in FR-006 order, most current first.
	Characters []characterView

	// Roadmap is the placeholder "what's coming" list. Shown whether or not the
	// roster loaded: it is about the site, not about this account.
	Roadmap []roadmapItem

	// Partial reports that some characters could not be loaded, so the page can
	// say so rather than quietly showing an incomplete roster (research.md D9).
	//
	// Characters Blizzard reports as gone do NOT set this: the account summary
	// keeps listing deleted and transferred characters indefinitely, and a
	// notice that fires on every visit because of a character somebody deleted
	// years ago is one people learn to ignore.
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
	Guild            string

	// Current marks the most recently played character, which the roster still
	// leads with and calls out (FR-006, FR-007).
	Current bool
}

// show renders the member's roster, most recently played first (FR-006, FR-007).
//
// The characters were fetched once by the core for this request, so this
// handler makes no Blizzard calls of its own — a view still costs exactly one
// 1+N fetch (FR-016), whether it renders one card or twenty.
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

	v := view{Partial: profile.Partial, Roadmap: roadmap}

	for _, c := range Rank(profile.Characters) {
		v.Characters = append(v.Characters, characterView{
			Name:             c.Name,
			RealmName:        realmLabel(&c),
			Class:            c.Class,
			ActiveSpec:       c.ActiveSpec,
			Level:            c.Level,
			AverageItemLevel: c.AverageItemLevel,
			LastLogin:        c.LastLogin.Format("2 Jan 2006, 15:04 MST"),
			Guild:            guildLabel(&c),
			Current:          c.IsCurrent,
		})
	}

	var body bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&body, "dashboard.html", v); err != nil {
		a.deps.Logger.Error("render dashboard", "error", err)
		http.Error(w, "Something went wrong rendering your dashboard.", http.StatusInternalServerError)
		return
	}

	a.deps.RenderInLayout(w, r, http.StatusOK, "My Characters", template.HTML(body.String()))
}

// guildLabel is the character's guild, or empty when it has none.
//
// Shown per card because it is the one field that makes the guild gate legible
// from the outside: a member refused entry can see at a glance which guild each
// character is actually in, and on which realm.
func guildLabel(c *blizzard.Character) string {
	if c.Guild == nil {
		return ""
	}
	return c.Guild.Name
}

// realmLabel prefers the display name, falling back to the slug.
func realmLabel(c *blizzard.Character) string {
	if c.RealmName != "" {
		return c.RealmName
	}
	return c.RealmSlug
}
