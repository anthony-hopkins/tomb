package warroom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// CurrentZone is the raid the discovery filters to.
func (f *fakeRaid) CurrentZone(context.Context) (wcl.RaidZone, error) {
	return wcl.RaidZone{ID: 53, Name: "The Venomous Abyss"}, nil
}

func asOfficer(r *http.Request) *http.Request {
	p := platform.Profile{Characters: []blizzard.Character{{Name: "Lazzlowe", RealmSlug: "elune", IsCurrent: true}}, Membership: platform.GuildMembership{IsMember: true, IsOfficer: true}}
	ctx := platform.ContextWithSession(r.Context(), auth.Session{User: auth.User{ID: 7, BattleTag: "Tester#1234"}, AccessToken: "tok"})
	return r.WithContext(platform.ContextWithProfile(ctx, p))
}

func post(a *App, path string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.start(rec, asOfficer(r))
	return rec
}

// TestMeta: officers only, in the navigation.
func TestMeta(t *testing.T) {
	a, _ := newApp(t, &fakeRaid{}, &fakeAI{})
	m := a.Meta()
	if !m.OfficerOnly || !m.RequiresGuild || m.NavLabel != "War Room" || m.RoutePrefix != "/app/war-room" {
		t.Errorf("meta = %+v", m)
	}
}

// TestIndexDiscovers: the raid's reports come through the raiders'
// characters, filtered to the current raid and the last two weeks,
// deduplicated, newest first; a character unknown to Warcraft Logs is
// skipped; the reviews are listed.
func TestIndexDiscovers(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	reader := &fakeRaid{recent: []wcl.ReportSummary{
		{Code: "OLDOLDOLDOLDOLD1", Title: "Old", Start: now.Add(-20 * 24 * time.Hour), ZoneID: 53},
		{Code: ourCode, Title: "Tuesday", Start: now.Add(-12 * time.Hour), ZoneID: 53, Owner: "bloodlordz"},
		{Code: "OTHERZONE0000001", Title: "Keys", Start: now.Add(-2 * time.Hour), ZoneID: 99},
		{Code: "EARLIER000000001", Title: "Monday", Start: now.Add(-36 * time.Hour), ZoneID: 53},
	}}
	a, store := newApp(t, reader, &fakeAI{})
	_, _ = store.Create(context.Background(), Review{Code: ourCode, Title: "Tuesday", RequestedBy: "Tester#1234"})
	rec := httptest.NewRecorder()
	a.index(rec, asOfficer(httptest.NewRequest(http.MethodGet, "/app/war-room", nil)))
	body := rec.Body.String()
	for _, want := range []string{"Tuesday", "bloodlordz", "Monday", `name="code" value="` + ourCode + `"`, "running", "/app/war-room/reviews/1", `action="/app/war-room/reviews"`} {
		if !strings.Contains(body, want) {
			t.Errorf("index is missing %q", want)
		}
	}
	for _, leak := range []string{">Old<", "Keys"} {
		if strings.Contains(body, leak) {
			t.Errorf("index shows %q, which is outside the window or the raid", leak)
		}
	}
	if i, j := strings.Index(body, "Tuesday"), strings.Index(body, "Monday"); i > j {
		t.Error("reports are not newest first")
	}
}

// TestStart: a link or code starts a review and lands on it; a bad code
// says so; a review of the same report inside the hold-off is shown
// instead, unless asked again.
func TestStart(t *testing.T) {
	a, store := newApp(t, &fakeRaid{}, &fakeAI{})
	rec := post(a, "/app/war-room/reviews", url.Values{"code": {"https://www.warcraftlogs.com/reports/" + ourCode + "#fight=2"}, "title": {"Tuesday"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/app/war-room/reviews/1" {
		t.Fatalf("start = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rv, _ := store.Get(context.Background(), 1)
	if rv.Code != ourCode || rv.Title != "Tuesday" || rv.RequestedBy != "Tester#1234" || rv.State != Pending {
		t.Errorf("review = %+v", rv)
	}
	if rec := post(a, "/app/war-room/reviews", url.Values{"code": {"nope"}}); rec.Header().Get("Location") != "/app/war-room?e=code" {
		t.Errorf("bad code = %q", rec.Header().Get("Location"))
	}
	if rec := post(a, "/app/war-room/reviews", url.Values{"code": {ourCode}}); rec.Header().Get("Location") != "/app/war-room/reviews/1" {
		t.Errorf("inside the hold-off = %q, want the existing review", rec.Header().Get("Location"))
	}
	if rec := post(a, "/app/war-room/reviews", url.Values{"code": {ourCode}, "again": {"1"}}); rec.Header().Get("Location") != "/app/war-room/reviews/2" {
		t.Errorf("again = %q, want a new review", rec.Header().Get("Location"))
	}
}

// TestShow: pending refreshes itself with the patter; failed says why and
// offers again; done renders the overview, every boss's sections, the
// sides, the facts, the three things first and what to verify.
func TestShow(t *testing.T) {
	reader := &fakeRaid{}
	a, store := newApp(t, reader, &fakeAI{report: goodReport("Nymrissa", "Sentinels")})
	rv, _ := store.Create(context.Background(), Review{Code: ourCode, Title: "Tuesday", RequestedBy: "Tester#1234"})
	get := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/app/war-room/reviews/1", nil)
		r.SetPathValue("id", "1")
		rec := httptest.NewRecorder()
		a.show(rec, asOfficer(r))
		return rec
	}
	rec := get()
	if rec.Header().Get("Refresh") != "5" || !strings.Contains(rec.Body.String(), `class="analysing"`) || !strings.Contains(rec.Body.String(), "Warcraft Logs") {
		t.Errorf("pending page = %d refresh %q", rec.Code, rec.Header().Get("Refresh"))
	}
	a.RunOnce(context.Background())
	rec = get()
	body := rec.Body.String()
	if rec.Header().Get("Refresh") != "" {
		t.Error("a done review still refreshes")
	}
	for _, want := range []string{"<p>Two bosses.</p>", "Nymrissa", "Sentinels", "Sacred Lotus: kill in 4:00", "kill in 5:00, 1 deaths", "wipe in 2:30", "wiped, no kill", "Against the top kill", "<strong>Sentinels</strong>", "<table", "Boomy", "The plan for the next pull", `<ol class="do-first">`, `class="verify-inferred"`, "gemini-test", "150 tokens", `name="again" value="1"`, "the top kill&#39;s 240 s"} {
		if !strings.Contains(body, want) {
			t.Errorf("done page is missing %q", want)
		}
	}
	// Failed: the reason and the way to run again.
	_ = store.Fail(context.Background(), rv.ID, "x")
	rv2, _ := store.Create(context.Background(), Review{Code: "FAILEDREPORT0001"})
	_, _ = store.NextPending(context.Background())
	_ = store.Fail(context.Background(), rv2.ID, "Warcraft Logs is busy")
	r := httptest.NewRequest(http.MethodGet, "/app/war-room/reviews/2", nil)
	r.SetPathValue("id", "2")
	rec = httptest.NewRecorder()
	a.show(rec, asOfficer(r))
	if !strings.Contains(rec.Body.String(), "Warcraft Logs is busy") || !strings.Contains(rec.Body.String(), "Run again") {
		t.Errorf("failed page = %q", rec.Body.String())
	}
	r = httptest.NewRequest(http.MethodGet, "/app/war-room/reviews/99", nil)
	r.SetPathValue("id", "99")
	rec = httptest.NewRecorder()
	a.show(rec, asOfficer(r))
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing review = %d", rec.Code)
	}
}

// TestWithoutReader: a Warcraft Logs client that cannot read raids leaves
// the room saying so, and a start refuses.
func TestWithoutReader(t *testing.T) {
	a, _ := newApp(t, nil, &fakeAI{})
	rec := httptest.NewRecorder()
	a.index(rec, asOfficer(httptest.NewRequest(http.MethodGet, "/app/war-room", nil)))
	if !strings.Contains(rec.Body.String(), "no Warcraft Logs client") {
		t.Errorf("index = %q", rec.Body.String())
	}
	if rec := post(a, "/app/war-room/reviews", url.Values{"code": {ourCode}}); rec.Header().Get("Location") != "/app/war-room?e=unavailable" {
		t.Errorf("start without a reader = %q", rec.Header().Get("Location"))
	}
}
