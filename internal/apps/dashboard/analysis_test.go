package dashboard_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/apps/dashboard"
	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

type noWCL struct{}

func (noWCL) BestRank(context.Context, wcl.CharacterRef, int, int, string) (wcl.Ranking, error) {
	return wcl.Ranking{}, wcl.ErrNoRank
}

func (noWCL) TopPlayer(context.Context, int, int, string, string, string) (wcl.CharacterRef, wcl.Ranking, error) {
	return wcl.CharacterRef{}, wcl.Ranking{}, wcl.ErrNoRank
}

func (noWCL) ZoneRankings(context.Context, wcl.CharacterRef) (wcl.Zone, error) {
	return wcl.Zone{}, wcl.ErrNoLogs
}

func (noWCL) LatestRank(context.Context, wcl.CharacterRef, int, int, string) (wcl.Ranking, error) {
	return wcl.Ranking{}, wcl.ErrNoRank
}

func (noWCL) CurrentZone(context.Context) (wcl.RaidZone, error) { return wcl.RaidZone{}, wcl.ErrNoRank }

func (noWCL) Leaderboard(context.Context, int, int, string, string, string) ([]wcl.Entry, error) {
	return nil, wcl.ErrNoRank
}

func (noWCL) Casts(context.Context, string, int, string) (wcl.CastSet, error) {
	return wcl.CastSet{}, wcl.ErrNoRank
}

// stackAnalysis mounts the dashboard with a fights store and, when asked, a
// Warcraft Logs client.
func stackAnalysis(t *testing.T, store fights.Store, withWCL bool) http.Handler {
	t.Helper()
	templates, err := platform.LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Now()
	client := &fakeBlizzard{
		refs: refs("Nekromoo"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return character("Nekromoo", now, 90, 312, "TOMB"), nil
		},
	}
	deps := platform.Deps{Logger: logger, Blizzard: client, Config: platform.Config{Timezone: time.UTC}}
	if withWCL {
		deps.WCL = noWCL{}
	}
	core := &platform.Core{
		Deps:      deps,
		Sessions:  &auth.SessionManager{Store: &auth.Store{}},
		Profiles:  &platform.ProfileFetcher{Client: client, Guild: platform.GuildConfig{Name: "TOMB", RealmSlug: "area-52"}, Logger: logger},
		CSRF:      &platform.CSRF{},
		Templates: templates,
	}
	core.Deps.RenderInLayout = core.RenderInLayout
	app, err := dashboard.New(core.Deps)
	if err != nil {
		t.Fatal(err)
	}
	app.Fights = store
	handler, err := platform.Mount(core, &auth.Handlers{Logger: logger}, []platform.App{app})
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := platform.ContextWithSession(r.Context(), auth.Session{
			User: auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"}, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour),
		})
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
}

// storeWith seeds a parsed upload with a raid pull (and a dungeon pull the
// picker must not count), and optionally analyses.
func storeWith(t *testing.T, seed func(store *fights.MemStore, uploadID int64)) *fights.MemStore {
	t.Helper()
	store := fights.NewMemStore()
	ctx := context.Background()
	u, _ := store.Begin(ctx, fights.Upload{UserID: 1, Filename: "WoWCombatLog.txt", Fingerprint: []byte("x"), PiecesTotal: 1})
	_ = store.AddFights(ctx, u.ID, []fights.Fight{
		{EncounterID: 3009, EncounterName: "Vexie and the Geargrinders", DifficultyID: 16, Kill: true, StartedAt: time.Date(2026, 9, 14, 20, 0, 0, 0, time.UTC),
			Summaries: []fights.Summary{{Name: "Nekromoo", RealmSlug: "area-52", SpecID: 250}}},
		{EncounterID: 1, EncounterName: "Some Dungeon Boss", DifficultyID: 8, Kill: true, StartedAt: time.Date(2026, 9, 14, 21, 0, 0, 0, time.UTC),
			Summaries: []fights.Summary{{Name: "Nekromoo", RealmSlug: "area-52", SpecID: 250}}},
	})
	store.Uploads[u.ID].State = fights.Parsed
	if seed != nil {
		seed(store, u.ID)
	}
	return store
}

func analysed(store *fights.MemStore, uploadID int64, state fights.AnalysisState, failure string) {
	ctx := context.Background()
	cp, _ := store.PutComparisonPlayer(ctx, fights.ComparisonPlayer{Region: "us", RealmSlug: "area-52", Name: "toptank", Encounter: 3009, WCLDiff: 5, Metric: "dps",
		FetchedAt: time.Now(), Class: "Death Knight", Spec: "Blood", Payload: []byte(`{"Name":"Toptank","Class":"Death Knight","Spec":"Blood"}`)})
	an, _ := store.CreateAnalysis(ctx, fights.Analysis{UserID: 1, UploadID: uploadID, Name: "Nekromoo", RealmSlug: "area-52", ComparisonID: cp.ID}, true)
	switch state {
	case fights.Done:
		_ = store.FinishAnalysis(ctx, an.ID, []fights.UpgradeRow{
			{Slot: "Head", Yours: "Old Casque", YourLvl: 311, Theirs: "New Casque", TheirLv: 320, Verdict: "chase", Gap: 9},
			{Slot: "Neck", Yours: "Pendant", YourLvl: 324, Theirs: "Pendant", TheirLv: 320, Verdict: "same"},
		}, fights.TalentDiff{TheirsOnly: []string{"Consumption"}, YoursOnly: []string{}},
			"Overview\n\nYou died once at 2:10 and lost twenty seconds of uptime.\n\nDo these first\n\n- Use Dancing Rune Weapon on pull.", "gemini-3.1-pro", 1500, 300)
	case fights.AFailed:
		_ = store.FailAnalysis(ctx, an.ID, failure)
	}
}

// TestAnalysisSection: the picker lists uploads with raid pulls, the form
// posts to the Combat logs app, and the section follows the newest analysis.
func TestAnalysisSection(t *testing.T) {
	tests := []struct {
		name    string
		store   *fights.MemStore
		wcl     bool
		path    string
		want    []string
		wantNot []string
		refresh bool
	}{
		{"no uploads: Warcraft Logs is the source", fights.NewMemStore(), true, "/app/dashboard",
			[]string{`action="/app/combatlogs/analyses"`, `<option value="wcl">My latest raid on Warcraft Logs</option>`, `name="source"`, `name="character" value="area-52/nekromoo"`},
			[]string{"upload:"}, false},
		{"upload, no analysis", storeWith(t, nil), true, "/app/dashboard",
			[]string{`<div class="armory-aside">`, `action="/app/combatlogs/analyses"`, `<option value="wcl">`, `<option value="upload:1">My upload WoWCombatLog.txt`, "1 raid pull<", `name="csrf_token"`, "top-ranked player of your class", "appears here once an analysis has run"},
			[]string{"Some Dungeon Boss", "Analysed"}, false},
		{"comparisons not set up", storeWith(t, nil), false, "/app/dashboard",
			[]string{"not set up on this site"}, []string{"analyse-form"}, false},
		{"pending", storeWith(t, func(s *fights.MemStore, id int64) { analysed(s, id, fights.Pending, "") }), true, "/app/dashboard",
			[]string{"Analysing", `class="analysing-phrase"`, "Counting casts against the cooldowns", "Finding the best Protection Warrior in the region"}, []string{"Analysed"}, true},
		{"done", storeWith(t, func(s *fights.MemStore, id int64) { analysed(s, id, fights.Done, "") }), true, "/app/dashboard",
			[]string{"Analysed", "<strong>Toptank</strong>, top Blood Death Knight on Vexie and the Geargrinders", "Old Casque", "New Casque", "Upgrade to chase (+9)", "Same item", "Consumption", "<h4>Overview</h4>", "<h4>Do these first</h4>", "lost twenty seconds", "<p>- Use Dancing Rune Weapon on pull.</p>"},
			[]string{"Analysing"}, false},
		{"failed over done", storeWith(t, func(s *fights.MemStore, id int64) {
			analysed(s, id, fights.Done, "")
			analysed(s, id, fights.AFailed, "the model is busy")
		}), true, "/app/dashboard",
			[]string{"could not be completed: the model is busy", "Old Casque"}, nil, false},
		{"message: no spec", storeWith(t, nil), true, "/app/dashboard?c=area-52/nekromoo&msg=nospec",
			[]string{"specialization is not known"}, nil, false},
		{"message: no logs", storeWith(t, nil), true, "/app/dashboard?c=area-52/nekromoo&msg=nologs",
			[]string{"no logs for this character"}, nil, false},
		{"message: wait", storeWith(t, nil), true, "/app/dashboard?msg=wait&min=90",
			[]string{"another analysis in 90 minutes"}, nil, false},
		{"message: unknown code renders nothing", storeWith(t, nil), true, "/app/dashboard?msg=<script>",
			nil, []string{"<script>", "role=\"alert\""}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, stackAnalysis(t, tc.store, tc.wcl), tc.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET = %d", rec.Code)
			}
			body := rec.Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, not := range tc.wantNot {
				if strings.Contains(body, not) {
					t.Errorf("unexpected %q", not)
				}
			}
			if got := rec.Header().Get("Refresh") == "5"; got != tc.refresh {
				t.Errorf("refresh = %v, want %v", got, tc.refresh)
			}
		})
	}
}
