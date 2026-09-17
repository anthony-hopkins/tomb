package ai

import (
	"strings"
	"testing"
)

// TestBuild: every section is present and labelled, names travel as names,
// and the mismatch flag is what the caller set.
func TestBuild(t *testing.T) {
	in := Input{
		Raid: Raid{Difficulty: "Mythic", Date: "14 Sep 2026", Pulls: 9, Kills: 2, Wipes: 7, Note: "2 pulls at another difficulty were left out"},
		Bosses: []Boss{
			{Name: "Vexie and the Geargrinders", Pulls: 1, Kills: 1, YourBestDPS: 30, YourBestDuration: "2:00", YourBestWasKill: true,
				YourCasts: []Cast{{Name: "Death Strike", Count: 41}, {Name: "Dancing Rune Weapon", Count: 3, At: []float64{2, 92, 184}}},
				TheirDPS:  1498220, TheirRankPercent: 97.3, TheirDuration: "5:12"},
			{Name: "Cauldron of Carnage", Pulls: 8, Kills: 1, Note: "the top player has no ranked kill of this boss at this difficulty"},
		},
		You: Player{Name: "Nekromoo", Spec: "Blood", Damage: 3600, Deaths: 1,
			Gear:    []Gear{{Slot: "Head", Name: "Baleful Grave-Knight's Casque", Level: 311}},
			Talents: []string{"Marrowrend", "Bonestorm"}},
		Them: Player{Name: "Toptank", Spec: "Blood",
			Gear:    []Gear{{Slot: "Head", Name: "Baleful Grave-Knight's Casque", Level: 320}},
			Talents: []string{"Marrowrend", "Consumption"}},
		Table:    []map[string]any{{"slot": "Head", "verdict": "chase"}},
		Diff:     map[string][]string{"theirs_only": {"Consumption"}, "yours_only": {"Bonestorm"}},
		Mismatch: true,
	}
	got := Build(in)
	for _, want := range []string{"## raid", "## bosses", "## you", "## them", "## upgrade_table", "## talent_diff", "## mismatch\ntrue",
		"Vexie and the Geargrinders", "Cauldron of Carnage", "Dancing Rune Weapon", "at_seconds", "Consumption", "Baleful Grave-Knight's Casque",
		`"their_rank_percent": 97.3`, "no ranked kill", "left out"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if strings.Contains(got, "212345") {
		t.Error("an item id leaked into the prompt")
	}
	if !strings.Contains(System, "- do_these_first:") || !strings.Contains(System, "never re-rank the upgrade path") || !strings.Contains(System, "- cooldowns:") || !strings.Contains(System, "- verify:") {
		t.Error("the system instruction lost its rules")
	}
	// The tabular fields are asked for as tables (spec 005, FR-056): the
	// upgrade path, the opener, the priority and the cooldown rules in both
	// modes, and the cooldown diff in the full one.
	for _, sys := range []string{System, SystemShowcase} {
		for _, want := range []string{"- upgrade_path: a table in the site's order", "as a table: step, ability", "- priority: the single-target priority as a table", "- cooldown_rules: a table", "put them in a Markdown table"} {
			if !strings.Contains(sys, want) {
				t.Errorf("the instruction no longer asks for %q", want)
			}
		}
	}
	if !strings.Contains(System, "First a table from cooldown_diffs") {
		t.Error("the cooldown diff is not asked for as a table")
	}
}
