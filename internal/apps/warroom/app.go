// Package warroom is the officers' room (spec 007): a raid night from
// Warcraft Logs set against the region's fastest kills, boss by boss, role
// by role, with the model writing the officers' report over what the site
// computed. Officers and the administrator only; the core settles that
// before a request arrives.
package warroom

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anthony-hopkins/tomb/internal/ai"
	"github.com/anthony-hopkins/tomb/internal/markup"
	"github.com/anthony-hopkins/tomb/internal/platform"
	"github.com/anthony-hopkins/tomb/internal/raid"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

//go:embed templates/*.html
var templateFS embed.FS

const routePrefix = "/app/war-room"

const (
	// holdOff is how long a done review stands in for a new one of the
	// same report (FR-072).
	holdOff = 6 * time.Hour
	// discoveryTTL is how long the discovered reports are held.
	discoveryTTL = 10 * time.Minute
	// discoveryLookups bounds the raiders asked for their recent reports.
	discoveryLookups = 12
	// discoveryWindow is how far back a discovered report may be.
	discoveryWindow = 14 * 24 * time.Hour
	refreshEvery    = "5"
	listLimit       = 20
)

// App is the War Room.
type App struct {
	deps   platform.Deps
	store  Store
	reader wcl.RaidReader
	tmpl   *template.Template
	now    func() time.Time

	discMu   sync.Mutex
	discAt   time.Time
	discZone int
	disc     []wcl.ReportSummary
}

// New builds the app. The Warcraft Logs client must read raids; a client
// that cannot leaves the room saying so.
func New(deps platform.Deps, store Store) (*App, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse war room templates: %w", err)
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	a := &App{deps: deps, store: store, tmpl: tmpl, now: time.Now}
	if r, ok := deps.WCL.(wcl.RaidReader); ok {
		a.reader = r
	}
	return a, nil
}

// Meta describes the app: officers only.
func (a *App) Meta() platform.AppMeta {
	return platform.AppMeta{
		Slug:          "war-room",
		NavLabel:      "War Room",
		NavOrder:      45, // after Logs
		RoutePrefix:   routePrefix,
		RequiresGuild: true,
		OfficerOnly:   true,
	}
}

// Routes registers the pages and the action.
func (a *App) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(a.index))
	r.Handle("POST /reviews", http.HandlerFunc(a.start))
	r.Handle("GET /reviews/{id}", http.HandlerFunc(a.show))
}

func (a *App) audit(ctx context.Context, userID int64, tag, action, subject, detail string) {
	if a.deps.Audit == nil {
		return
	}
	if err := a.deps.Audit.Record(ctx, platform.AuditEntry{UserID: userID, BattleTag: tag, Action: action, Subject: subject, Detail: detail}); err != nil {
		a.deps.Logger.Error("record audit entry", "action", action, "error", err)
	}
}

// indexView is the room's front page.
type indexView struct {
	Available bool
	Reason    string
	CSRF      string
	Notice    string
	Reports   []reportView
	Reviews   []reviewLine
}

type reportView struct {
	Code  string
	Title string
	When  string
	Owner string
	Zone  string
}

type reviewLine struct {
	ID      int64
	Code    string
	Title   string
	When    string
	By      string
	State   string
	Failure string
	Pending bool
}

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	v := indexView{CSRF: platform.CSRFTokenFrom(r.Context()), Available: a.reader != nil && a.deps.RaidAI != nil}
	switch {
	case a.reader == nil:
		v.Reason = "The site has no Warcraft Logs client configured, so there is nothing to read."
	case a.deps.RaidAI == nil:
		v.Reason = "The site has no model configured, so nothing can write the report."
	}
	v.Notice = a.notice(r.URL.Query().Get("e"))
	if v.Available {
		for _, s := range a.discover(r) {
			v.Reports = append(v.Reports, reportView{Code: s.Code, Title: s.Title, When: a.when(s.Start), Owner: s.Owner, Zone: s.ZoneName})
		}
	}
	if list, err := a.store.List(r.Context(), listLimit); err != nil {
		a.deps.Logger.Error("war room: list", "error", err)
	} else {
		for _, rv := range list {
			v.Reviews = append(v.Reviews, reviewLine{ID: rv.ID, Code: rv.Code, Title: rv.Title, When: a.when(rv.CreatedAt), By: rv.RequestedBy,
				State: rv.State, Failure: rv.Failure, Pending: rv.State == Pending})
		}
	}
	a.render(w, r, "warroom.html", "War Room", v)
}

// discover is the raid's recent reports, through the raiders' characters
// (FR-067), held ten minutes.
func (a *App) discover(r *http.Request) []wcl.ReportSummary {
	a.discMu.Lock()
	defer a.discMu.Unlock()
	if a.now().Sub(a.discAt) < discoveryTTL {
		return a.disc
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	sess, _ := platform.SessionFrom(r.Context())
	var members []struct {
		name, realm string
		rank        int
	}
	if a.deps.Roster != nil {
		if ms, err := a.deps.Roster.Members(ctx, sess.AccessToken); err == nil {
			for _, m := range ms {
				members = append(members, struct {
					name, realm string
					rank        int
				}{m.Name, m.RealmSlug, m.Rank})
			}
		}
	}
	// Officers first: they are the likeliest loggers.
	sort.SliceStable(members, func(i, j int) bool { return members[i].rank < members[j].rank })
	// The viewer's own characters go first of all.
	if p, ok := platform.ProfileFrom(r.Context()); ok {
		for i := len(p.Characters) - 1; i >= 0; i-- {
			c := p.Characters[i]
			members = append([]struct {
				name, realm string
				rank        int
			}{{c.Name, c.RealmSlug, -1}}, members...)
		}
	}
	zone := 0
	if a.deps.WCL != nil {
		if z, err := a.deps.WCL.CurrentZone(ctx); err == nil {
			zone = z.ID
		}
	}
	seen := map[string]bool{}
	var out []wcl.ReportSummary
	region := strings.ToLower(a.deps.Config.BnetRegion)
	since := a.now().Add(-discoveryWindow)
	looked := 0
	for _, m := range members {
		if looked == discoveryLookups {
			break
		}
		key := strings.ToLower(m.realm + "/" + m.name)
		if seen["c:"+key] {
			continue
		}
		seen["c:"+key] = true
		looked++
		reps, err := a.reader.RecentReports(ctx, wcl.CharacterRef{Region: region, Slug: m.realm, Name: m.name}, 8)
		if err != nil {
			continue
		}
		for _, s := range reps {
			if seen[s.Code] || s.Start.Before(since) || (zone != 0 && s.ZoneID != 0 && s.ZoneID != zone) {
				continue
			}
			seen[s.Code] = true
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	if len(out) > 12 {
		out = out[:12]
	}
	a.disc, a.discAt, a.discZone = out, a.now(), zone
	return out
}

// start begins a review of a report, or shows the one already done.
func (a *App) start(w http.ResponseWriter, r *http.Request) {
	if a.deps.CSRF == nil || !a.deps.CSRF.Verify(r) {
		http.Error(w, "The request could not be verified. Reload the page and try again.", http.StatusForbidden)
		return
	}
	sess, ok := platform.SessionFrom(r.Context())
	if !ok {
		http.Redirect(w, r, routePrefix, http.StatusSeeOther)
		return
	}
	code, ok := wcl.ParseReportCode(r.PostFormValue("code"))
	if !ok {
		http.Redirect(w, r, routePrefix+"?e=code", http.StatusSeeOther)
		return
	}
	if a.reader == nil || a.deps.RaidAI == nil {
		http.Redirect(w, r, routePrefix+"?e=unavailable", http.StatusSeeOther)
		return
	}
	if r.PostFormValue("again") != "1" {
		if prev, err := a.store.LatestByCode(r.Context(), code); err == nil && a.now().Sub(prev.CreatedAt) < holdOff {
			http.Redirect(w, r, reviewURL(prev.ID), http.StatusSeeOther)
			return
		}
	}
	rv, err := a.store.Create(r.Context(), Review{UserID: sess.User.ID, RequestedBy: sess.User.BattleTag, Code: code, Title: r.PostFormValue("title")})
	if err != nil {
		a.deps.Logger.Error("war room: create", "error", err)
		http.Redirect(w, r, routePrefix+"?e=failed", http.StatusSeeOther)
		return
	}
	a.audit(r.Context(), sess.User.ID, sess.User.BattleTag, "warroom.review", code, "started")
	http.Redirect(w, r, reviewURL(rv.ID), http.StatusSeeOther)
}

func reviewURL(id int64) string { return routePrefix + "/reviews/" + strconv.FormatInt(id, 10) }

// reviewView is one review's page.
type reviewView struct {
	ID       int64
	Code     string
	Title    string
	Zone     string
	When     string
	By       string
	Link     string
	CSRF     string
	Pending  bool
	Phrases  []string
	Failure  string
	Model    string
	Tokens   int
	Overview template.HTML
	Bosses   []bossView
	DoFirst  []string
	Verify   []ai.VerifyRow
}

type bossView struct {
	Name       string
	Difficulty string
	Pulls      int
	Killed     bool
	Wall       bool
	Ours       string // "4:52, 3 deaths, 19 raiders"
	Top        string // "Sacred Lotus (Area 52): 3:36, 0 deaths, 28 raiders"
	TopNote    string
	Summary    []string
	Sections   []sectionView
}

type sectionView struct {
	Title string
	Body  template.HTML
}

func (a *App) show(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rv, err := a.store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		a.deps.Logger.Error("war room: get", "review", id, "error", err)
		http.Error(w, "The review could not be read.", http.StatusInternalServerError)
		return
	}
	v := reviewView{ID: rv.ID, Code: rv.Code, Title: rv.Title, Zone: rv.Zone, When: a.when(rv.CreatedAt), By: rv.RequestedBy,
		Link: "https://www.warcraftlogs.com/reports/" + rv.Code, CSRF: platform.CSRFTokenFrom(r.Context()), Model: rv.Model, Tokens: rv.PromptTokens + rv.OutputTokens}
	if v.Title == "" {
		v.Title = "Report " + rv.Code
	}
	switch rv.State {
	case Pending:
		v.Pending = true
		v.Phrases = phrases(a.now().Sub(rv.CreatedAt))
		w.Header().Set("Refresh", refreshEvery)
	case Failed:
		v.Failure = rv.Failure
	default:
		a.fill(&v, rv)
	}
	a.render(w, r, "review.html", "War Room: "+v.Title, v)
}

// fill draws the done review from the stored payload and report.
func (a *App) fill(v *reviewView, rv Review) {
	var payload raid.Payload
	_ = json.Unmarshal(rv.Payload, &payload)
	var report ai.RaidReport
	if err := json.Unmarshal(rv.Report, &report); err != nil {
		v.Failure = "the stored report could not be read"
		return
	}
	v.Overview = markup.Render(ai.Clean(report.Overview))
	for _, d := range report.DoTheseFirst {
		v.DoFirst = append(v.DoFirst, ai.Clean(d))
	}
	v.Verify = report.Verify
	byName := map[string]raid.Boss{}
	for _, b := range payload.Bosses {
		byName[b.Name] = b
	}
	for i, br := range report.Bosses {
		bv := bossView{Name: br.Name}
		b, ok := byName[br.Name]
		if !ok && i < len(payload.Bosses) {
			b, ok = payload.Bosses[i], true
		}
		if ok {
			bv.Difficulty, bv.Pulls, bv.Killed, bv.Wall, bv.TopNote = b.Difficulty, len(b.Pulls), b.Killed, b.Wall, b.TopNote
			bv.Ours = sideLine(b.Ours)
			if b.Theirs != nil {
				bv.Top = b.Theirs.Guild + ": " + sideLine(*b.Theirs)
			}
			if b.Diff != nil {
				bv.Summary = b.Diff.Summary
			}
		}
		for _, s := range br.Sections() {
			bv.Sections = append(bv.Sections, sectionView{Title: s.Title, Body: markup.Render(s.Body)})
		}
		v.Bosses = append(v.Bosses, bv)
	}
}

func sideLine(s raid.Side) string {
	outcome := "wipe"
	if s.Kill {
		outcome = "kill"
	}
	return fmt.Sprintf("%s in %d:%02d, %d deaths, %d raiders at %.0f item level", outcome, s.Seconds/60, s.Seconds%60, len(s.Deaths), s.Size, s.ItemLevel)
}

// phrases is the patter while the worker runs, rotated by how long it has.
func phrases(since time.Duration) []string {
	lines := []string{
		"Reading the night's pulls off Warcraft Logs",
		"Finding the fastest kill of each boss in the region",
		"Counting who died to what, and where they stood",
		"Weighing the tanks' intake against the top kill's",
		"Timing the adds",
		"Adding up the dispels nobody got to",
		"Writing the officers' report",
		"Deciding what to change first",
	}
	if since < 0 {
		since = 0
	}
	shift := int(since.Seconds()/4) % len(lines)
	return append(lines[shift:], lines[:shift]...)
}

func (a *App) notice(code string) string {
	switch code {
	case "code":
		return "That is not a Warcraft Logs report link or code."
	case "unavailable":
		return "The War Room is not set up on this site."
	case "failed":
		return "The review could not be started. Try again."
	}
	return ""
}

func (a *App) when(t time.Time) string {
	tz := a.deps.Config.Timezone
	if tz == nil {
		tz = time.UTC
	}
	return t.In(tz).Format("Mon 2 Jan 15:04")
}

func (a *App) render(w http.ResponseWriter, r *http.Request, name, title string, v any) {
	var body bytes.Buffer
	if err := a.tmpl.ExecuteTemplate(&body, name, v); err != nil {
		a.deps.Logger.Error("render war room", "template", name, "error", err)
		http.Error(w, "The page could not be rendered.", http.StatusInternalServerError)
		return
	}
	a.deps.RenderInLayout(w, r, http.StatusOK, title, template.HTML(body.String())) //nolint:gosec // rendered by html/template
}
