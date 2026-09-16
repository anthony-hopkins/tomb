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

// fakeWCL answers the two reads from a table, and counts.
type fakeWCL struct {
	top      wcl.Ranking
	topRef   wcl.CharacterRef
	topErr   error
	rank     wcl.Ranking
	rankErr  error
	tops     int
	ranks    int
	lastSpec string
}

func (f *fakeWCL) BestRank(context.Context, wcl.CharacterRef, int, int, string) (wcl.Ranking, error) {
	f.ranks++
	return f.rank, f.rankErr
}

func (f *fakeWCL) TopPlayer(_ context.Context, _, _ int, class, spec, _ string) (wcl.CharacterRef, wcl.Ranking, error) {
	f.tops++
	f.lastSpec = class + "/" + spec
	return f.topRef, f.top, f.topErr
}

var topTank = wcl.Ranking{Name: "Toptank", Class: "Death Knight", Spec: "Blood", Metric: "dps", RankPercent: 100, Amount: 1498220, Duration: 312 * time.Second,
	Gear:    []wcl.Gear{{ID: 212345, Name: "Baleful Grave-Knight's Casque", ItemLevel: 320}, {ID: 999, Name: "Pendant of Malefic Fury", ItemLevel: 324}},
	Talents: []wcl.Talent{{ID: 1, Name: "Marrowrend"}, {ID: 2, Name: "Consumption"}}}

var topRef = wcl.CharacterRef{Region: "us", Slug: "area-52", Name: "Toptank"}

func healthyWCL() *fakeWCL {
	return &fakeWCL{top: topTank, topRef: topRef, rank: wcl.Ranking{Name: "Toptank", Class: "Death Knight", Spec: "Blood", Metric: "dps", RankPercent: 96, Amount: 1400000, Duration: 200 * time.Second}}
}

// seeded parses the synthetic night into the store and returns the app and
// the upload.
func seeded(t *testing.T, store *fights.MemStore, audit *memAudit, w *fakeWCL) (*App, fights.Upload) {
	t.Helper()
	a := newApp(t, store, audit, true)
	if w != nil {
		a.deps.WCL = w
	}
	u := queued(t, a, store, gz(t, fixture(t)))
	if !a.ParseOnce(context.Background()) {
		t.Fatal("nothing parsed")
	}
	return a, u
}

func postAnalyse(a *App, officer bool, uploadID int64, character string) *httptest.ResponseRecorder {
	form := url.Values{"upload": {itoa(uploadID)}, "character": {character}}
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

const nekromoo = "area-52/nekromoo"

// TestAnalyseRoute walks every outcome of the form (FR-035, FR-039,
// FR-041): each refusal creates nothing and spends no allowance.
func TestAnalyseRoute(t *testing.T) {
	tests := []struct {
		name     string
		wcl      *fakeWCL // nil means no client configured
		mangle   func(store *fights.MemStore)
		wantMsg  string
		wantRows int
	}{
		{"success", healthyWCL(), nil, "", 1},
		{"no raid pulls", healthyWCL(), func(s *fights.MemStore) {
			for _, f := range s.Fights {
				f.DifficultyID = 8
			}
		}, "nopulls", 0},
		{"no spec recorded", healthyWCL(), func(s *fights.MemStore) {
			for _, f := range s.Fights {
				for i := range f.Summaries {
					f.Summaries[i].SpecID = 0
				}
			}
		}, "nospec", 0},
		{"nobody ranked", &fakeWCL{topErr: wcl.ErrNoRank}, nil, "norank", 0},
		{"warcraft logs down", &fakeWCL{topErr: errors.New("boom")}, nil, "unavailable", 0},
		{"no client configured", nil, nil, "unavailable", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := fights.NewMemStore()
			a, u := seeded(t, store, &memAudit{}, tc.wcl)
			if tc.wcl == nil {
				a.deps.WCL = nil
			}
			if tc.mangle != nil {
				tc.mangle(store)
			}
			rec := postAnalyse(a, false, u.ID, nekromoo)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("code = %d", rec.Code)
			}
			path, q := location(rec)
			if path != "/app/dashboard" || q.Get("c") != nekromoo || q.Get("msg") != tc.wantMsg {
				t.Errorf("redirect = %s?%s, want msg %q", path, q.Encode(), tc.wantMsg)
			}
			if len(store.Analyses) != tc.wantRows {
				t.Errorf("analyses = %d, want %d", len(store.Analyses), tc.wantRows)
			}
			if tc.wantRows == 1 {
				if tc.wcl.lastSpec != "DeathKnight/Blood" {
					t.Errorf("top player looked up for %q, want DeathKnight/Blood", tc.wcl.lastSpec)
				}
				for _, an := range store.Analyses {
					if an.UploadID != u.ID || an.Name != "Nekromoo" || an.ComparisonID == 0 {
						t.Errorf("analysis = %+v", an)
					}
				}
			}
		})
	}
}

// TestAnalyseRefusals: a character not on the upload, another member's
// upload, and an unparsed upload are 404s.
func TestAnalyseRefusals(t *testing.T) {
	store := fights.NewMemStore()
	a, u := seeded(t, store, &memAudit{}, healthyWCL())
	if rec := postAnalyse(a, false, u.ID, "area-52/nobody"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown character = %d", rec.Code)
	}
	if rec := postAnalyse(a, false, u.ID+100, nekromoo); rec.Code != http.StatusNotFound {
		t.Errorf("unknown upload = %d", rec.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/app/combatlogs/analyses", strings.NewReader(url.Values{"upload": {itoa(u.ID)}, "character": {nekromoo}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.analyse(rec, as(r, 2))
	if rec.Code != http.StatusNotFound {
		t.Errorf("another member = %d", rec.Code)
	}
}

// TestAnalyseAllowance: a second run inside the window is refused with the
// minutes for a member and allowed for an officer.
func TestAnalyseAllowance(t *testing.T) {
	store := fights.NewMemStore()
	store.Now = func() time.Time { return clock }
	w := healthyWCL()
	a, u := seeded(t, store, &memAudit{}, w)

	if _, q := location(postAnalyse(a, false, u.ID, nekromoo)); q.Get("msg") != "" {
		t.Fatalf("first run refused: %s", q.Encode())
	}
	_, q := location(postAnalyse(a, false, u.ID, nekromoo))
	if q.Get("msg") != "wait" || q.Get("min") != "120" {
		t.Errorf("second run = %s, want wait 120", q.Encode())
	}
	if _, q := location(postAnalyse(a, true, u.ID, nekromoo)); q.Get("msg") != "" {
		t.Errorf("officer refused: %s", q.Encode())
	}
	if len(store.Analyses) != 2 {
		t.Errorf("analyses = %d, want 2", len(store.Analyses))
	}
}
