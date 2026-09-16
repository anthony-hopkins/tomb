package combatlog

import (
	"reflect"
	"testing"
)

// TestParseCombatant reads the spec, the talents and the gear out of the
// bracket arrays, in both the 12.0 layout and the older one.
func TestParseCombatant(t *testing.T) {
	v22 := `COMBATANT_INFO,Player-1168-0A1B2C3D,0,10000,2000,50000,1000,0,0,0,800,800,800,0,0,700,700,700,0,900,600,600,600,20000,0,250,[(1,2,1),(3,4,1),(5,6,0)],(0,0,0),[(212345,311,(7456,0,0,0),(10390,6652),(213743)),(0,0,(),(),()),(212346,308,(),(),())],[Player-1168-0A1B2C3D,1234],1,0,0,0`
	c, err := parseCombatant(rec(v22))
	if err != nil {
		t.Fatal(err)
	}
	if c.GUID != "Player-1168-0A1B2C3D" || c.SpecID != 250 {
		t.Errorf("guid/spec = %q/%d", c.GUID, c.SpecID)
	}
	wantTalents := []Talent{{1, 2, 1}, {3, 4, 1}}
	if !reflect.DeepEqual(c.Talents, wantTalents) {
		t.Errorf("talents = %+v, want %+v (rank 0 dropped)", c.Talents, wantTalents)
	}
	wantGear := []GearPiece{
		{Slot: 0, Item: 212345, Level: 311, Enchant: 7456, Bonus: []int{10390, 6652}, Gems: []int{213743}},
		{Slot: 1, Item: 0, Level: 0},
		{Slot: 2, Item: 212346, Level: 308},
	}
	if !reflect.DeepEqual(c.Gear, wantGear) {
		t.Errorf("gear = %+v, want %+v", c.Gear, wantGear)
	}

	// The older layout has one stat field fewer before the spec; the spec is
	// still the field before the first array.
	v20 := `COMBATANT_INFO,Player-1,0,10000,2000,50000,1000,0,0,0,800,800,800,0,0,700,700,700,0,900,600,600,600,20000,65,[(7,8,1)],(0,0,0),[(212400,305,(),(),())],[],1,0,0,0`
	c, err = parseCombatant(rec(v20))
	if err != nil || c.SpecID != 65 || len(c.Talents) != 1 || len(c.Gear) != 1 || c.Gear[0].Item != 212400 {
		t.Errorf("v20 = %+v, %v", c, err)
	}

	// No gear array: the talents are still worth having.
	c, err = parseCombatant(rec(`COMBATANT_INFO,Player-1,0,250,[(1,2,1)]`))
	if err != nil || c.SpecID != 250 || len(c.Talents) != 1 || c.Gear != nil {
		t.Errorf("talents only = %+v, %v", c, err)
	}

	for _, bad := range []string{
		`COMBATANT_INFO,Player-1`,
		`COMBATANT_INFO,Player-1,0,x,[(1,2,1)]`,
		`COMBATANT_INFO,Player-1,0,250,[(1,2,1`,
		`COMBATANT_INFO,Player-1,0,250,[(1,2,1)],(0),[(1,2,(),(),()]`,
	} {
		if _, err := parseCombatant(rec(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// TestParseTree is the bracket reader on its own.
func TestParseTree(t *testing.T) {
	n, err := parseTree("[(1,2,(3,4)),(),5]")
	if err != nil {
		t.Fatal(err)
	}
	if len(n.items) != 3 || len(n.items[0].items) != 3 || n.items[0].items[2].items[1].value != 4 || n.items[2].value != 5 {
		t.Errorf("tree = %+v", n)
	}
	if _, err := parseTree("[1,2"); err == nil {
		t.Error("unclosed accepted")
	}
	if _, err := parseTree("[1]x"); err == nil {
		t.Error("trailing accepted")
	}
}
