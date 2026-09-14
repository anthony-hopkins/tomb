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
	"context"
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

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

const (
	// defaultRefresh is how old the roster snapshot may be before a view
	// triggers a background refresh, when TOMB_GUILD_ROSTER_TTL is unset.
	//
	// An hour, because the data it covers changes on the scale of a day --
	// who is in the guild, what spec they play, when they last logged in --
	// and because refreshing it costs one call per member. See snapshot.
	defaultRefresh = time.Hour

	// loadTimeout bounds one roster-and-profiles load. A guild of two hundred
	// at eight in flight is well under a minute; this is the ceiling for a
	// bad day at Blizzard, not the expectation.
	loadTimeout = 2 * time.Minute

	// maxConcurrentProfileFetches bounds the per-member fan-out, on the same
	// reasoning as the platform's character fan-out: far inside Blizzard's
	// 100/second, and a refresh is not on anybody's request path.
	maxConcurrentProfileFetches = 8
)

// App is the guild overview.
type App struct {
	deps platform.Deps
	tmpl *template.Template

	// refreshEvery is how old a snapshot may be before it is refreshed. Zero
	// -- as in a bare test construction -- means every view finds it stale.
	refreshEvery time.Duration

	mu         sync.Mutex
	snap       *rosterSnapshot
	refreshing bool
	flight     singleflight.Group
}

var _ platform.App = (*App)(nil)

// New builds the app from the dependencies the core lends it.
func New(deps platform.Deps) (*App, error) {
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	refresh := deps.Config.GuildRosterTTL
	if refresh <= 0 {
		refresh = defaultRefresh
	}
	return &App{deps: deps, tmpl: tmpl, refreshEvery: refresh}, nil
}

// rosterSnapshot is the roster and every member's detail, as of one refresh.
// Details is keyed by memberKey and is missing any member whose profile
// Blizzard would not serve; the roster row still stands for them.
type rosterSnapshot struct {
	members []blizzard.GuildMember
	details map[string]memberDetail
	fetched time.Time
}

// memberDetail is what the roster does not carry about a member: their
// profile summary, and their season standing.
type memberDetail struct {
	blizzard.Character
	blizzard.Progress
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

// memberView is one character on the roster, carrying what its card shows.
//
// Name, realm, level, class and rank come from the roster endpoint. The rest
// -- specialisation, item level, when they last played -- is on the
// per-character profile, fetched for every member as part of the snapshot so
// the card reads exactly like the one on My Characters. When a member's
// profile was unavailable those fields are empty and the card omits them
// rather than showing a zero.
type memberView struct {
	Name  string
	Realm string
	Level int
	Class string

	// Guild is the configured guild name, on every card for the same reason
	// My Characters puts it on its cards: the two lists should look the same.
	Guild string

	ActiveSpec       string
	AverageItemLevel int
	LastLogin        string
	MythicPlusRating int
	Raids            []blizzard.RaidProgress

	// ClassSlug paints the name in the class's colour; see armory.ClassSlug.
	ClassSlug string

	// SearchValue is what the search box's suggestion for this member reads:
	// the name alone, or "Name (Realm)" when another member shares the name,
	// so that picking a suggestion always means exactly one character. The
	// server accepts either form.
	SearchValue string

	// Rank is the label of the rank this character holds, repeated onto the
	// member so the card can name it. The heading above the group says it too,
	// but a card that opens over other groups should not make you look up to
	// find out which rank it belongs to. RankIndex is the number behind it,
	// for the authority mark: 0 is the guild master, 1 the officers.
	Rank      string
	RankIndex int

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
// Every number here is counted from the snapshot already in hand. It is the
// whole reason this panel is cheap -- a guild summary that needed its own
// fetches would be a second cost on the page every member lands on.
type summaryView struct {
	Name  string
	Realm string
	Total int

	// AtCap is how many characters sit at the highest level anyone in the guild
	// has reached, and CapLevel is that level. Derived rather than hardcoded:
	// the cap moves every expansion, and a hardcoded 80 would have quietly
	// started counting nothing. CapPct is AtCap as a whole percentage of Total,
	// for the ring meter.
	AtCap    int
	CapLevel int
	CapPct   int

	// Ranks is one entry per rank held, for the count in the stat tile. It is
	// no longer drawn as a chart -- the rail's headings already say who
	// outranks whom, and a bar per rank said it again.
	Ranks []barView

	// Classes is the class chart: each row a label, a count, and the bar's
	// length as a percentage of the longest bar.
	Classes []barView

	// Boards are the leaderboards beside the summary, in the order shown.
	// Absent entirely until the snapshot carries member detail.
	Boards []boardView
}

// barView is one row of a horizontal bar chart.
//
// Pct is relative to the LARGEST row, not to the total, so the longest bar
// always fills the track and the rest read against it. That is what a bar
// chart of counts is for: comparison between rows, not share of the whole.
type barView struct {
	Label string
	Count int
	Pct   int

	// Class is a CSS slug for the row's colour, set only on the class chart:
	// "death-knight", "mage". The label beside the bar is what identifies the
	// row; the colour reinforces it with the hue every player already knows.
	Class string
}

// boardView is one leaderboard: a title and its top entries, best first.
type boardView struct {
	Title   string
	Entries []boardEntry
}

// boardEntry is one placing on a leaderboard.
type boardEntry struct {
	Rank  int
	Name  string
	Key   string // for the link to the member's Armory panel
	Class string // CSS slug; the name is written in the class's colour
	Value string // already formatted: "311", "2431", "3M · 8H"

	// RankIndex is the member's guild rank, for the authority mark beside
	// the name: a guild master or officer stays recognisable on a board
	// where the rank headings of the rail are not there to say so.
	RankIndex int
}

// boardSize is how many places each leaderboard shows. Ten, because five was
// asked for and then found too short: a top ten is the list people expect,
// and the rows are compact enough that three of them still sit beside the
// summary.
const boardSize = 10

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

	// Query is what was typed into the roster search, echoed back into the
	// box. Matches are the members it could mean when it could mean more than
	// one, offered as a list to pick from; a query that means exactly one is
	// never rendered, because it redirects to that member instead.
	Query   string
	Matches []memberView
}

func (a *App) show(w http.ResponseWriter, r *http.Request) {
	v := view{
		RanksUnnamed: len(a.deps.Guild.Ranks) == 0,
		Home:         routePrefix,
	}

	snap, err := a.snapshot(r)
	if err != nil {
		v.Unavailable = true
	} else {
		members := snap.members
		v.Groups = a.group(members, snap.details)
		v.Total = len(members)

		// The search box. A query that names exactly one member becomes a
		// redirect to that member's own URL -- the same ?c= a click produces,
		// so the address bar, the back button and a bookmark all behave as if
		// the name had been clicked. Only an ambiguous or empty result renders.
		if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" && r.URL.Query().Get("c") == "" {
			matches := findMembers(members, q)
			if len(matches) == 1 {
				http.Redirect(w, r, routePrefix+"?c="+url.QueryEscape(memberKey(matches[0])), http.StatusFound)
				return
			}
			v.Query = q
			if len(matches) == 0 {
				v.NotFound = q
			} else {
				v.Matches = a.views(v.Groups, matches)
			}
		}

		if key := r.URL.Query().Get("c"); key != "" {
			a.selectMember(r, &v, members, key)
		}
		// The summary is the default panel, not a permanent one: when a member
		// is on display the page is about them, and showing both would be two
		// things competing for the same column.
		if v.Selected == nil {
			v.Summary = a.summarise(members, v.Groups, snap.details)
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

// snapshot returns the roster and its members' profiles, refreshing them
// without making the viewer wait.
//
// WHY THIS IS KEPT BETWEEN VIEWS WHEN CHARACTER DATA ELSEWHERE IS NOT.
//
// The rail shows each member what My Characters shows: specialisation, item
// level, when they last played. None of that is on the roster endpoint; each
// is on the per-character profile, so a guild of two hundred is two hundred
// calls. My Characters re-pays its fan-out on every view (FR-016) because it
// is your handful of characters. Re-paying two hundred on the page every
// member lands on would be tens of seconds a view and a real share of the
// hourly limit, for data that changes on the scale of a day (FR-018).
//
// So the roster and the profiles are taken together, kept, and refreshed in
// the background once they have aged out: a view after that gets the last
// snapshot at once and quietly starts the next. Only the very first view after
// a start waits, and concurrent first views share that one load. The selected
// member's Armory panel is still fetched live, because that is the thing
// somebody is actually reading.
func (a *App) snapshot(r *http.Request) (*rosterSnapshot, error) {
	session, ok := platform.SessionFrom(r.Context())
	if !ok {
		return nil, http.ErrNoLocation
	}
	token := session.AccessToken

	a.mu.Lock()
	snap := a.snap
	fresh := snap != nil && a.refreshEvery > 0 && time.Since(snap.fetched) < a.refreshEvery
	if snap != nil && !fresh && !a.refreshing {
		a.refreshing = true
		go a.refresh(token)
	}
	a.mu.Unlock()

	if snap != nil {
		return snap, nil
	}

	// Detached from the request's cancellation: several first views may be
	// sharing this load, and the one whose viewer navigated away must not
	// cancel it for the rest.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), loadTimeout)
	defer cancel()

	loaded, err, _ := a.flight.Do("roster", func() (any, error) {
		// Re-check under the lock. A view that found no snapshot a moment ago
		// may reach here after another view's load has already landed and its
		// flight has closed -- singleflight shares a load in progress, not one
		// just finished -- and there is no reason to fetch the roster twice.
		a.mu.Lock()
		if s := a.snap; s != nil {
			a.mu.Unlock()
			return s, nil
		}
		a.mu.Unlock()

		s, err := a.load(ctx, token)
		if err != nil {
			return nil, err
		}
		a.mu.Lock()
		a.snap = s
		a.mu.Unlock()
		return s, nil
	})
	if err != nil {
		return nil, err
	}
	return loaded.(*rosterSnapshot), nil
}

// refresh replaces the snapshot in the background. A failure keeps the one in
// hand: a bad minute at Blizzard is not a reason to empty the front page.
func (a *App) refresh(token string) {
	defer func() {
		a.mu.Lock()
		a.refreshing = false
		a.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()

	s, err := a.load(ctx, token)
	if err != nil {
		return // load already logged it
	}
	a.mu.Lock()
	a.snap = s
	a.mu.Unlock()
}

// load fetches the roster and then every member's profile.
func (a *App) load(ctx context.Context, token string) (*rosterSnapshot, error) {
	members, err := a.deps.Blizzard.GuildRoster(ctx, token, a.deps.Guild.RealmSlug, a.deps.Guild.Name)
	if err != nil {
		a.deps.Logger.Warn("guild roster unavailable",
			"guild", a.deps.Guild.Name+"@"+a.deps.Guild.RealmSlug,
			"outcome", blizzard.OutcomeOf(err).String(),
		)
		return nil, err
	}
	return &rosterSnapshot{
		members: members,
		details: a.details(ctx, token, members),
		fetched: time.Now(),
	}, nil
}

// details fetches each member's profile summary and season standing, bounded
// and in parallel: three calls per member, the profile first and the two
// standing calls together behind it.
//
// A member whose profile Blizzard will not serve -- renamed, transferred, or
// just not right now -- keeps their roster row and loses the detail. That is
// counted and logged once per refresh rather than once per member: at roster
// scale, a line each is a page of warnings nobody reads.
func (a *App) details(ctx context.Context, token string, members []blizzard.GuildMember) map[string]memberDetail {
	fetched := make([]*memberDetail, len(members))
	b := &armory.Builder{Client: a.deps.Blizzard, Logger: a.deps.Logger}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentProfileFetches)
	for i, m := range members {
		g.Go(func() error {
			ref := blizzard.CharacterRef{Name: m.Name, RealmSlug: m.RealmSlug}
			c, err := a.deps.Blizzard.CharacterProfile(gctx, token, ref)
			if err != nil {
				return nil
			}
			fetched[i] = &memberDetail{Character: c, Progress: b.Progress(gctx, token, ref)}
			return nil
		})
	}
	_ = g.Wait()

	out := make(map[string]memberDetail, len(members))
	for i, m := range members {
		if fetched[i] != nil {
			out[memberKey(m)] = *fetched[i]
		}
	}
	if missing := len(members) - len(out); missing > 0 {
		a.deps.Logger.Warn("guild member profiles unavailable",
			"missing", missing, "of", len(members))
	}
	return out
}

// group turns the sorted roster into rank groups, filling each card from the
// member's detail where it was fetched.
//
// The roster arrives already ordered -- by rank, then alphabetically within
// each rank -- so this only has to notice where one rank ends and the next
// begins. Doing the ordering here as well would be two places to get it wrong.
func (a *App) group(members []blizzard.GuildMember, details map[string]memberDetail) []rankGroup {
	var groups []rankGroup
	current := -1

	for _, m := range members {
		if m.Rank != current {
			current = m.Rank
			groups = append(groups, rankGroup{Label: a.deps.Guild.RankLabel(m.Rank)})
		}
		g := &groups[len(groups)-1]

		mv := memberView{
			Name:      m.Name,
			Realm:     m.RealmLabel(),
			Level:     m.Level,
			Class:     m.Class,
			Guild:     a.deps.Guild.Name,
			Rank:      g.Label,
			RankIndex: m.Rank,
			Key:       memberKey(m),
		}
		if d, ok := details[mv.Key]; ok {
			// The profile carries the realm's display name; the roster only
			// its slug. "Area 52", not "area-52", the same as My Characters.
			mv.Realm = d.RealmLabel()
			if d.Class != "" {
				mv.Class = d.Class
			}
			if d.Level > 0 {
				mv.Level = d.Level
			}
			mv.ActiveSpec = d.ActiveSpec
			mv.AverageItemLevel = d.AverageItemLevel
			if d.LastLogin.Unix() > 0 {
				mv.LastLogin = armory.LastPlayed(d.LastLogin)
			}
			mv.MythicPlusRating = d.MythicPlusRating
			mv.Raids = d.Raids
		}
		// After the detail, which may have supplied the class the roster lacked.
		mv.ClassSlug = armory.ClassSlug(mv.Class)
		g.Members = append(g.Members, mv)
	}

	// Namesakes get their realm in the search suggestion, so a pick is never
	// ambiguous. Everyone else is just their name: the common case should
	// read as a name, not as a name with paperwork attached.
	shared := map[string]int{}
	for _, m := range members {
		shared[strings.ToLower(m.Name)]++
	}
	for gi := range groups {
		for mi := range groups[gi].Members {
			mv := &groups[gi].Members[mi]
			mv.SearchValue = mv.Name
			if shared[strings.ToLower(mv.Name)] > 1 {
				mv.SearchValue = mv.Name + " (" + mv.Realm + ")"
			}
		}
	}
	return groups
}

// summarise counts what the roster adds up to.
//
// Rank counts come from the groups rather than being recounted: they are the
// same numbers, and counting them twice is how the rail and the panel end up
// disagreeing with each other.
func (a *App) summarise(members []blizzard.GuildMember, groups []rankGroup, details map[string]memberDetail) *summaryView {
	if len(members) == 0 {
		return nil
	}

	s := &summaryView{
		Name:  a.deps.Guild.Name,
		Realm: a.deps.Guild.RealmSlug,
		Total: len(members),
	}

	for _, g := range groups {
		s.Ranks = append(s.Ranks, barView{Label: g.Label, Count: len(g.Members)})
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
		s.Classes = append(s.Classes, barView{Label: name, Count: n, Class: armory.ClassSlug(name)})
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

	scaleBars(s.Classes)
	if s.Total > 0 {
		s.CapPct = (s.AtCap*100 + s.Total/2) / s.Total
	}
	s.Boards = a.boards(members, details)

	return s
}

// scaleBars sets each bar's length as a percentage of the longest.
func scaleBars(bars []barView) {
	longest := 0
	for _, b := range bars {
		longest = max(longest, b.Count)
	}
	if longest == 0 {
		return
	}
	for i := range bars {
		bars[i].Pct = (bars[i].Count*100 + longest/2) / longest
	}
}

// contender is one member with what the boards rank them on, pulled together
// once so each board is a sort and a slice rather than a fresh walk.
type contender struct {
	member blizzard.GuildMember
	detail memberDetail

	// Bosses down at each difficulty, summed across the current raids. The
	// boards prize mythic over heroic over normal, so these are compared in
	// that order rather than added together: three mythic kills outrank eight
	// heroic ones, which is how players themselves would rank them.
	mythic, heroic, normal int
}

// boards builds the leaderboards from the snapshot's member detail.
//
// A member with nothing to rank on -- no item level fetched, no rating, no
// kills -- is simply absent from that board rather than placed last with a
// zero. A board nobody qualifies for is left out altogether, which is what
// happens on a fresh snapshot before any detail has arrived.
func (a *App) boards(members []blizzard.GuildMember, details map[string]memberDetail) []boardView {
	var cs []contender
	for _, m := range members {
		d, ok := details[memberKey(m)]
		if !ok {
			continue
		}
		c := contender{member: m, detail: d}
		for _, r := range d.Raids {
			for _, mode := range r.Modes {
				switch mode.Difficulty {
				case "MYTHIC":
					c.mythic += mode.Completed
				case "HEROIC":
					c.heroic += mode.Completed
				case "NORMAL":
					c.normal += mode.Completed
				}
			}
		}
		cs = append(cs, c)
	}

	var out []boardView
	add := func(title string, keep func(contender) bool, less func(x, y contender) bool, value func(contender) string) {
		var pool []contender
		for _, c := range cs {
			if keep(c) {
				pool = append(pool, c)
			}
		}
		if len(pool) == 0 {
			return
		}
		// Best first; ties break on name so the board is stable between
		// refreshes rather than shuffling two equal characters.
		sort.SliceStable(pool, func(i, j int) bool {
			if less(pool[j], pool[i]) {
				return true
			}
			if less(pool[i], pool[j]) {
				return false
			}
			return strings.ToLower(pool[i].member.Name) < strings.ToLower(pool[j].member.Name)
		})
		b := boardView{Title: title}
		for i, c := range pool[:min(boardSize, len(pool))] {
			b.Entries = append(b.Entries, boardEntry{
				Rank:      i + 1,
				Name:      c.member.Name,
				Key:       memberKey(c.member),
				Class:     armory.ClassSlug(c.detail.Class),
				Value:     value(c),
				RankIndex: c.member.Rank,
			})
		}
		out = append(out, b)
	}

	add("Top item level",
		func(c contender) bool { return c.detail.AverageItemLevel > 0 },
		func(x, y contender) bool { return x.detail.AverageItemLevel < y.detail.AverageItemLevel },
		func(c contender) string { return strconv.Itoa(c.detail.AverageItemLevel) },
	)
	add("Top Mythic+ rating",
		func(c contender) bool { return c.detail.MythicPlusRating > 0 },
		func(x, y contender) bool { return x.detail.MythicPlusRating < y.detail.MythicPlusRating },
		func(c contender) string { return strconv.Itoa(c.detail.MythicPlusRating) },
	)
	add("Most raid bosses down",
		func(c contender) bool { return c.mythic+c.heroic+c.normal > 0 },
		func(x, y contender) bool {
			if x.mythic != y.mythic {
				return x.mythic < y.mythic
			}
			if x.heroic != y.heroic {
				return x.heroic < y.heroic
			}
			return x.normal < y.normal
		},
		func(c contender) string { return bossesLabel(c) },
	)
	return out
}

// bossesLabel is the compact form a leaderboard row has room for: the two
// hardest difficulties with any kills, hardest first -- "3M · 8H".
func bossesLabel(c contender) string {
	var parts []string
	for _, t := range []struct {
		n     int
		short string
	}{{c.mythic, "M"}, {c.heroic, "H"}, {c.normal, "N"}} {
		if t.n > 0 && len(parts) < 2 {
			parts = append(parts, strconv.Itoa(t.n)+t.short)
		}
	}
	return strings.Join(parts, " · ")
}

// findMembers resolves what was typed into the search box to roster members.
//
// Exact name first, then names that begin with what was typed, both without
// regard to case: "laz" finds Lazzlowe if nobody is called Laz, and finds
// every Laz-something if several are. A realm can follow the name to tell
// namesakes apart -- "Cwds elune", "Cwds (Elune)", "cwds/elune" all work --
// because a character name is only unique within a realm and a guild this
// size has namesakes.
//
// Nothing fuzzier than a prefix, on purpose. The datalist behind the box
// already completes any name on the roster as it is typed, so the server's
// job is to accept what the box produced and forgive a partial, not to guess.
func findMembers(members []blizzard.GuildMember, query string) []blizzard.GuildMember {
	name, realm := splitQuery(query)

	var exact, prefix []blizzard.GuildMember
	for _, m := range members {
		if realm != "" && !strings.EqualFold(m.RealmSlug, realm) && !strings.EqualFold(m.RealmLabel(), realm) {
			continue
		}
		switch {
		case strings.EqualFold(m.Name, name):
			exact = append(exact, m)
		case strings.HasPrefix(strings.ToLower(m.Name), strings.ToLower(name)):
			prefix = append(prefix, m)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return prefix
}

// splitQuery separates an optional realm from the name: "Cwds elune",
// "Cwds (Elune)", "cwds/elune" and "Cwds" alone.
func splitQuery(q string) (name, realm string) {
	q = strings.NewReplacer("(", " ", ")", " ", "/", " ", ",", " ").Replace(q)
	fields := strings.Fields(q)
	switch len(fields) {
	case 0:
		return "", ""
	case 1:
		return fields[0], ""
	default:
		return fields[0], strings.Join(fields[1:], " ")
	}
}

// views picks the rows already built for the rail that correspond to the
// given members, so a list of matches is drawn exactly as the rail draws them
// -- class colour, authority mark, and all.
func (a *App) views(groups []rankGroup, members []blizzard.GuildMember) []memberView {
	want := make(map[string]bool, len(members))
	for _, m := range members {
		want[memberKey(m)] = true
	}
	var out []memberView
	for _, g := range groups {
		for _, mv := range g.Members {
			if want[mv.Key] {
				out = append(out, mv)
			}
		}
	}
	return out
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
