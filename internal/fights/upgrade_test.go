package fights

import "testing"

// TestUpgradePath: only chases rank; with a catalog the order is item level
// per crest and a track's ceiling caps the step; without one the order is
// item level gained and every step says the cost is unknown.
func TestUpgradePath(t *testing.T) {
	rows := []UpgradeRow{
		{Slot: "Head", Yours: "Casque", YourLvl: 311, Theirs: "Casque", TheirLv: 321, Verdict: VerdictSame},
		{Slot: "Wrist", Yours: "Old Bracers", YourLvl: 266, Theirs: "Bracers", TheirLv: 331, Verdict: VerdictChase, Gap: 65},
		{Slot: "Chest", Yours: "Old Chest", YourLvl: 272, Theirs: "Chest", TheirLv: 321, Verdict: VerdictChase, Gap: 49},
		{Slot: "Legs", Yours: "Legplates", YourLvl: 311, Theirs: "Chausses", TheirLv: 334, Verdict: VerdictChase, Gap: 23},
		{Slot: "Finger 1", Yours: "Ring", YourLvl: 311, Theirs: "Ring", TheirLv: 308, Verdict: VerdictHolds},
	}
	bare := UpgradePath(rows, CrestCatalog{})
	if len(bare) != 3 || bare[0].Slot != "Wrist" || bare[1].Slot != "Chest" || bare[2].Slot != "Legs" || bare[0].Rank != 1 || bare[0].Cost != 0 {
		t.Errorf("bare path = %+v", bare)
	}
	for _, s := range bare {
		if s.Note != "crest cost unknown: no catalog for this patch; ranked by item level gained" {
			t.Errorf("bare note = %q", s.Note)
		}
	}

	cat := CrestCatalog{Patch: "test", Tracks: []UpgradeTrack{
		{Name: "Veteran", Lowest: 260, Highest: 285, PerStep: 5, Crest: "Weathered", CrestCost: 15},
		{Name: "Champion", Lowest: 300, Highest: 325, PerStep: 5, Crest: "Runed", CrestCost: 15},
	}}
	priced := UpgradePath(rows, cat)
	by := map[string]UpgradeStep{}
	for _, s := range priced {
		by[s.Slot] = s
	}
	// Wrist 266 on Veteran: capped at 285, 4 steps of 15 = 60 crests for 19
	// levels = 0.32 per crest, with a note about the ceiling. Legs 311 on
	// Champion: to 325, 3 steps = 45 crests for 14 = 0.31. Chest 272 on
	// Veteran: to 285, 3 steps = 45 for 13 = 0.29.
	if w := by["Wrist"]; w.To != 285 || w.Gain != 19 || w.Cost != 60 || w.Crest != "Weathered" || w.PerCrest != 0.32 || w.Note == "" {
		t.Errorf("wrist = %+v", w)
	}
	if l := by["Legs"]; l.To != 325 || l.Cost != 45 || l.PerCrest != 0.31 {
		t.Errorf("legs = %+v", l)
	}
	if priced[0].Slot != "Wrist" || priced[1].Slot != "Legs" || priced[2].Slot != "Chest" {
		t.Errorf("priced order = %s, %s, %s", priced[0].Slot, priced[1].Slot, priced[2].Slot)
	}
	// An item at its track's ceiling has nowhere to go on crests.
	ceiling := UpgradePath([]UpgradeRow{{Slot: "Back", YourLvl: 325, TheirLv: 334, Verdict: VerdictChase, Gap: 9}}, cat)
	if len(ceiling) != 1 || ceiling[0].Cost != 0 || ceiling[0].Note != "at the Champion track's ceiling; needs a new item" {
		t.Errorf("ceiling = %+v", ceiling)
	}
}
