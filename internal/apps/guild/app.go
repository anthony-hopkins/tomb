// Package guild is the guild overview: the whole roster, by rank.
//
// This is the site's home for a signed-in member. My Characters answers "what
// am I playing"; this answers "who is TOMB", which is the question a guild site
// exists for.
//
// Only the roster rail is built. The panel beside it is deliberately empty
// until there is something worth putting in it.
package guild

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

//go:embed templates/guild.html
var templateFS embed.FS

// App is the guild overview.
type App struct {
	deps platform.Deps
	tmpl *template.Template
}

var _ platform.App = (*App)(nil)

// New builds the app from the dependencies the core lends it.
func New(deps platform.Deps) (*App, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/guild.html")
	if err != nil {
		return nil, err
	}
	return &App{deps: deps, tmpl: tmpl}, nil
}

// Meta describes the app to the core (contracts/app-registration.md).
func (a *App) Meta() platform.AppMeta {
	return platform.AppMeta{
		Slug:          "guild",
		NavLabel:      "Guild",
		RoutePrefix:   "/app/guild",
		RequiresGuild: true,
	}
}

// Routes registers the app's handlers relative to its own prefix.
func (a *App) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(a.show))
}

// memberView is one character on the roster.
type memberView struct {
	Name  string
	Realm string
	Level int
	Class string
}

// rankGroup is one rank and everybody holding it.
//
// Grouped rather than flat because the grouping is the information: a roster
// sorted by rank with no headings reads as an arbitrary order to anyone who
// does not already know the ranks.
type rankGroup struct {
	Label   string
	Members []memberView
}

type view struct {
	Groups []rankGroup

	// Total is every character on the roster, which is not the same as the
	// number of people: one member with five alts in the guild is five rows.
	Total int

	// Unavailable reports that the roster could not be fetched, so the page can
	// say so rather than rendering an empty guild.
	Unavailable bool

	// RanksUnnamed is true when no rank names are configured, so the page can
	// explain why the headings read "Rank 2" instead of pretending that is
	// normal.
	RanksUnnamed bool
}

func (a *App) show(w http.ResponseWriter, r *http.Request) {
	v := view{RanksUnnamed: len(a.deps.Guild.Ranks) == 0}

	members, err := a.roster(r)
	if err != nil {
		v.Unavailable = true
	} else {
		v.Groups = a.group(members)
		v.Total = len(members)
	}

	var body bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&body, "guild.html", v); err != nil {
		a.deps.Logger.Error("render guild overview", "error", err)
		http.Error(w, "Something went wrong rendering the roster.", http.StatusInternalServerError)
		return
	}

	a.deps.RenderInLayout(w, r, http.StatusOK, "Guild", template.HTML(body.String()))
}

// roster fetches the guild's characters.
//
// One call, however large the guild: the roster is a single endpoint rather
// than a per-character fetch, so this costs the same for thirty members as for
// three hundred.
func (a *App) roster(r *http.Request) ([]blizzard.GuildMember, error) {
	session, ok := platform.SessionFrom(r.Context())
	if !ok {
		return nil, http.ErrNoLocation
	}

	members, err := a.deps.Blizzard.GuildRoster(
		r.Context(), session.AccessToken,
		a.deps.Guild.RealmSlug, a.deps.Guild.Name,
	)
	if err != nil {
		a.deps.Logger.Warn("guild roster unavailable",
			"guild", a.deps.Guild.Name+"@"+a.deps.Guild.RealmSlug,
			"outcome", blizzard.OutcomeOf(err).String(),
		)
		return nil, err
	}
	return members, nil
}

// group turns the sorted roster into rank groups.
//
// The roster arrives already ordered -- by rank, then alphabetically within
// each rank -- so this only has to notice where one rank ends and the next
// begins. Doing the ordering here as well would be two places to get it wrong.
func (a *App) group(members []blizzard.GuildMember) []rankGroup {
	var groups []rankGroup
	current := -1

	for _, m := range members {
		if m.Rank != current {
			current = m.Rank
			groups = append(groups, rankGroup{Label: a.deps.Guild.RankLabel(m.Rank)})
		}
		g := &groups[len(groups)-1]
		g.Members = append(g.Members, memberView{
			Name:  m.Name,
			Realm: realmLabel(m),
			Level: m.Level,
			Class: m.Class,
		})
	}
	return groups
}

func realmLabel(m blizzard.GuildMember) string {
	if m.RealmName != "" {
		return m.RealmName
	}
	return m.RealmSlug
}
