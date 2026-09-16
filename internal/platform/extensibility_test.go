package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// This file is acceptance scenario 6 and FR-011: a new app can be added without
// any change to the login flow, session handling, or another app.
//
// The stub apps here implement nothing but Meta() and Routes(). If making them
// work ever requires editing internal/auth or internal/apps/dashboard, the
// extension point has regressed.

// sessionedCore returns a Core plus a handler where the viewer is treated as a
// signed-in guild member, so gated app routes can be exercised.
func sessionedCore(t *testing.T, member bool, apps ...App) (*Core, http.Handler) {
	t.Helper()
	return sessionedCoreWith(t, member, Config{}, apps...)
}

// sessionedCoreWith is sessionedCore with a Config on the core's Deps.
func sessionedCoreWith(t *testing.T, member bool, cfg Config, apps ...App) (*Core, http.Handler) {
	t.Helper()

	templates, err := LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates() error = %v", err)
	}

	guildName := "TOMB"
	if !member {
		guildName = "Some Other Guild"
	}

	fake := &fakeClient{
		refs: refsFor("Maintank"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return memberOf(ref.Name, guildName), nil
		},
	}

	core := &Core{
		Deps:      Deps{Logger: discardLogger(), Blizzard: fake, Config: cfg},
		Sessions:  &auth.SessionManager{Store: &auth.Store{}},
		Profiles:  newFetcher(fake),
		CSRF:      &CSRF{},
		Templates: templates,
	}
	core.Deps.RenderInLayout = core.RenderInLayout

	// Let stubs render through the shared shell, so navigation and the
	// signed-in header are exercised the way a real app exercises them.
	for _, a := range apps {
		if stub, ok := a.(*stubApp); ok {
			stub.render = core.RenderInLayout
		}
	}

	handler, err := Mount(core, &auth.Handlers{Logger: discardLogger()}, apps)
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	// Inject a session the way the real middleware would, without a database.
	withFakeSession := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = withSession(r, auth.Session{
			User:        auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"},
			AccessToken: "token",
			ExpiresAt:   time.Now().Add(time.Hour),
		})
		handler.ServeHTTP(w, r)
	})

	return core, withFakeSession
}

// TestSecondAppMountsWithNoCoreChanges is scenario 6 proper.
func TestSecondAppMountsWithNoCoreChanges(t *testing.T) {
	first := newStub("dashboard", "My Character", "first-app-body", false)
	second := newStub("roster", "Roster", "second-app-body", false)

	_, handler := sessionedCore(t, true, first, second)

	for _, tc := range []struct{ path, wantBody string }{
		{"/app/dashboard", "first-app-body"},
		{"/app/roster", "second-app-body"},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", tc.path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("GET %s body does not contain %q", tc.path, tc.wantBody)
		}
	}
}

// TestStubAppInheritsGuildGate: a stub declaring RequiresGuild is gated exactly
// like the dashboard, proving gating comes from the core rather than being
// reimplemented per app (contracts/app-registration.md guarantee 3).
func TestStubAppInheritsGuildGate(t *testing.T) {
	tests := []struct {
		name          string
		member        bool
		requiresGuild bool
		wantBody      bool
	}{
		{"member reaches a gated app", true, true, true},
		{"non-member is denied a gated app", false, true, false},
		{"non-member reaches an ungated app", false, false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := newStub("roster", "Roster", "gated-body", tc.requiresGuild)
			_, handler := sessionedCore(t, tc.member, stub)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/roster", nil))

			hasBody := strings.Contains(rec.Body.String(), "gated-body")
			if hasBody != tc.wantBody {
				t.Errorf("handler reached = %v, want %v (status %d)", hasBody, tc.wantBody, rec.Code)
			}

			if !tc.wantBody {
				// The denial must be the FR-013a non-member page.
				if !strings.Contains(rec.Body.String(), "TOMB members only") {
					t.Error("non-member response is not the members-only page")
				}
			}
		})
	}
}

// TestNavigationBuiltFromAppMeta covers guarantee 4.
func TestNavigationBuiltFromAppMeta(t *testing.T) {
	_, handler := sessionedCore(t, true,
		newStub("roster", "Roster", "body", false),
		newStub("dashboard", "My Character", "body", false),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/roster", nil))

	body := rec.Body.String()
	for _, want := range []string{"Roster", "My Character", "/app/roster", "/app/dashboard"} {
		if !strings.Contains(body, want) {
			t.Errorf("navigation is missing %q", want)
		}
	}
}

// TestAppsDoNotTouchAuthOrEachOther is a static check backing scenario 6's
// "without any code changes to the login flow, session handling, or other
// existing apps". It reads the dashboard's source and asserts it imports no
// auth package and no sibling app.
func TestAppsDoNotTouchAuthOrEachOther(t *testing.T) {
	appsDir := filepath.Join("..", "apps")

	entries, err := os.ReadDir(appsDir)
	if err != nil {
		t.Skipf("cannot read %s: %v", appsDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		appName := entry.Name()

		files, err := filepath.Glob(filepath.Join(appsDir, appName, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", appName, err)
		}

		for _, file := range files {
			// The constraint is on an app's production source. A test may
			// legitimately import auth to construct a session.
			if strings.HasSuffix(file, "_test.go") {
				continue
			}

			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			text := string(src)

			if strings.Contains(text, `"github.com/anthony-hopkins/tomb/internal/auth"`) {
				t.Errorf("%s imports tomb/internal/auth; apps must never handle auth themselves", file)
			}

			// An app must not import a sibling app.
			for _, other := range entries {
				if !other.IsDir() || other.Name() == appName {
					continue
				}
				if strings.Contains(text, `"github.com/anthony-hopkins/tomb/internal/apps/`+other.Name()+`"`) {
					t.Errorf("%s imports sibling app %q; cross-app coupling is forbidden",
						file, other.Name())
				}
			}
		}
	}
}

// TestGuildGateRunsBeforeAppHandler proves ordering: a denied request must
// never execute the app's handler at all.
func TestGuildGateRunsBeforeAppHandler(t *testing.T) {
	reached := false
	spy := &stubApp{meta: AppMeta{
		Slug:          "spy",
		NavLabel:      "Spy",
		RoutePrefix:   "/app/spy",
		RequiresGuild: true,
	}}
	// Replace Routes' handler with one that records being reached.
	spyApp := &recordingApp{stubApp: spy, reached: &reached}

	_, handler := sessionedCore(t, false, spyApp)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/spy", nil))

	if reached {
		t.Error("the app handler ran for a non-member; the gate must run first")
	}
}

type recordingApp struct {
	*stubApp
	reached *bool
}

func (r *recordingApp) Routes(reg Registrar) {
	reg.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		*r.reached = true
		_, _ = w.Write([]byte("should not happen"))
	}))
}

// --- Rank, officers, and the officer-only gate (FR-020, FR-021) -----------------

// rankedClient is the extensibility fake plus a roster, so a core built on it
// can resolve the viewer's rank.
type rankedClient struct {
	*fakeClient
	roster []blizzard.GuildMember
}

func (c *rankedClient) GuildRoster(context.Context, string, string, string) ([]blizzard.GuildMember, error) {
	return c.roster, nil
}

// officerCore is sessionedCore with the viewer's one character on the roster
// at the given rank.
func officerCore(t *testing.T, rank int, apps ...App) http.Handler {
	t.Helper()
	return officerCoreWith(t, rank, Config{}, apps...)
}

// officerCoreWith is officerCore with a Config on the core's Deps.
func officerCoreWith(t *testing.T, rank int, cfg Config, apps ...App) http.Handler {
	t.Helper()

	templates, err := LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates() error = %v", err)
	}

	base := &fakeClient{
		refs: refsFor("Maintank"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return memberOf(ref.Name, "TOMB"), nil
		},
	}
	me := memberOf("Maintank", "TOMB")
	fake := &rankedClient{fakeClient: base, roster: []blizzard.GuildMember{
		{Name: "Somebody", RealmSlug: me.RealmSlug, Rank: 0},
		{Name: me.Name, RealmSlug: me.RealmSlug, Rank: rank},
	}}

	guild := GuildConfig{Name: "TOMB", RealmSlug: "area-52", OfficerRank: 1}
	roster := &RosterCache{Client: fake, Guild: guild, Logger: discardLogger()}
	fetcher := &ProfileFetcher{Client: fake, Guild: guild, Logger: discardLogger(), Roster: roster}

	core := &Core{
		Deps:      Deps{Logger: discardLogger(), Blizzard: fake, Roster: roster, Config: cfg},
		Sessions:  &auth.SessionManager{Store: &auth.Store{}},
		Profiles:  fetcher,
		CSRF:      &CSRF{},
		Templates: templates,
	}
	core.Deps.RenderInLayout = core.RenderInLayout
	for _, a := range apps {
		if stub, ok := a.(*stubApp); ok {
			stub.render = core.RenderInLayout
		}
	}

	handler, err := Mount(core, &auth.Handlers{Logger: discardLogger()}, apps)
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	// The same fake session sessionedCore injects.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = withSession(r, auth.Session{
			User:        auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"},
			AccessToken: "token",
			ExpiresAt:   time.Now().Add(time.Hour),
		})
		handler.ServeHTTP(w, r)
	})
}

func officerStub() *stubApp {
	return &stubApp{meta: AppMeta{
		Slug: "logs", NavLabel: "Logs", RoutePrefix: "/app/logs",
		RequiresGuild: true, OfficerOnly: true, NavOrder: 40,
	}, body: "the logs"}
}

// TestOfficerOnlyAppIsHiddenAndRefusedBelowRank: for a rank-5 member the Logs
// entry is not in the navigation and the route answers with the officers page,
// not the members page and not the app.
func TestOfficerOnlyAppIsHiddenAndRefusedBelowRank(t *testing.T) {
	handler := officerCore(t, 5, newStub("dashboard", "My Characters", "dash", true), officerStub())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/dashboard", nil))
	if body := rec.Body.String(); strings.Contains(body, `href="/app/logs"`) {
		t.Error("a rank-5 member can see the Logs entry in the navigation")
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/logs", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("GET /app/logs as rank 5 = %d, want 403", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "the logs") {
		t.Error("the officer-only app rendered for a rank-5 member")
	}
	if !strings.Contains(body, "officers") {
		t.Error("the refusal does not say it is about officers")
	}
	if strings.Contains(body, "TOMB members only") {
		t.Error("a member below rank was told they are not a member")
	}
}

// TestOfficerSeesAndReachesOfficerOnlyApp: rank 1 -- an officer under the
// default threshold -- gets the entry and the page.
func TestOfficerSeesAndReachesOfficerOnlyApp(t *testing.T) {
	for _, rank := range []int{0, 1} {
		handler := officerCore(t, rank, newStub("dashboard", "My Characters", "dash", true), officerStub())

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/dashboard", nil))
		if !strings.Contains(rec.Body.String(), `href="/app/logs"`) {
			t.Errorf("rank %d: the Logs entry is missing from the navigation", rank)
		}

		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/logs", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "the logs") {
			t.Errorf("rank %d: GET /app/logs = %d, want 200 with the app", rank, rec.Code)
		}
	}
}

// TestRankFailsClosedWithoutARoster: a member the roster cannot vouch for is
// not an officer, however senior they may be.
func TestRankFailsClosedWithoutARoster(t *testing.T) {
	_, handler := sessionedCore(t, true, newStub("dashboard", "My Characters", "dash", true), officerStub())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/logs", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("with no roster, GET /app/logs = %d, want 403", rec.Code)
	}
}

// TestNavigationFollowsNavOrder: the bar is ordered by NavOrder, not label.
// "Coming Soon" sorts before "My Characters" alphabetically and must not.
func TestNavigationFollowsNavOrder(t *testing.T) {
	soon := newStub("coming-soon", "Coming Soon", "soon", true)
	soon.meta.NavOrder = 20
	dash := newStub("dashboard", "My Characters", "dash", true)
	dash.meta.NavOrder = 10

	_, handler := sessionedCore(t, true, soon, dash)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/dashboard", nil))
	body := rec.Body.String()
	if strings.Index(body, `href="/app/dashboard"`) > strings.Index(body, `href="/app/coming-soon"`) {
		t.Error("My Characters is listed after Coming Soon; navigation must follow NavOrder")
	}
}

// TestMountRefusesOfficerOnlyWithoutGuild: officer-only means guild-gated, and
// an app that claims one without the other is a mistake worth failing on.
func TestMountRefusesOfficerOnlyWithoutGuild(t *testing.T) {
	bad := &stubApp{meta: AppMeta{Slug: "x", NavLabel: "X", RoutePrefix: "/app/x", OfficerOnly: true}}
	if _, err := Mount(testCore(t), emptyAuthHandlers(), []App{bad}); err == nil {
		t.Error("Mount accepted an officer-only app that is not guild-gated")
	}
}

// --- The administrator (FR-025) ---------------------------------------------

// TestAdministratorPassesEveryGateWhateverTheRank: a rank-5 member configured
// as the administrator sees and reaches the officer-only app; the same member
// unconfigured does not.
func TestAdministratorPassesEveryGateWhateverTheRank(t *testing.T) {
	handler := officerCoreWith(t, 5, Config{Admin: "Tester#1234"},
		newStub("dashboard", "My Characters", "dash", true), officerStub())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/dashboard", nil))
	if !strings.Contains(rec.Body.String(), `href="/app/logs"`) {
		t.Error("the administrator does not see the Logs entry")
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/logs", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "the logs") {
		t.Errorf("the administrator got %d from the officer-only app", rec.Code)
	}

	// Somebody else configured: nothing changes for this rank-5 member.
	handler = officerCoreWith(t, 5, Config{Admin: "Somebody#0001"},
		newStub("dashboard", "My Characters", "dash", true), officerStub())
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/logs", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("a non-administrator at rank 5 got %d, want 403", rec.Code)
	}
}

// TestAdministratorOutsideTheGuildStillGetsIn: the administrator runs the
// site whether or not a character of theirs is in the guild.
func TestAdministratorOutsideTheGuildStillGetsIn(t *testing.T) {
	_, handler := sessionedCoreWith(t, false, Config{Admin: "Tester#1234"},
		newStub("dashboard", "My Characters", "dash", true))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/dashboard", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "dash") {
		t.Errorf("the administrator, not in the guild, got %d", rec.Code)
	}
}

// TestNothingSaysAdministrator: the pages an administrator sees never say so.
// Administration is a fact about running the site, not a standing in the guild,
// and the interface must not conflate the two.
func TestNothingSaysAdministrator(t *testing.T) {
	handler := officerCoreWith(t, 5, Config{Admin: "Tester#1234"},
		newStub("dashboard", "My Characters", "dash", true), officerStub())
	for _, path := range []string{"/app/dashboard", "/app/logs"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if strings.Contains(strings.ToLower(rec.Body.String()), "admin") {
			t.Errorf("%s says \"admin\" somewhere; the administrator must look like anyone else", path)
		}
	}
}

// headlineStub is a stubApp with something to say in the header.
type headlineStub struct {
	*stubApp
	lines []Headline
}

func (h *headlineStub) Headlines(*http.Request) []Headline { return h.lines }

// TestHeadlinesInTheHeader: an app that implements Headliner has its lines
// drawn in the shell of every page the viewer may see them on -- including
// another app's page -- and a viewer who could not reach the app is not
// shown them.
func TestHeadlinesInTheHeader(t *testing.T) {
	tests := []struct {
		name          string
		member        bool
		requiresGuild bool
		officerOnly   bool
		wantTicker    bool
	}{
		{"member sees a gated app's headlines", true, true, false, true},
		{"non-member does not see a gated app's headlines", false, true, false, false},
		{"non-member sees an ungated app's headlines", false, false, false, true},
		{"member below officer does not see an officer-only app's headlines", true, true, true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			talker := newStub("calendar", "Calendar", "calendar-body", tc.requiresGuild)
			talker.meta.OfficerOnly = tc.officerOnly
			loud := &headlineStub{stubApp: talker, lines: []Headline{
				{When: "Today 20:00", Title: "Raid night", Href: "/app/calendar"},
				{When: "Now", Title: "Keys", Href: "/app/calendar", Live: true},
			}}
			// The page under test is another app's. Gated for a member, since
			// a gated page is what carries the profile that vouches for
			// them (every real app is gated); ungated for a non-member, so
			// they still render a page with a header on it.
			other := newStub("dashboard", "My Character", "other-body", tc.member)

			_, handler := sessionedCore(t, tc.member, loud, other)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/dashboard", nil))
			body := rec.Body.String()

			if !strings.Contains(body, "other-body") {
				t.Fatalf("page did not render: %d", rec.Code)
			}
			hasTicker := strings.Contains(body, `class="ticker"`)
			if hasTicker != tc.wantTicker {
				t.Fatalf("ticker shown = %v, want %v", hasTicker, tc.wantTicker)
			}
			if !tc.wantTicker {
				if strings.Contains(body, "Raid night") {
					t.Error("a headline leaked without its ticker")
				}
				return
			}
			for _, want := range []string{"Today 20:00", "Raid night", `<li class="is-live">`, "Keys", `href="/app/calendar"`} {
				if !strings.Contains(body, want) {
					t.Errorf("header is missing %q", want)
				}
			}
			// The ticker sits between the navigation and the sign-out.
			nav := strings.Index(body, `<nav aria-label="Apps">`)
			ticker := strings.Index(body, `class="ticker"`)
			logout := strings.Index(body, `class="logout"`)
			if nav >= ticker || ticker >= logout {
				t.Errorf("header order nav=%d ticker=%d logout=%d, want nav < ticker < logout", nav, ticker, logout)
			}
		})
	}
}
