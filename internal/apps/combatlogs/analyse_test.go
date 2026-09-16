package combatlogs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// fakeWCL answers BestRank from a table, and counts.
type fakeWCL struct {
	rank  wcl.Ranking
	err   error
	calls int
}

func (f *fakeWCL) BestRank(context.Context, wcl.CharacterRef, int, int, string) (wcl.Ranking, error) {
	f.calls++
	return f.rank, f.err
}

var topTank = wcl.Ranking{Name: "Toptank", ClassID: 1, Spec: "Blood", Metric: "dps", RankPercent: 97.3, Amount: 1498220, Duration: 312 * time.Second,
	Gear:    []wcl.Gear{{ID: 212345, Name: "Baleful Grave-Knight's Casque", ItemLevel: 320}, {ID: 999, Name: "Pendant of Malefic Fury", ItemLevel: 324}},
	Talents: []wcl.Talent{{ID: 1, Name: "Marrowrend"}, {ID: 2, Name: "Consumption"}}}

// seeded parses the synthetic night into the store and returns the app and
// Nekromoo's Vexie summary.
func seeded(t *testing.T, store *fights.MemStore, audit *memAudit, w *fakeWCL) (*App, fights.Summary) {
	t.Helper()
	a := newApp(t, store, audit, true)
	if w != nil {
		a.deps.WCL = w
	}
	queued(t, a, store, gz(t, fixture(t)))
	if !a.ParseOnce(context.Background()) {
		t.Fatal("nothing parsed")
	}
	sums, _ := store.SummariesForCharacter(context.Background(), 1, "Nekromoo", "area-52")
	for _, sm := range sums {
		if sm.Fight.EncounterID == 3009 {
			return a, sm
		}
	}
	t.Fatal("no Vexie summary")
	return nil, fights.Summary{}
}

func postAnalyse(a *App, officer bool, summaryID int64, link string) *httptest.ResponseRecorder {
	form := url.Values{"summary": {itoa(summaryID)}, "link": {link}}
	r := httptest.NewRequest(http.MethodPost, "/app/combatlogs/analyses", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := platform.ContextWithSession(r.Context(), auth.Session{User: auth.User{ID: 1, BattleTag: "Lazzloe#1149"}, AccessToken: "t"})
	ctx = platform.ContextWithProfile(ctx, platform.Profile{
		Characters: []blizzard.Character{{Name: "Nekromoo", RealmSlug: "area-52"}},
		Membership: platform.GuildMembership{IsMember: true, IsOfficer: officer},
	})
	rec := httptest.NewRecorder()
	a.analyse(rec, r.WithContext(ctx))
	return rec
}

func location(rec *httptest.ResponseRecorder) (path string, q url.Values) {
	u, _ := url.Parse(rec.Header().Get("Location"))
	return u.Path, u.Query()
}

const goodLink = "https://www.warcraftlogs.com/character/us/area-52/toptank"

// TestAnalyseRoute walks every outcome of the form (FR-034, FR-035, FR-039,
// FR-041): each refusal creates nothing and spends no allowance.
func TestAnalyseRoute(t *testing.T) {
	tests := []struct {
		name     string
		wcl      *fakeWCL // nil means no client configured
		link     string
		officer  bool
		dungeon  bool
		wantMsg  string
		wantRows int
	}{
		{"success", &fakeWCL{rank: topTank}, goodLink, false, false, "", 1},
		{"bad link", &fakeWCL{rank: topTank}, "https://www.warcraftlogs.com/reports/abc", false, false, "badlink", 0},
		{"not a raid", &fakeWCL{rank: topTank}, goodLink, false, true, "notraid", 0},
		{"unknown character", &fakeWCL{err: wcl.ErrNoCharacter}, goodLink, false, false, "nochar", 0},
		{"no rank", &fakeWCL{err: wcl.ErrNoRank}, goodLink, false, false, "norank", 0},
		{"warcraft logs down", &fakeWCL{err: errors.New("boom")}, goodLink, false, false, "unavailable", 0},
		{"no client configured", nil, goodLink, false, false, "unavailable", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := fights.NewMemStore()
			a, sm := seeded(t, store, &memAudit{}, tc.wcl)
			if tc.wcl == nil {
				a.deps.WCL = nil
			}
			if tc.dungeon {
				// Turn the fight into a dungeon pull.
				for _, f := range store.Fights {
					if f.ID == sm.FightID {
						f.DifficultyID = 8
					}
				}
			}
			rec := postAnalyse(a, tc.officer, sm.ID, tc.link)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("code = %d", rec.Code)
			}
			path, q := location(rec)
			if path != "/app/dashboard" || q.Get("c") != "area-52/nekromoo" || q.Get("msg") != tc.wantMsg {
				t.Errorf("redirect = %s?%s, want msg %q", path, q.Encode(), tc.wantMsg)
			}
			if len(store.Analyses) != tc.wantRows {
				t.Errorf("analyses = %d, want %d", len(store.Analyses), tc.wantRows)
			}
		})
	}
}

// TestAnalyseAllowanceAndCache: a second run inside the window is refused
// with the minutes for a member and allowed for an officer; the player is
// fetched once a day.
func TestAnalyseAllowanceAndCache(t *testing.T) {
	store := fights.NewMemStore()
	store.Now = func() time.Time { return clock }
	w := &fakeWCL{rank: topTank}
	a, sm := seeded(t, store, &memAudit{}, w)

	if _, q := location(postAnalyse(a, false, sm.ID, goodLink)); q.Get("msg") != "" {
		t.Fatalf("first run refused: %s", q.Encode())
	}
	_, q := location(postAnalyse(a, false, sm.ID, goodLink))
	if q.Get("msg") != "wait" || q.Get("min") != "120" {
		t.Errorf("second run = %s, want wait 120", q.Encode())
	}
	if _, q := location(postAnalyse(a, true, sm.ID, goodLink)); q.Get("msg") != "" {
		t.Errorf("officer refused: %s", q.Encode())
	}
	if w.calls != 1 {
		t.Errorf("warcraft logs fetched %d times, want 1 (cached)", w.calls)
	}
	if len(store.Comparisons) != 1 {
		t.Errorf("comparison players = %d, want 1", len(store.Comparisons))
	}

	// A day later the player is fetched again.
	a.now = func() time.Time { return clock.Add(25 * time.Hour) }
	store.Now = a.now
	postAnalyse(a, true, sm.ID, goodLink)
	if w.calls != 2 {
		t.Errorf("warcraft logs fetched %d times after a day, want 2", w.calls)
	}

	// Another member's summary is a 404.
	r := httptest.NewRequest(http.MethodPost, "/app/combatlogs/analyses", strings.NewReader(url.Values{"summary": {itoa(sm.ID)}, "link": {goodLink}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.analyse(rec, as(r, 2))
	if rec.Code != http.StatusNotFound {
		t.Errorf("another member = %d", rec.Code)
	}
}
