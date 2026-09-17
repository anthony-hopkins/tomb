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

// fakeWCL answers the five reads from a table, and counts.
type fakeWCL struct {
	top       wcl.Ranking
	topRef    wcl.CharacterRef
	topErr    error
	rank      wcl.Ranking
	rankErr   error
	zone      wcl.Zone
	zoneErr   error
	latest    wcl.Ranking
	raid      wcl.RaidZone
	board     []wcl.Entry         // the leaderboard page; nil means just top
	boardOf   map[int][]wcl.Entry // per boss, when set
	casts     wcl.CastSet
	boards    int
	castsN    int
	timelines int
	tops      int
	ranks     int
	zones     int
	latests   int
	lastSpec  string
}

func (f *fakeWCL) CurrentZone(context.Context) (wcl.RaidZone, error) {
	if f.raid.ID == 0 {
		return wcl.RaidZone{}, errors.New("no zone")
	}
	return f.raid, nil
}

func (f *fakeWCL) Leaderboard(_ context.Context, enc, _ int, class, spec, _ string) ([]wcl.Entry, error) {
	f.boards++
	f.lastSpec = class + "/" + spec
	if f.topErr != nil {
		return nil, f.topErr
	}
	if f.board != nil {
		return f.board, nil
	}
	if f.boardOf != nil {
		if b := f.boardOf[enc]; len(b) > 0 {
			return b, nil
		}
		return nil, wcl.ErrNoRank
	}
	return []wcl.Entry{{Ref: f.topRef, Rank: f.top}}, nil
}

func (f *fakeWCL) Timeline(_ context.Context, _ string, _ int, player string, abilities []string) (wcl.Timeline, error) {
	f.timelines++
	if len(abilities) == 0 {
		return wcl.Timeline{Duration: 312 * time.Second}, nil
	}
	// The top player opens Dancing Rune Weapon on the pull and again at
	// 1:36; the member opens it late and once.
	tl := wcl.Timeline{Duration: 312 * time.Second, Kill: true, Phases: []wcl.Phase{{ID: 1, Name: "Stage One", At: 0}, {ID: 2, Name: "Stage Two", At: 150 * time.Second}}}
	if player == "Toptank" {
		tl.Casts = []wcl.CastEvent{{At: 4 * time.Second, Ability: "Dancing Rune Weapon"}, {At: 96 * time.Second, Ability: "Dancing Rune Weapon"}}
	} else {
		tl.Casts = []wcl.CastEvent{{At: 20 * time.Second, Ability: "Dancing Rune Weapon"}}
	}
	return tl, nil
}

func (f *fakeWCL) Casts(context.Context, string, int, string) (wcl.CastSet, error) {
	f.castsN++
	if len(f.casts.Abilities) == 0 {
		return wcl.CastSet{}, wcl.ErrNoRank
	}
	return f.casts, nil
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

func (f *fakeWCL) ZoneRankings(context.Context, wcl.CharacterRef) (wcl.Zone, error) {
	f.zones++
	return f.zone, f.zoneErr
}

func (f *fakeWCL) LatestRank(context.Context, wcl.CharacterRef, int, int, string) (wcl.Ranking, error) {
	f.latests++
	return f.latest, nil
}

var topTank = wcl.Ranking{Name: "Toptank", Class: "Death Knight", Spec: "Blood", Metric: "dps", RankPercent: 100, Amount: 1498220, Duration: 312 * time.Second, ReportCode: "AbCdEf123", FightID: 7,
	Gear:    []wcl.Gear{{ID: 212345, Name: "Baleful Grave-Knight's Casque", ItemLevel: 320}, {ID: 999, Name: "Pendant of Malefic Fury", ItemLevel: 324}},
	Talents: []wcl.Talent{{ID: 1, Name: "Marrowrend"}, {ID: 2, Name: "Consumption"}}}

var topRef = wcl.CharacterRef{Region: "us", Slug: "area-52", Name: "Toptank"}

func healthyWCL() *fakeWCL {
	return &fakeWCL{
		top: topTank, topRef: topRef,
		rank: wcl.Ranking{Name: "Toptank", Class: "Death Knight", Spec: "Blood", Metric: "dps", RankPercent: 96, Amount: 1400000, Duration: 200 * time.Second, ReportCode: "ThEiRs", FightID: 5},
		zone: wcl.Zone{Class: "Death Knight", Spec: "Blood", Difficulty: 4, Metric: "dps",
			Encounters: []wcl.ZoneEncounter{{ID: 3009, Name: "Vexie and the Geargrinders", Kills: 6}, {ID: 3010, Name: "Cauldron of Carnage", Kills: 4}}},
		latest: wcl.Ranking{Name: "Nekromoo", Class: "Death Knight", Spec: "Blood", Metric: "dps", RankPercent: 74, Amount: 1102000, Duration: 250 * time.Second, ReportCode: "YoUrS", FightID: 3,
			StartedAt: time.Date(2026, 9, 14, 20, 0, 0, 0, time.UTC),
			Gear:      []wcl.Gear{{ID: 212345, Name: "Baleful Grave-Knight's Casque", ItemLevel: 311}}, Talents: []wcl.Talent{{ID: 1, Name: "Marrowrend"}, {ID: 3, Name: "Bonestorm"}}},
		raid:  wcl.RaidZone{ID: 44, Name: "The Venomous Abyss", Encounters: []wcl.ZoneEncounter{{ID: 3009, Name: "Vexie and the Geargrinders"}, {ID: 3010, Name: "Cauldron of Carnage"}}},
		casts: wcl.CastSet{Abilities: []wcl.CastCount{{ID: 49998, Name: "Death Strike", Count: 63}, {ID: 49028, Name: "Dancing Rune Weapon", Count: 4}}, Active: 300 * time.Second, Total: 312 * time.Second},
	}
}

// topOnly is a Warcraft Logs that knows the leaderboards and the raid but
// has never seen the member: what a raider with no logs gets.
func topOnly() *fakeWCL {
	w := healthyWCL()
	w.zoneErr = wcl.ErrNoCharacter
	return w
}

// seeded parses the synthetic night into the store and returns the app and
// the upload.
func seeded(t *testing.T, store *fights.MemStore, audit *memAudit, w *fakeWCL) (*App, fights.Upload) {
	t.Helper()
	a := newApp(t, store, audit, true)
	a.deps.Config.BnetRegion = "us"
	if w != nil {
		a.deps.WCL = w
	}
	u := queued(t, a, store, gz(t, fixture(t)))
	if !a.ParseOnce(context.Background()) {
		t.Fatal("nothing parsed")
	}
	return a, u
}

func postAnalyse(a *App, officer bool, source, character string) *httptest.ResponseRecorder {
	form := url.Values{"source": {source}, "character": {character}}
	r := httptest.NewRequest(http.MethodPost, "/app/combatlogs/analyses", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := platform.ContextWithSession(r.Context(), auth.Session{User: auth.User{ID: 1, BattleTag: "Lazzloe#1149"}, AccessToken: "t"})
	ctx = platform.ContextWithProfile(ctx, platform.Profile{
		Characters: []blizzard.Character{{Name: "Nekromoo", RealmSlug: "area-52", Class: "Death Knight", ActiveSpec: "Blood"}},
		Membership: platform.GuildMembership{IsMember: true, IsOfficer: officer},
	})
	rec := httptest.NewRecorder()
	a.analyse(rec, r.WithContext(ctx))
	return rec
}

func uploadSource(u fights.Upload) string { return "upload:" + itoa(u.ID) }

func location(rec *httptest.ResponseRecorder) (path string, q url.Values) {
	u, _ := url.Parse(rec.Header().Get("Location"))
	return u.Path, u.Query()
}

const nekromoo = "area-52/nekromoo"

// TestAnalyseRoute walks every outcome of the form for both sources
// (FR-035, FR-039, FR-041): each refusal creates nothing and spends no
// allowance.
func TestAnalyseRoute(t *testing.T) {
	tests := []struct {
		name     string
		wcl      *fakeWCL // nil means no client configured
		source   string   // "" is Warcraft Logs; "upload" is the seeded upload
		mangle   func(store *fights.MemStore)
		wantMsg  string
		wantRows int
	}{
		{"warcraft logs: success", healthyWCL(), "", nil, "", 1},
		{"warcraft logs: no logs -> a showcase", topOnly(), "", nil, "", 1},
		{"warcraft logs: unknown character -> a showcase", topOnly(), "", nil, "", 1},
		{"no logs and no raid to showcase", &fakeWCL{zoneErr: wcl.ErrNoLogs, top: topTank, topRef: topRef}, "", nil, "nologs", 0},
		{"no logs and nobody ranked", &fakeWCL{zoneErr: wcl.ErrNoLogs, topErr: wcl.ErrNoRank, raid: healthyWCL().raid}, "", nil, "nologs", 0},
		{"warcraft logs: down", &fakeWCL{zoneErr: errors.New("boom")}, "", nil, "unavailable", 0},
		{"upload: success", healthyWCL(), "upload", nil, "", 1},
		{"upload: no raid pulls", healthyWCL(), "upload", func(s *fights.MemStore) {
			for _, f := range s.Fights {
				f.DifficultyID = 8
			}
		}, "nopulls", 0},
		{"upload: no spec recorded", healthyWCL(), "upload", func(s *fights.MemStore) {
			for _, f := range s.Fights {
				for i := range f.Summaries {
					f.Summaries[i].SpecID = 0
				}
			}
		}, "nospec", 0},
		{"nobody ranked", &fakeWCL{topErr: wcl.ErrNoRank, zone: healthyWCL().zone}, "", nil, "norank", 0},
		{"top player lookup down", &fakeWCL{topErr: errors.New("boom"), zone: healthyWCL().zone}, "", nil, "unavailable", 0},
		{"no client configured", nil, "", nil, "unavailable", 0},
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
			source := tc.source
			if source == "upload" {
				source = uploadSource(u)
			}
			rec := postAnalyse(a, false, source, nekromoo)
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
					wantSource, wantUpload := fights.SourceWCL, int64(0)
					if tc.source == "upload" {
						wantSource, wantUpload = fights.SourceUpload, u.ID
					}
					if tc.wcl.zoneErr != nil {
						wantSource = fights.SourceShowcase
					}
					if an.Source != wantSource || an.UploadID != wantUpload || an.Name != "Nekromoo" || an.ComparisonID == 0 {
						t.Errorf("analysis = %+v", an)
					}
				}
			}
		})
	}
}

// TestAnalyseRefusals: a character not on the account, another member's
// upload, and a malformed source are 404s.
func TestAnalyseRefusals(t *testing.T) {
	store := fights.NewMemStore()
	a, u := seeded(t, store, &memAudit{}, healthyWCL())
	if rec := postAnalyse(a, false, "", "area-52/nobody"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown character = %d", rec.Code)
	}
	if rec := postAnalyse(a, false, "upload:"+itoa(u.ID+100), nekromoo); rec.Code != http.StatusNotFound {
		t.Errorf("unknown upload = %d", rec.Code)
	}
	if rec := postAnalyse(a, false, "something-else", nekromoo); rec.Code != http.StatusNotFound {
		t.Errorf("bad source = %d", rec.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/app/combatlogs/analyses", strings.NewReader(url.Values{"source": {uploadSource(u)}, "character": {nekromoo}}.Encode()))
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
	a, _ := seeded(t, store, &memAudit{}, w)

	if _, q := location(postAnalyse(a, false, "", nekromoo)); q.Get("msg") != "" {
		t.Fatalf("first run refused: %s", q.Encode())
	}
	_, q := location(postAnalyse(a, false, "", nekromoo))
	if q.Get("msg") != "wait" || q.Get("min") != "120" {
		t.Errorf("second run = %s, want wait 120", q.Encode())
	}
	if _, q := location(postAnalyse(a, true, "", nekromoo)); q.Get("msg") != "" {
		t.Errorf("officer refused: %s", q.Encode())
	}
	if len(store.Analyses) != 2 {
		t.Errorf("analyses = %d, want 2", len(store.Analyses))
	}
}
