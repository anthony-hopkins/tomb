package combatlogs_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/ai"
	"github.com/anthony-hopkins/tomb/internal/apps/combatlogs"
	"github.com/anthony-hopkins/tomb/internal/apps/dashboard"
	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

type fakeWCL struct{ rank wcl.Ranking }

func (f fakeWCL) BestRank(context.Context, wcl.CharacterRef, int, int, string) (wcl.Ranking, error) {
	return f.rank, nil
}

func (f fakeWCL) TopPlayer(context.Context, int, int, string, string, string) (wcl.CharacterRef, wcl.Ranking, error) {
	return wcl.CharacterRef{Region: "us", Slug: "area-52", Name: "Toptank"}, f.rank, nil
}

func (f fakeWCL) ZoneRankings(context.Context, wcl.CharacterRef) (wcl.Zone, error) {
	return wcl.Zone{Class: "Death Knight", Spec: "Blood", Difficulty: 5, Metric: "dps",
		Encounters: []wcl.ZoneEncounter{{ID: 3009, Name: "Vexie and the Geargrinders", Kills: 6}, {ID: 3010, Name: "Cauldron of Carnage", Kills: 4}}}, nil
}

func (f fakeWCL) CurrentZone(context.Context) (wcl.RaidZone, error) {
	return wcl.RaidZone{ID: 44, Name: "The Venomous Abyss", Encounters: []wcl.ZoneEncounter{{ID: 3009, Name: "Vexie and the Geargrinders"}}}, nil
}

func (f fakeWCL) LatestRank(context.Context, wcl.CharacterRef, int, int, string) (wcl.Ranking, error) {
	return wcl.Ranking{Name: "Nekromoo", Class: "Death Knight", Spec: "Blood", Metric: "dps", RankPercent: 74, Amount: 1102000, Duration: 250 * time.Second,
		StartedAt: time.Date(2026, 9, 14, 20, 0, 0, 0, time.UTC),
		Gear:      []wcl.Gear{{ID: 212345, Name: "Casque", ItemLevel: 311}}, Talents: []wcl.Talent{{ID: 2, Name: "Marrowrend"}}}, nil
}

type fakeAI struct{ text string }

func (f fakeAI) Write(context.Context, string, string) (string, ai.Usage, error) {
	return f.text, ai.Usage{PromptTokens: 100, OutputTokens: 50}, nil
}

// stackBoth mounts Combat logs and the character card behind the real core,
// sharing one store, with Warcraft Logs and the model faked.
func stackBoth(t *testing.T, store *fights.MemStore, audit *memAudit) (http.Handler, *combatlogs.App) {
	t.Helper()
	templates, err := platform.LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := &fakeBlizzard{guild: "TOMB"}
	csrf := &platform.CSRF{}
	core := &platform.Core{
		Deps: platform.Deps{
			Logger: logger, Blizzard: client, Audit: audit, CSRF: csrf,
			Config: platform.Config{UploadDir: t.TempDir(), Timezone: time.UTC, AIModel: "gemini-3.1-pro"},
			WCL: fakeWCL{rank: wcl.Ranking{Name: "Toptank", Class: "Death Knight", Spec: "Blood", Metric: "dps", RankPercent: 97, Amount: 1498220,
				Gear: []wcl.Gear{{ID: 212345, Name: "Casque", ItemLevel: 320}}, Talents: []wcl.Talent{{ID: 1, Name: "Consumption"}}}},
			AI: fakeAI{text: "Overview\n\nYou pressed Death Strike twice in two minutes.\n\nDo these first\n\n- Press it more."},
		},
		Sessions:  &auth.SessionManager{Store: &auth.Store{}},
		Profiles:  &platform.ProfileFetcher{Client: client, Guild: platform.GuildConfig{Name: "TOMB", RealmSlug: "area-52"}, Logger: logger},
		CSRF:      csrf,
		Templates: templates,
	}
	core.Deps.RenderInLayout = core.RenderInLayout
	logs, err := combatlogs.New(core.Deps, store)
	if err != nil {
		t.Fatal(err)
	}
	card, err := dashboard.New(core.Deps)
	if err != nil {
		t.Fatal(err)
	}
	card.Fights = store
	handler, err := platform.Mount(core, &auth.Handlers{Logger: logger}, []platform.App{card, logs})
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := platform.ContextWithSession(r.Context(), auth.Session{
			User: auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"}, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour),
		})
		handler.ServeHTTP(w, r.WithContext(ctx))
	}), logs
}

// uploadNight sends the fixture through the protocol and parses it,
// returning the upload's id.
func uploadNight(t *testing.T, h http.Handler, app *combatlogs.App) int64 {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(fixture(t))
	_ = zw.Close()
	body, _ := json.Marshal(map[string]any{"filename": "WoWCombatLog.txt", "size": 1000, "fingerprint": strings.Repeat("cd", 32)})
	r := httptest.NewRequest(http.MethodPost, "/app/combatlogs/uploads", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := do(h, withCSRF(r))
	var began struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &began)
	r = httptest.NewRequest(http.MethodPut, "/app/combatlogs/uploads/"+itoa(began.ID)+"/pieces/0", bytes.NewReader(buf.Bytes()))
	do(h, withCSRF(r))
	do(h, withCSRF(httptest.NewRequest(http.MethodPost, "/app/combatlogs/uploads/"+itoa(began.ID)+"/finish", nil)))
	if !app.ParseOnce(context.Background()) {
		t.Fatal("nothing parsed")
	}
	return began.ID
}

func postForm(h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: platform.CSRFCookieName, Value: token})
	form.Set(platform.CSRFFieldName, token)
	r.Body = io.NopCloser(strings.NewReader(form.Encode()))
	return do(h, r)
}

// TestAnalyseEndToEnd is US3 through the real core: the card's form, the
// route's redirect, the worker, the card's result, the trail, and the
// allowance.
func TestAnalyseEndToEnd(t *testing.T) {
	store := fights.NewMemStore()
	audit := &memAudit{}
	h, app := stackBoth(t, store, audit)
	uploadID := uploadNight(t, h, app)

	// The card offers the night.
	rec := do(h, httptest.NewRequest(http.MethodGet, "/app/dashboard?c=area-52/nekromoo", nil))
	card := rec.Body.String()
	if !strings.Contains(card, `action="/app/combatlogs/analyses"`) || !strings.Contains(card, `<option value="wcl">`) || !strings.Contains(card, "WoWCombatLog.txt") || !strings.Contains(card, "2 raid pulls") {
		t.Fatalf("card has no analyse form: %.300s", card)
	}
	form := url.Values{"source": {"upload:" + itoa(uploadID)}, "character": {"area-52/nekromoo"}}

	// Run it.
	rec = postForm(h, "/app/combatlogs/analyses", form)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/app/dashboard?c=area-52%2Fnekromoo" {
		t.Fatalf("analyse = %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	rec = do(h, httptest.NewRequest(http.MethodGet, "/app/dashboard?c=area-52/nekromoo", nil))
	if !strings.Contains(rec.Body.String(), "Analysing") || rec.Header().Get("Refresh") != "5" {
		t.Error("the card does not show the analysis in progress")
	}
	if !app.AnalyseOnce(context.Background()) {
		t.Fatal("nothing pending")
	}
	rec = do(h, httptest.NewRequest(http.MethodGet, "/app/dashboard?c=area-52/nekromoo", nil))
	card = rec.Body.String()
	for _, want := range []string{"Analysed", "<strong>Toptank</strong>, top Blood Death Knight on", "Head", "Same item", "Consumption", "<h4>Overview</h4>", "Press it more"} {
		if !strings.Contains(card, want) {
			t.Errorf("result card is missing %q", want)
		}
	}
	if rec.Header().Get("Refresh") != "" {
		t.Error("a finished card still refreshes")
	}
	var seen bool
	for _, e := range audit.entries {
		if e.Action == "combatlogs.analyse" && strings.Contains(e.Detail, "Toptank") && strings.Contains(e.Detail, "done") && e.BattleTag == "Tester#1234" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("no analyse audit entry: %+v", audit.entries)
	}

	// A member's second run inside two hours is refused, and the card says so.
	rec = postForm(h, "/app/combatlogs/analyses", form)
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "msg=wait") {
		t.Fatalf("second run = %q", loc)
	}
	rec = do(h, httptest.NewRequest(http.MethodGet, loc, nil))
	if !strings.Contains(rec.Body.String(), "You can run another analysis in") {
		t.Error("the card does not word the wait")
	}

	// A character not on the account is refused outright.
	before := len(store.Analyses)
	rec = postForm(h, "/app/combatlogs/analyses", url.Values{"source": {"upload:" + itoa(uploadID)}, "character": {"area-52/nobody"}})
	if len(store.Analyses) != before || rec.Code != http.StatusNotFound {
		t.Errorf("unknown character = %d, analyses %d -> %d", rec.Code, before, len(store.Analyses))
	}
}
