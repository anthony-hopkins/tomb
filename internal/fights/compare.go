package fights

import (
	"sort"
	"strings"
)

// The computed half of the comparison (FR-036, research D11): a table one
// can check by eye and a diff one can count, both the same on every run.

// SlotNames are the gear array's positions as the game orders them, which is
// the order COMBATANT_INFO and Warcraft Logs both use. Shirt and tabard are
// cosmetic and left out of the table.
var SlotNames = []string{
	"Head", "Neck", "Shoulder", "Shirt", "Chest", "Waist", "Legs", "Feet", "Wrist", "Hands",
	"Finger 1", "Finger 2", "Trinket 1", "Trinket 2", "Back", "Main Hand", "Off Hand", "Ranged", "Tabard",
}

var cosmetic = map[int]bool{3: true, 18: true}

// GearNamed is one item with its name resolved, by slot position.
type GearNamed struct {
	Slot  int
	ID    int
	Name  string
	Level int
}

// Verdicts.
const (
	VerdictSame  = "same"  // the same item
	VerdictHolds = "holds" // yours is at least their level
	VerdictChase = "chase" // theirs is higher; an upgrade to chase
)

// UpgradeTable lays yours beside theirs, slot by slot, with a verdict. A
// slot neither has is left out; a slot only one has is shown with the other
// side empty and no verdict.
func UpgradeTable(yours, theirs []GearNamed) []UpgradeRow {
	mine := bySlot(yours)
	them := bySlot(theirs)
	var rows []UpgradeRow
	for slot, name := range SlotNames {
		if cosmetic[slot] {
			continue
		}
		y, haveY := mine[slot]
		t, haveT := them[slot]
		if !haveY && !haveT {
			continue
		}
		row := UpgradeRow{Slot: name}
		if haveY {
			row.Yours, row.YourLvl = y.Name, y.Level
		}
		if haveT {
			row.Theirs, row.TheirLv = t.Name, t.Level
		}
		switch {
		case !haveY || !haveT:
			// no verdict
		case (y.ID != 0 && y.ID == t.ID) || (y.Name != "" && strings.EqualFold(y.Name, t.Name)):
			// The same item by id, or by name when one side came without
			// an id (current gear from Blizzard carries names only).
			row.Verdict = VerdictSame
		case y.Level >= t.Level:
			row.Verdict = VerdictHolds
		default:
			row.Verdict, row.Gap = VerdictChase, t.Level-y.Level
		}
		rows = append(rows, row)
	}
	return rows
}

func bySlot(gear []GearNamed) map[int]GearNamed {
	out := map[int]GearNamed{}
	for _, g := range gear {
		if (g.ID == 0 && g.Name == "") || g.Slot < 0 || g.Slot >= len(SlotNames) {
			continue
		}
		out[g.Slot] = g
	}
	return out
}

// DiffTalents is what they have that you do not, and the reverse, by name
// without regard to case, each side sorted.
func DiffTalents(yours, theirs []string) TalentDiff {
	fold := func(names []string) map[string]string {
		out := map[string]string{}
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n != "" {
				out[strings.ToLower(n)] = n
			}
		}
		return out
	}
	mine, them := fold(yours), fold(theirs)
	d := TalentDiff{TheirsOnly: []string{}, YoursOnly: []string{}}
	for k, n := range them {
		if _, ok := mine[k]; !ok {
			d.TheirsOnly = append(d.TheirsOnly, n)
		}
	}
	for k, n := range mine {
		if _, ok := them[k]; !ok {
			d.YoursOnly = append(d.YoursOnly, n)
		}
	}
	sort.Strings(d.TheirsOnly)
	sort.Strings(d.YoursOnly)
	return d
}
