package ai

import (
	"strings"
	"testing"
)

// TestBuild: every section is present and labelled, names travel as names,
// and the mismatch flag is what the caller set.
func TestBuild(t *testing.T) {
	in := Input{
		Fight: Fight{Boss: "Vexie and the Geargrinders", Difficulty: "Mythic", Kill: true, Duration: "5:12", Date: "14 Sep 2026"},
		You: Player{Name: "Nekromoo", Spec: "Blood", Damage: 3600, Deaths: 0, ActiveTime: "5:10",
			Casts:   []Cast{{Name: "Death Strike", Count: 41}, {Name: "Dancing Rune Weapon", Count: 3, At: []float64{2, 92, 184}}},
			Gear:    []Gear{{Slot: "Head", Name: "Baleful Grave-Knight's Casque", Level: 311}},
			Talents: []string{"Marrowrend", "Bonestorm"}},
		Them: Player{Name: "Toptank", Spec: "Blood", DPS: 1498220, RankPercent: 97.3,
			Gear:    []Gear{{Slot: "Head", Name: "Baleful Grave-Knight's Casque", Level: 320}},
			Talents: []string{"Marrowrend", "Consumption"}},
		Table:    []map[string]any{{"slot": "Head", "verdict": "chase"}},
		Diff:     map[string][]string{"theirs_only": {"Consumption"}, "yours_only": {"Bonestorm"}},
		Mismatch: true,
	}
	got := Build(in)
	for _, want := range []string{"## fight", "## you", "## them", "## upgrade_table", "## talent_diff", "## mismatch\ntrue",
		"Vexie and the Geargrinders", "Dancing Rune Weapon", "at_seconds", "Consumption", "Baleful Grave-Knight's Casque", `"rank_percent": 97.3`} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if strings.Contains(got, "212345") {
		t.Error("an item id leaked into the prompt")
	}
	if !strings.Contains(System, "Do these first") || !strings.Contains(System, "Do not restate the gear table") {
		t.Error("the system instruction lost its rules")
	}
}
