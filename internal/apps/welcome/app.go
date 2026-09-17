// Package welcome is the front door: the page an anonymous visitor sees at
// "/" (spec 004). Who TOMB is, who runs it, the top of the roster, where the
// Discord is, what TOMB Cares means, and the way in for a member.
//
// Nothing on it needs a viewer. The guild's numbers come from Blizzard with
// the site's own token, held between requests and refreshed in the
// background, so the page is drawn from memory and never waits on Blizzard;
// the first view after a start simply says the numbers are on their way.
package welcome

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/anthony-hopkins/tomb/internal/armory"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

//go:embed templates/welcome.html
var templateFS embed.FS

const routePrefix = "/app/welcome"

const (
	// defaultRefresh is how old the snapshot may be before a view starts a
	// refresh, when TOMB_GUILD_ROSTER_TTL is unset: the same hour as the
	// guild page, for the same data.
	defaultRefresh = time.Hour

	// loadTimeout bounds one roster-and-profiles load.
	loadTimeout = 2 * time.Minute

	// boardSize is how many places each top list shows. Five: the front
	// door is a glance, and the guild page has the ten.
	boardSize = 5

	// maxConcurrentProfileFetches bounds the per-member fan-out, as on the
	// guild page.
	maxConcurrentProfileFetches = 8
)

// App is the front door.
type App struct {
	deps platform.Deps
	tmpl *template.Template

	// tokens mints the site's own token; nil when the Blizzard client cannot
	// (a fake in a test), in which case the page has no numbers to show.
	tokens blizzard.AppTokenSource

	// refreshEvery is how old a snapshot may be before a view refreshes it.
	// Zero -- a bare test construction -- means every view finds it stale.
	refreshEvery time.Duration

	mu      sync.Mutex
	snap    *snapshot
	loading bool
}

var _ platform.App = (*App)(nil)

// snapshot is the roster and every member's detail, as of one refresh.
type snapshot struct {
	members []blizzard.GuildMember
	details map[string]armory.MemberDetail
	// owners is which account a character belongs to, for those who have
	// signed in; keyed as platform.OwnerKey.
	owners  map[string]int64
	fetched time.Time
}

// New builds the app from the dependencies the core lends it.
func New(deps platform.Deps) (*App, error) {
	// Its own page plus the shared armory partials, for the rank mark
	// beside a guild master's or officer's name.
	tmpl, err := template.ParseFS(templateFS, "templates/welcome.html")
	if err != nil {
		return nil, err
	}
	if tmpl, err = tmpl.ParseFS(armory.FS, "templates/armory.html"); err != nil {
		return nil, err
	}
	refresh := deps.Config.GuildRosterTTL
	if refresh <= 0 {
		refresh = defaultRefresh
	}
	a := &App{deps: deps, tmpl: tmpl, refreshEvery: refresh}
	if src, ok := deps.Blizzard.(blizzard.AppTokenSource); ok {
		a.tokens = src
	}
	return a, nil
}

// Meta describes the app to the core (contracts/app-registration.md): public,
// and the page "/" shows an anonymous visitor. No nav entry -- a member has
// the brand link, and this page is for everyone else.
func (a *App) Meta() platform.AppMeta {
	return platform.AppMeta{
		Slug:        "welcome",
		RoutePrefix: routePrefix,
		Public:      true,
		Landing:     true,
	}
}

// Routes registers the page.
func (a *App) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(a.index))
}

// Warm loads the guild's numbers ahead of the first visitor. main runs it
// once at start, off the request path; it returns when the load is done or
// given up. A load already running (a view got there first) is not doubled.
func (a *App) Warm(ctx context.Context) {
	a.refresh(ctx)
}

// view is what the page is drawn from.
type view struct {
	Guild    string
	Realm    string
	Message  string // the core's notice: signed out, authorize again
	SignedIn bool
	Discord  string // the invite link; empty means ask an officer

	// Have says the numbers below are in. Until the first load lands they
	// are not, and the page says so rather than showing an empty guild.
	Have     bool
	Total    int
	AsOf     string
	Officers []officer
	Boards   []armory.Board
}

// officer is one leader on the list: the guild master, or an officer.
type officer struct {
	Name      string
	Realm     string
	Class     string
	ClassSlug string
	Spec      string
	Rank      string
	RankIndex int
	ItemLevel int
	// Others is how many more of this person's characters hold an officer
	// rank, folded into this line (spec 004, amendment).
	Others int
}

// index draws the page from the snapshot in hand, and starts a refresh when
// there is none or it has aged out. It never waits for one.
func (a *App) index(w http.ResponseWriter, r *http.Request) {
	snap := a.current()
	_, signedIn := platform.SessionFrom(r.Context())
	v := view{
		Guild:    a.deps.Guild.Name,
		Realm:    a.deps.Guild.RealmSlug,
		Message:  platform.LandingMessage(r),
		SignedIn: signedIn,
		Discord:  a.deps.Config.DiscordInvite,
	}
	if snap != nil {
		v.Have = true
		v.Total = len(snap.members)
		v.AsOf = armory.LastPlayed(snap.fetched, a.deps.Config.Timezone)
		v.Officers = a.officers(snap)
		v.Boards = armory.Boards(snap.members, snap.details, boardSize)
		if realm := realmOf(snap, a.deps.Guild.RealmSlug); realm != "" {
			v.Realm = realm
		}
	}

	var body bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&body, "welcome.html", v); err != nil {
		a.deps.Logger.Error("render welcome", "error", err)
		http.Error(w, "The page could not be rendered.", http.StatusInternalServerError)
		return
	}
	a.deps.RenderInLayout(w, r, http.StatusOK, "Welcome", template.HTML(body.String()))
}

// officers is the guild master and the officers, in roster order (rank, then
// name), each with what their profile added when it was fetched (FR-045),
// folded to one line per person where the site knows which characters are
// one person's: the character of the best rank, then the highest item
// level, stands for the rest, and says how many more there are. Characters
// of members who have never signed in stay one line each; the roster
// cannot tell their alts apart.
func (a *App) officers(snap *snapshot) []officer {
	var out []officer
	byOwner := map[int64]int{} // user id -> index in out
	for _, m := range snap.members {
		if !a.deps.Guild.IsOfficer(m.Rank) {
			continue
		}
		o := officer{
			Name:      m.Name,
			Realm:     m.RealmLabel(),
			Class:     m.Class,
			Rank:      a.deps.Guild.RankLabel(m.Rank),
			RankIndex: m.Rank,
		}
		if d, ok := snap.details[armory.MemberKey(m)]; ok {
			o.Realm = d.RealmLabel()
			if d.Class != "" {
				o.Class = d.Class
			}
			o.Spec = d.ActiveSpec
			o.ItemLevel = d.AverageItemLevel
		}
		o.ClassSlug = armory.ClassSlug(o.Class)
		if owner, known := snap.owners[platform.OwnerKey(m.RealmSlug, m.Name)]; known {
			if i, have := byOwner[owner]; have {
				if better(o, out[i]) {
					o.Others = out[i].Others + 1
					out[i] = o
				} else {
					out[i].Others++
				}
				continue
			}
			byOwner[owner] = len(out)
		}
		out = append(out, o)
	}
	return out
}

// better says whether a stands for a person ahead of b: the better rank,
// then the higher item level.
func better(a, b officer) bool {
	if a.RankIndex != b.RankIndex {
		return a.RankIndex < b.RankIndex
	}
	return a.ItemLevel > b.ItemLevel
}

// realmOf is the guild's realm as Blizzard displays it, from any member whose
// profile named it; the slug otherwise.
func realmOf(snap *snapshot, slug string) string {
	for _, m := range snap.members {
		if m.RealmSlug != slug {
			continue
		}
		if d, ok := snap.details[armory.MemberKey(m)]; ok && d.RealmName != "" {
			return d.RealmName
		}
		if m.RealmName != "" {
			return m.RealmName
		}
	}
	return ""
}

// current is the snapshot in hand, possibly nil, and starts one refresh in
// the background when it is missing or stale and none is running.
func (a *App) current() *snapshot {
	a.mu.Lock()
	snap := a.snap
	stale := snap == nil || a.refreshEvery <= 0 || time.Since(snap.fetched) >= a.refreshEvery
	start := stale && !a.loading && a.tokens != nil
	if start {
		a.loading = true
	}
	a.mu.Unlock()
	if start {
		go a.load(context.Background())
	}
	return snap
}

// refresh runs one load now unless one is already running.
func (a *App) refresh(ctx context.Context) {
	a.mu.Lock()
	start := !a.loading && a.tokens != nil
	if start {
		a.loading = true
	}
	a.mu.Unlock()
	if start {
		a.load(ctx)
	}
}

// load fetches the roster and every member's detail as the site, and
// replaces the snapshot. A failure keeps the one in hand: a bad minute at
// Blizzard is not a reason to empty the front door. Called with loading set;
// clears it.
func (a *App) load(ctx context.Context) {
	defer func() {
		a.mu.Lock()
		a.loading = false
		a.mu.Unlock()
	}()
	if a.deps.Roster == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), loadTimeout)
	defer cancel()

	token, err := a.tokens.AppToken(ctx)
	if err != nil {
		a.deps.Logger.Warn("front door: no site token", "error", err)
		return
	}
	// The roster comes from the core's cache, which the guild page and the
	// rank check share; the cache has logged a failure already.
	members, err := a.deps.Roster.Members(ctx, token)
	if err != nil {
		return
	}
	b := &armory.Builder{Client: a.deps.Blizzard, Logger: a.deps.Logger, Zone: a.deps.Config.Timezone}
	snap := &snapshot{
		members: members,
		details: b.Details(ctx, token, members, maxConcurrentProfileFetches),
		fetched: time.Now(),
	}
	if a.deps.Owners != nil {
		if owners, err := a.deps.Owners.Owners(ctx); err != nil {
			a.deps.Logger.Warn("front door: character owners", "error", err)
		} else {
			snap.owners = owners
		}
	}
	a.mu.Lock()
	a.snap = snap
	a.mu.Unlock()
}
