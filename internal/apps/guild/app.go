// Package guild is the guild overview: the whole roster, by rank.
//
// This is the site's home for a signed-in member. My Characters answers "what
// am I playing"; this answers "who is TOMB", which is the question a guild site
// exists for.
//
// The rail lists every member; the panel beside it summarises the guild until a
// name is clicked, and shows that member's Armory view after. The panel is the
// same one My Characters renders, from internal/armory.
package guild

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"sort"
	"strings"

	"github.com/anthony-hopkins/tomb/internal/armory"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

//go:embed templates/guild.html
var templateFS embed.FS

// routePrefix is where this app is mounted. Declared once because two things
// need it: the registration metadata, and the back-link out of a member's
// Armory panel. A relative href cannot do that job -- from /app/guild?c=x a
// bare "." resolves to /app/, not back to the roster.
const routePrefix = "/app/guild"

// App is the guild overview.
type App struct {
	deps platform.Deps
	tmpl *template.Template
}

var _ platform.App = (*App)(nil)

// New builds the app from the dependencies the core lends it.
func New(deps platform.Deps) (*App, error) {
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return &App{deps: deps, tmpl: tmpl}, nil
}

// parseTemplates builds this app's template set.
//
// Its own page plus the shared Armory panel, which My Characters renders too --
// so a roster member and one of your own characters are displayed by literally
// the same markup. Factored out because the tests need the same set: a test
// that parsed only guild.html would render a template the app never uses, and
// would have gone on passing while the real page failed on a missing partial.
func parseTemplates() (*template.Template, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/guild.html")
	if err != nil {
		return nil, err
	}
	return tmpl.ParseFS(armory.FS, "templates/armory.html")
}

// Meta describes the app to the core (contracts/app-registration.md).
func (a *App) Meta() platform.AppMeta {
	return platform.AppMeta{
		Slug: "guild",
		// No nav entry. This is what the TOMB brand link leads to, so listing
		// it beside that link would be the same destination twice.
		NavLabel:      "",
		Home:          true,
		RoutePrefix:   routePrefix,
		RequiresGuild: true,
	}
}

// Routes registers the app's handlers relative to its own prefix.
func (a *App) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(a.show))
}

// memberView is one character on the roster.
//
// Everything here comes from the roster endpoint. There is deliberately no item
// level, specialisation or last-played time: those live on the per-character
// profile, which would be one call per member on every view -- a hundred calls
// for a hundred-member guild, re-paid each time somebody opens the page. The
// card shows what a roster knows.
type memberView struct {
	Name  string
	Realm string
	Level int
	Class string

	// Rank is the label of the rank this character holds, repeated onto the
	// member so the card can name it. The heading above the group says it too,
	// but a card that opens over other groups should not make you look up to
	// find out which rank it belongs to.
	Rank string

	// Key identifies this character in a URL, so the name on its card can link
	// to its own Armory panel -- the same affordance, and the same ?c= shape,
	// that My Characters uses. Raw, not escaped: html/template knows it lands
	// in a URL query and escapes it correctly there.
	Key string

	// Selected marks the member the Armory panel is currently showing.
	Selected bool
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

// summaryView is the panel beside the rail: what the roster adds up to.
//
// Every number here is counted from the roster already in hand. It is the whole
// reason this panel is cheap -- a guild summary that needed its own fetches
// would be a second cost on the page every member lands on.
type summaryView struct {
	Name  string
	Realm string
	Total int

	// AtCap is how many characters sit at the highest level anyone in the guild
	// has reached, and CapLevel is that level. Derived rather than hardcoded:
	// the cap moves every expansion, and a hardcoded 80 would have quietly
	// started counting nothing.
	AtCap    int
	CapLevel int

	Ranks   []countView
	Classes []countView
}

// countView is one label and how many characters carry it.
type countView struct {
	Label string
	Count int
}

type view struct {
	Groups []rankGroup

	// Summary is the panel beside the rail, shown when no particular member is
	// selected. It is what makes the landing page free: every number in it is
	// counted from the roster already in hand.
	Summary *summaryView

	// Selected is the member whose Armory panel is on display, set only when
	// the URL asks for one. Absent by default, and deliberately so: this is the
	// page every member lands on, and rendering somebody's gear on arrival
	// would put three Blizzard calls on the front door for every visit.
	Selected *armory.Panel

	// NotFound is the character the URL asked for when nobody by that name is
	// on the roster, so the page can say so instead of silently showing the
	// summary and looking like the link was ignored.
	NotFound string

	// Unreachable is the name of a roster member whose profile Blizzard would
	// not return. Distinct from NotFound because the two have different causes
	// and different advice: one is a stale link, the other is a bad minute.
	Unreachable string

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

	// Home is this app's own path, for the link back out of a member's panel.
	Home string
}

func (a *App) show(w http.ResponseWriter, r *http.Request) {
	v := view{
		RanksUnnamed: len(a.deps.Guild.Ranks) == 0,
		Home:         routePrefix,
	}

	members, err := a.roster(r)
	if err != nil {
		v.Unavailable = true
	} else {
		v.Groups = a.group(members)
		v.Total = len(members)

		if key := r.URL.Query().Get("c"); key != "" {
			a.selectMember(r, &v, members, key)
		}
		// The summary is the default panel, not a permanent one: when a member
		// is on display the page is about them, and showing both would be two
		// things competing for the same column.
		if v.Selected == nil {
			v.Summary = a.summarise(members, v.Groups)
		}
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
			Realm: m.RealmLabel(),
			Level: m.Level,
			Class: m.Class,
			Rank:  g.Label,
			Key:   memberKey(m),
		})
	}
	return groups
}

// summarise counts what the roster adds up to.
//
// Rank counts come from the groups rather than being recounted: they are the
// same numbers, and counting them twice is how the rail and the panel end up
// disagreeing with each other.
func (a *App) summarise(members []blizzard.GuildMember, groups []rankGroup) *summaryView {
	if len(members) == 0 {
		return nil
	}

	s := &summaryView{
		Name:  a.deps.Guild.Name,
		Realm: a.deps.Guild.RealmSlug,
		Total: len(members),
	}

	for _, g := range groups {
		s.Ranks = append(s.Ranks, countView{Label: g.Label, Count: len(g.Members)})
	}

	classes := map[string]int{}
	for _, m := range members {
		switch {
		case m.Level > s.CapLevel:
			// A new high water mark: everyone counted so far was below it, so
			// the count starts again at this one.
			s.CapLevel, s.AtCap = m.Level, 1
		case m.Level == s.CapLevel:
			s.AtCap++
		}
		if m.Class != "" {
			classes[m.Class]++
		}
	}

	for name, n := range classes {
		s.Classes = append(s.Classes, countView{Label: name, Count: n})
	}
	// Commonest first, then alphabetically so equal counts do not shuffle
	// between page loads -- map iteration order is random, and a list that
	// reorders itself on refresh looks broken.
	sort.SliceStable(s.Classes, func(i, j int) bool {
		if s.Classes[i].Count != s.Classes[j].Count {
			return s.Classes[i].Count > s.Classes[j].Count
		}
		return s.Classes[i].Label < s.Classes[j].Label
	})

	return s
}

// memberKey identifies a roster member in a URL. Realm first, because a
// character name is only unique within one realm -- the same shape My
// Characters uses, so the two lists behave identically.
func memberKey(m blizzard.GuildMember) string {
	return m.RealmSlug + "/" + strings.ToLower(m.Name)
}

// selectMember resolves ?c= to a roster member and builds their Armory panel.
//
// THE ROSTER IS THE ALLOW-LIST, AND THAT IS THE POINT.
//
// Blizzard will happily return a profile for any character in the region, so a
// handler that passed ?c= straight through would turn this page into an open
// proxy for the character API -- anyone could walk arbitrary names through it,
// spending OUR rate limit, from a URL that looks like part of the guild site.
// Resolving against the roster we already fetched costs nothing and means the
// only characters this page will ever fetch are the ones it already lists.
//
// An unknown key says so rather than falling back to the summary. My Characters
// falls back on purpose -- a bookmark to a character you deleted should show
// you your main -- but here the same silence would look like a broken link,
// because the name came from a list the viewer is looking at.
func (a *App) selectMember(r *http.Request, v *view, members []blizzard.GuildMember, key string) {
	session, ok := platform.SessionFrom(r.Context())
	if !ok {
		return
	}

	idx := -1
	for i := range members {
		if strings.EqualFold(memberKey(members[i]), key) {
			idx = i
			break
		}
	}
	if idx < 0 {
		v.NotFound = key
		return
	}
	m := members[idx]

	b := &armory.Builder{Client: a.deps.Blizzard, Logger: a.deps.Logger}
	panel, err := b.For(
		r.Context(), session.AccessToken,
		blizzard.CharacterRef{Name: m.Name, RealmSlug: m.RealmSlug},
		a.deps.Guild.RankLabel(m.Rank),
	)
	if err != nil {
		v.Unreachable = m.Name
		return
	}
	v.Selected = panel

	// Mark the row so the rail shows which member the panel belongs to, the
	// way My Characters marks the selected character.
	for gi := range v.Groups {
		for mi := range v.Groups[gi].Members {
			if v.Groups[gi].Members[mi].Key == memberKey(m) {
				v.Groups[gi].Members[mi].Selected = true
			}
		}
	}
}
