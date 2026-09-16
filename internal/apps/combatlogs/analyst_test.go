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
			a, sm := seeded(t, store, audit, &fakeWCL{rank: topTank})
			a.deps.AI = tc.model
			audit.entries = nil
			postAnalyse(a, true, sm.ID, goodLink)

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
			if done.Writeup != tc.model.text || done.Model != "" && done.Model != a.deps.Config.AIModel || done.PromptTokens != 1500 {
				t.Errorf("done = %+v", done)
			}
			// The table: Nekromoo's Head is item 212345 at 311 against the
			// same item at 320 -> same; Neck 999 at 324 vs nothing on
			// Nekromoo -> theirs only. Item 212345 has no name from Game
			// Data here, so it keeps its number on your side.
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
			// Talents: yours are entry ids 2 and 4 with no names -> "2", "4";
			// theirs are named.
			if done.TalentDiff == nil || strings.Join(done.TalentDiff.TheirsOnly, ",") != "Consumption,Marrowrend" || strings.Join(done.TalentDiff.YoursOnly, ",") != "2,4" {
				t.Errorf("diff = %+v", done.TalentDiff)
			}
			// The prompt carried names, the fight and the flag.
			for _, want := range []string{"Vexie and the Geargrinders", "Toptank", "Death Strike", "Pendant of Malefic Fury", `"mismatch"`, "Do these first"} {
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
	a, sm := seeded(t, store, &memAudit{}, &fakeWCL{rank: topTank})
	a.deps.AI = nil
	postAnalyse(a, true, sm.ID, goodLink)
	a.AnalyseOnce(context.Background())
	newest, _, _ := store.LatestAnalyses(context.Background(), "Nekromoo", "area-52")
	if newest == nil || newest.State != fights.AFailed || !strings.Contains(newest.Failure, "no model") {
		t.Errorf("newest = %+v", newest)
	}
}
