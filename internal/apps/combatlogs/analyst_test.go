package combatlogs

import (
	"context"
	"strings"
	"testing"

	"github.com/anthony-hopkins/tomb/internal/ai"
	"github.com/anthony-hopkins/tomb/internal/fights"
)

// fakeAI answers with fixed text, or an error, or a panic.
type fakeAI struct {
	text   string
	err    error
	panics bool
	system string
	prompt string
}

func (f *fakeAI) Write(_ context.Context, system, prompt string) (string, ai.Usage, error) {
	f.system, f.prompt = system, prompt
	if f.panics {
		panic("model exploded")
	}
	return f.text, ai.Usage{PromptTokens: 1500, OutputTokens: 300}, f.err
}

// TestAnalystFromWarcraftLogs: the default source. Your side is your latest
// ranked kill per boss on Warcraft Logs; the top player's parses on the
// other bosses are fetched; the table and diff use your latest kill's gear
// and talents; the prompt says what is and is not known.
func TestAnalystFromWarcraftLogs(t *testing.T) {
	store := fights.NewMemStore()
	audit := &memAudit{}
	w := healthyWCL()
	a, _ := seeded(t, store, audit, w)
	model := &fakeAI{text: "Overview\n\nSolid.\n\nDo these first\n\n- Keep going."}
	a.deps.AI = model
	audit.entries = nil
	postAnalyse(a, true, "", nekromoo)

	if !a.AnalyseOnce(context.Background()) {
		t.Fatal("nothing pending")
	}
	newest, done, _ := store.LatestAnalyses(context.Background(), "Nekromoo", "area-52")
	if newest == nil || newest.State != fights.Done || done == nil {
		t.Fatalf("state = %+v", newest)
	}
	// One top lookup on Vexie (most kills), your latest kill on both
	// bosses, their parse on Cauldron.
	if w.tops != 1 || w.zones != 2 || w.latests != 2 || w.ranks != 1 {
		t.Errorf("tops %d zones %d latests %d ranks %d; want 1 2 2 1", w.tops, w.zones, w.latests, w.ranks)
	}
	var head, neck *fights.UpgradeRow
	for i := range done.Table {
		switch done.Table[i].Slot {
		case "Head":
			head = &done.Table[i]
		case "Neck":
			neck = &done.Table[i]
		}
	}
	if head == nil || head.Verdict != fights.VerdictSame || head.YourLvl != 311 || head.TheirLv != 320 || head.Yours != "Baleful Grave-Knight's Casque" {
		t.Errorf("head row = %+v", head)
	}
	if neck == nil || neck.Yours != "" || neck.Theirs != "Pendant of Malefic Fury" {
		t.Errorf("neck row = %+v", neck)
	}
	if done.TalentDiff == nil || strings.Join(done.TalentDiff.TheirsOnly, ",") != "Consumption" || strings.Join(done.TalentDiff.YoursOnly, ",") != "Bonestorm" {
		t.Errorf("diff = %+v", done.TalentDiff)
	}
	for _, want := range []string{"## raid", "Heroic", "Vexie and the Geargrinders", "Cauldron of Carnage", `"your_rank_percent": 74`, `"your_kill_date": "14 Sep 2026"`,
		`"their_dps": 1400000`, "Toptank", "wipes, pull counts and ability use are not known", "## mismatch\nfalse"} {
		if !strings.Contains(model.prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if len(audit.entries) != 1 || !strings.Contains(audit.entries[0].Detail, "latest ranked kills on Warcraft Logs against Toptank: done") {
		t.Errorf("audit = %+v", audit.entries)
	}
}

// TestAnalystShowcase: a raider with no logs gets the top parses of their
// class and spec on every boss of the current raid, with cast counts and
// rates, against their current gear; the prompt is the showcase one.
func TestAnalystShowcase(t *testing.T) {
	store := fights.NewMemStore()
	audit := &memAudit{}
	w := topOnly()
	a, _ := seeded(t, store, audit, w)
	model := &fakeAI{text: "Rotation\n\nDeath Strike 12 times a minute.\n\nDo these first\n\n- Copy the build."}
	a.deps.AI = model
	audit.entries = nil
	postAnalyse(a, true, "", nekromoo)

	if !a.AnalyseOnce(context.Background()) {
		t.Fatal("nothing pending")
	}
	newest, done, _ := store.LatestAnalyses(context.Background(), "Nekromoo", "area-52")
	if newest == nil || newest.State != fights.Done || done == nil || done.Source != fights.SourceShowcase {
		t.Fatalf("state = %+v", newest)
	}
	// The route looked the top player up on the first boss at Mythic; the
	// worker used that answer and looked up the second boss; casts for both.
	if w.tops != 2 || w.castsN != 2 || w.latests != 0 {
		t.Errorf("tops %d casts %d latests %d; want 2 2 0", w.tops, w.castsN, w.latests)
	}
	if !strings.Contains(model.system, "briefing one of your raiders who has no logged raids") {
		t.Error("the compare instruction was used for a showcase")
	}
	for _, want := range []string{"## mode\n\"showcase\"", "Vexie and the Geargrinders", "Cauldron of Carnage", `"their_name": "Toptank"`, `"name": "Death Strike"`, `"per_minute": 12.1`, `"name": "Dancing Rune Weapon"`, "Mythic"} {
		if !strings.Contains(model.prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	// Nekromoo has no gear on record and the fake Blizzard cannot fetch any.
	if !strings.Contains(model.prompt, "could not be fetched") {
		t.Error("the prompt does not explain the missing gear")
	}
	if len(audit.entries) != 1 || !strings.Contains(audit.entries[0].Detail, "showcase of top Blood Death Knight parses in The Venomous Abyss against Toptank: done") {
		t.Errorf("audit = %+v", audit.entries)
	}
	// The leaderboard answers were cached under the class-and-spec key, with
	// the casts kept on them.
	cached, err := store.ComparisonPlayer(context.Background(), fights.ComparisonKey{Region: "top", RealmSlug: "DeathKnight", Name: "blood", Encounter: 3010, WCLDiff: 5, Metric: "dps"})
	if err != nil || !strings.Contains(string(cached.Payload), "Dancing Rune Weapon") {
		t.Errorf("cached leaderboard = %+v, %v", cached, err)
	}
}

// TestAnalystFromUpload: a run from an upload finishes with the computed
// table, the diff, the write-up and one audit entry; a failing model fails
// the row and leaves an earlier result alone; unknown ids keep their
// numbers.
func TestAnalystFromUpload(t *testing.T) {
	tests := []struct {
		name      string
		model     *fakeAI
		wantState fights.AnalysisState
		wantIn    string
	}{
		{"done", &fakeAI{text: "Overview\n\nYou pressed Death Strike twice.\n\nDo these first\n\n- Press it more."}, fights.Done, "done"},
		{"model busy", &fakeAI{err: ai.ErrBusy}, fights.AFailed, "busy"},
		{"model declined", &fakeAI{err: ai.ErrDeclined}, fights.AFailed, "declined"},
		{"model panics", &fakeAI{panics: true}, fights.AFailed, "something went wrong"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := fights.NewMemStore()
			audit := &memAudit{}
			w := healthyWCL()
			a, u := seeded(t, store, audit, w)
			a.deps.AI = tc.model
			audit.entries = nil
			postAnalyse(a, true, uploadSource(u), nekromoo)

			if !a.AnalyseOnce(context.Background()) {
				t.Fatal("nothing pending")
			}
			newest, done, _ := store.LatestAnalyses(context.Background(), "Nekromoo", "area-52")
			if newest == nil || newest.State != tc.wantState {
				t.Fatalf("state = %+v", newest)
			}
			if len(audit.entries) != 1 || audit.entries[0].Action != "combatlogs.analyse" || !strings.Contains(audit.entries[0].Detail, tc.wantIn) {
				t.Errorf("audit = %+v", audit.entries)
			}
			if tc.wantState != fights.Done {
				if done != nil {
					t.Error("a failed run produced a result")
				}
				return
			}
			if done.Writeup != tc.model.text || done.PromptTokens != 1500 {
				t.Errorf("done = %+v", done)
			}
			// Nekromoo pulled Vexie (Mythic) and Cauldron (Heroic) once
			// each; the night is the harder difficulty, so Vexie alone: one
			// top-player lookup, no other boss to fetch, no Warcraft Logs
			// lookup of the member.
			if w.tops != 1 || w.ranks != 0 || w.zones != 0 || w.latests != 0 {
				t.Errorf("tops %d ranks %d zones %d latests %d; want 1 0 0 0", w.tops, w.ranks, w.zones, w.latests)
			}
			var head, neck *fights.UpgradeRow
			for i := range done.Table {
				switch done.Table[i].Slot {
				case "Head":
					head = &done.Table[i]
				case "Neck":
					neck = &done.Table[i]
				}
			}
			if head == nil || head.Verdict != fights.VerdictSame || head.YourLvl != 311 || head.TheirLv != 320 || head.Yours != "212345" {
				t.Errorf("head row = %+v", head)
			}
			if neck == nil || neck.Yours != "" || neck.Theirs != "Pendant of Malefic Fury" || neck.Verdict != "" {
				t.Errorf("neck row = %+v", neck)
			}
			if done.TalentDiff == nil || strings.Join(done.TalentDiff.TheirsOnly, ",") != "Consumption,Marrowrend" || strings.Join(done.TalentDiff.YoursOnly, ",") != "2,4" {
				t.Errorf("diff = %+v", done.TalentDiff)
			}
			for _, want := range []string{"## raid", "Vexie and the Geargrinders", "Toptank", "Death Strike", "Pendant of Malefic Fury", `"mismatch"`, `"pulls": 1`, "left out", "Do these first"} {
				if !strings.Contains(tc.model.prompt+tc.model.system, want) {
					t.Errorf("prompt is missing %q", want)
				}
			}
			if a.AnalyseOnce(context.Background()) {
				t.Error("ran twice")
			}
		})
	}
}

// TestAnalystNoModel: a site without a model configured fails the run with a
// plain reason rather than hanging.
func TestAnalystNoModel(t *testing.T) {
	store := fights.NewMemStore()
	a, _ := seeded(t, store, &memAudit{}, healthyWCL())
	a.deps.AI = nil
	postAnalyse(a, true, "", nekromoo)
	a.AnalyseOnce(context.Background())
	newest, _, _ := store.LatestAnalyses(context.Background(), "Nekromoo", "area-52")
	if newest == nil || newest.State != fights.AFailed || !strings.Contains(newest.Failure, "no model") {
		t.Errorf("newest = %+v", newest)
	}
}

// TestNightOf: the difficulty raided most wins, bosses order by pulls then
// recency, the best pull is the kill or the highest rate, and the latest
// snapshot is kept.
func TestNightOf(t *testing.T) {
	store := fights.NewMemStore()
	a, u := seeded(t, store, &memAudit{}, nil)
	fs, _ := a.store.FightsForUpload(context.Background(), u.ID)
	n, ok := nightOf(summariesOf(fs, "Nekromoo", "area-52"), false)
	if !ok {
		t.Fatal("no night")
	}
	if n.Difficulty != 16 || len(n.Pulls) != 1 || n.LeftOut != 1 || n.SpecID != 250 {
		t.Errorf("night = diff %d, pulls %d, left out %d, spec %d", n.Difficulty, len(n.Pulls), n.LeftOut, n.SpecID)
	}
	if len(n.Bosses) != 1 || n.Bosses[0].Name != "Vexie and the Geargrinders" || n.Bosses[0].Kills != 1 || n.Bosses[0].Damage != 3600 {
		t.Errorf("bosses = %+v", n.Bosses)
	}
	if len(n.Gear) != 3 || len(n.Talents) != 2 {
		t.Errorf("snapshot = %d gear, %d talents", len(n.Gear), len(n.Talents))
	}
	if _, ok := nightOf(nil, false); ok {
		t.Error("an empty night was accepted")
	}
}

// TestAnalystFetchesEveryBoss: with two bosses at one difficulty in an
// upload, the top player is looked up on the boss pulled most (ties to the
// latest) and their parse on the other boss is fetched and named.
func TestAnalystFetchesEveryBoss(t *testing.T) {
	store := fights.NewMemStore()
	w := healthyWCL()
	a, u := seeded(t, store, &memAudit{}, w)
	for _, f := range store.Fights {
		f.DifficultyID = 16
	}
	model := &fakeAI{text: "fine"}
	a.deps.AI = model
	postAnalyse(a, true, uploadSource(u), nekromoo)
	if !a.AnalyseOnce(context.Background()) {
		t.Fatal("nothing pending")
	}
	if w.tops != 1 || w.ranks != 1 {
		t.Errorf("top lookups %d, rank fetches %d; want 1 and 1", w.tops, w.ranks)
	}
	for _, want := range []string{"Vexie and the Geargrinders", "Cauldron of Carnage", `"pulls": 2`, `"their_dps": 1400000`} {
		if !strings.Contains(model.prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if strings.Contains(model.prompt, "left out") {
		t.Error("nothing was left out, but the prompt says so")
	}
}
