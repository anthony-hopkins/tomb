package fights

import (
	"reflect"
	"testing"
)

// TestUpgradeTable covers the verdicts, missing slots on either side, the
// cosmetic slots left out, and the two ring slots kept apart.
func TestUpgradeTable(t *testing.T) {
	yours := []GearNamed{
		{Slot: 0, ID: 1, Name: "Casque", Level: 311},
		{Slot: 1, ID: 2, Name: "Old Pendant", Level: 300},
		{Slot: 3, ID: 9, Name: "Shirt", Level: 1},
		{Slot: 4, ID: 4, Name: "Shelter", Level: 331},
		{Slot: 10, ID: 10, Name: "Ring A", Level: 310},
		{Slot: 11, ID: 11, Name: "Ring B", Level: 305},
		{Slot: 14, ID: 14, Name: "Drape", Level: 311},
	}
	theirs := []GearNamed{
		{Slot: 0, ID: 1, Name: "Casque", Level: 320},
		{Slot: 1, ID: 3, Name: "New Pendant", Level: 324},
		{Slot: 4, ID: 5, Name: "Other Chest", Level: 320},
		{Slot: 10, ID: 11, Name: "Ring B", Level: 320},
		{Slot: 11, ID: 10, Name: "Ring A", Level: 320},
		{Slot: 15, ID: 15, Name: "Sword", Level: 320},
	}
	got := UpgradeTable(yours, theirs)
	want := []UpgradeRow{
		{Slot: "Head", Yours: "Casque", YourLvl: 311, Theirs: "Casque", TheirLv: 320, Verdict: "same"},
		{Slot: "Neck", Yours: "Old Pendant", YourLvl: 300, Theirs: "New Pendant", TheirLv: 324, Verdict: "chase", Gap: 24},
		{Slot: "Chest", Yours: "Shelter", YourLvl: 331, Theirs: "Other Chest", TheirLv: 320, Verdict: "holds"},
		{Slot: "Finger 1", Yours: "Ring A", YourLvl: 310, Theirs: "Ring B", TheirLv: 320, Verdict: "chase", Gap: 10},
		{Slot: "Finger 2", Yours: "Ring B", YourLvl: 305, Theirs: "Ring A", TheirLv: 320, Verdict: "chase", Gap: 15},
		{Slot: "Back", Yours: "Drape", YourLvl: 311},
		{Slot: "Main Hand", Theirs: "Sword", TheirLv: 320},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UpgradeTable =\n%+v\nwant\n%+v", got, want)
	}
	// Determinism (SC-005).
	if again := UpgradeTable(yours, theirs); !reflect.DeepEqual(got, again) {
		t.Error("the table differed on a second run")
	}
	if rows := UpgradeTable(nil, nil); len(rows) != 0 {
		t.Errorf("empty = %+v", rows)
	}
}

// TestDiffTalents: case-folded, trimmed, sorted, and never nil.
func TestDiffTalents(t *testing.T) {
	got := DiffTalents([]string{"Marrowrend", " Bonestorm ", "Death Strike"}, []string{"marrowrend", "Consumption", "Death Strike", "Abomination Limb"})
	want := TalentDiff{TheirsOnly: []string{"Abomination Limb", "Consumption"}, YoursOnly: []string{"Bonestorm"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DiffTalents = %+v, want %+v", got, want)
	}
	if d := DiffTalents(nil, nil); d.TheirsOnly == nil || d.YoursOnly == nil {
		t.Error("nil slices would render as null")
	}
}
