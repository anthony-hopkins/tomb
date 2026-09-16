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

// TestAnalystWorker: a run finishes with the computed table, the diff, the
// write-up and one audit entry; a failing model fails the row and leaves an
// earlier result alone; unknown ids keep their numbers.
func TestAnalystWorker(t *testing.T) {
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
			postAnalyse(a, true, u.ID, nekromoo)

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
			// top-player lookup, no other boss to fetch.
			if w.tops != 1 || w.ranks != 0 {
				t.Errorf("top lookups %d, rank fetches %d; want 1 and 0", w.tops, w.ranks)
			}
			// The table: the Vexie snapshot has Head 212345 at 311 against
			// the same item at 320 -> same; Neck is empty on your side ->
			// theirs only. Item 212345 has no name from Game Data here, so
			// it keeps its number on your side.
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
			// Talents: the snapshot has entries 2 and 4, unnamed.
			if done.TalentDiff == nil || strings.Join(done.TalentDiff.TheirsOnly, ",") != "Consumption,Marrowrend" || strings.Join(done.TalentDiff.YoursOnly, ",") != "2,4" {
				t.Errorf("diff = %+v", done.TalentDiff)
			}
			// The prompt carried the night, the boss, names, the pull left
			// out at the other difficulty, and the flag.
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
	a, u := seeded(t, store, &memAudit{}, healthyWCL())
	a.deps.AI = nil
	postAnalyse(a, true, u.ID, nekromoo)
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
	// Vexie is Mythic, Cauldron Heroic: one pull each, so the harder one.
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

// TestAnalystFetchesEveryBoss: with two bosses at one difficulty, the top
// player is looked up on the boss pulled most (ties to the latest) and
// their parse on the other boss is fetched and named in the prompt.
func TestAnalystFetchesEveryBoss(t *testing.T) {
	store := fights.NewMemStore()
	w := healthyWCL()
	a, u := seeded(t, store, &memAudit{}, w)
	for _, f := range store.Fights {
		f.DifficultyID = 16
	}
	model := &fakeAI{text: "fine"}
	a.deps.AI = model
	postAnalyse(a, true, u.ID, nekromoo)
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
