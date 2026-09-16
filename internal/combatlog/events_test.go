package combatlog

import (
	"strings"
	"testing"
)

const adv19 = "Player-1,0000000000000000,1000000,1000000,50000,20000,10000,0,0,0,6,60,100,0,100.5,200.5,2769,3.14,312"
const adv17 = "Player-1,0000000000000000,1000000,1000000,50000,20000,10000,0,6,60,100,0,100.5,200.5,2769,3.14,312"

func rec(s string) line { return line{Fields: splitFields(s)} }

// TestDamage covers every damage kind, both layouts, advanced on and off,
// overkill absent and present.
func TestDamage(t *testing.T) {
	hdr := `Player-1,"A-B",0x511,0x0,Creature-1,"Boss",0x10a48,0x0`
	tests := []struct {
		name    string
		version int
		on      bool
		rec     string
		want    int64
		ok      bool
	}{
		{"v22 spell, advanced on", 22, true, "SPELL_DAMAGE," + hdr + `,1,"X",0x1,` + adv19 + ",1000,1000,-1,1,0,0,0,nil,nil,nil,nil", 1000, true},
		{"v22 spell with overkill", 22, true, "SPELL_DAMAGE," + hdr + `,1,"X",0x1,` + adv19 + ",1000,1000,200,1,0,0,0,nil,nil,nil,nil", 800, true},
		{"v22 swing, advanced on", 22, true, "SWING_DAMAGE," + hdr + "," + adv19 + ",500,500,-1,1,0,0,0,nil,nil,nil,nil", 500, true},
		{"v22 periodic", 22, true, "SPELL_PERIODIC_DAMAGE," + hdr + `,1,"X",0x1,` + adv19 + ",42,42,-1,1,0,0,0,nil,nil,nil,nil", 42, true},
		{"v22 spell, advanced off", 22, false, "SPELL_DAMAGE," + hdr + `,1,"X",0x1,1000,1000,-1,1,0,0,0,nil,nil,nil,nil`, 1000, true},
		{"v20 spell, advanced on", 20, true, "SPELL_DAMAGE," + hdr + `,1,"X",0x1,` + adv17 + ",1000,-1,1,0,0,0,nil,nil,nil,nil", 1000, true},
		{"v20 spell with overkill", 20, true, "SPELL_DAMAGE," + hdr + `,1,"X",0x1,` + adv17 + ",1000,300,1,0,0,0,nil,nil,nil,nil", 700, true},
		{"v20 swing, advanced off", 20, false, "SWING_DAMAGE," + hdr + ",500,-1,1,0,0,0,nil,nil,nil,nil", 500, true},
		{"overkill beyond amount clamps to zero", 22, false, "SPELL_DAMAGE," + hdr + `,1,"X",0x1,100,100,500,1,0,0,0,nil,nil,nil,nil`, 0, true},
		{"truncated", 22, true, "SPELL_DAMAGE," + hdr + `,1,"X",0x1,` + adv19, 0, false},
		{"not a number", 22, false, "SPELL_DAMAGE," + hdr + `,1,"X",0x1,lots,1000,-1`, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := rec(tc.rec)
			_, spell := classify(l.Event())
			got, ok := damage(l, spell, tc.on, layoutFor(tc.version))
			if ok != tc.ok || got != tc.want {
				t.Errorf("damage = %d, %v; want %d, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestHeal subtracts overheal in both layouts.
func TestHeal(t *testing.T) {
	hdr := `Player-1,"A-B",0x511,0x0,Player-1,"A-B",0x511,0x0`
	tests := []struct {
		name    string
		version int
		on      bool
		rec     string
		want    int64
	}{
		{"v22 advanced on", 22, true, "SPELL_HEAL," + hdr + `,1,"X",0x1,` + adv19 + ",950000,400,100,0,nil", 300},
		{"v22 advanced off", 22, false, "SPELL_HEAL," + hdr + `,1,"X",0x1,950000,400,100,0,nil`, 300},
		{"v20 advanced on", 20, true, "SPELL_PERIODIC_HEAL," + hdr + `,1,"X",0x1,` + adv17 + ",400,100,0,nil", 300},
		{"all overheal", 22, false, "SPELL_HEAL," + hdr + `,1,"X",0x1,950000,400,400,0,nil`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := heal(rec(tc.rec), tc.on, layoutFor(tc.version))
			if !ok || got != tc.want {
				t.Errorf("heal = %d, %v; want %d", got, ok, tc.want)
			}
		})
	}
}

// TestSmallEvents: spell prefix, owner, death flag, encounter lines.
func TestSmallEvents(t *testing.T) {
	id, name, ok := spellOf(rec(`SPELL_CAST_SUCCESS,Player-1,"A-B",0x511,0x0,Creature-1,"Boss",0x10a48,0x0,49998,"Death Strike",0x1`))
	if !ok || id != 49998 || name != "Death Strike" {
		t.Errorf("spellOf = %d %q %v", id, name, ok)
	}
	if owner := ownerOf(strings.Split("Pet-1,Player-1,1,2", ",")); owner != "Player-1" {
		t.Errorf("ownerOf = %q", owner)
	}
	if owner := ownerOf(strings.Split("Player-1,0000000000000000,1", ",")); owner != "" {
		t.Errorf("ownerOf with no owner = %q", owner)
	}
	if feigned(rec(`UNIT_DIED,0000000000000000,nil,0x80000000,0x80000000,Player-1,"A-B",0x511,0x0,0`)) {
		t.Error("a real death read as feigned")
	}
	if !feigned(rec(`UNIT_DIED,0000000000000000,nil,0x80000000,0x80000000,Player-1,"A-B",0x511,0x0,1`)) {
		t.Error("a feign read as a death")
	}
	e, ok := parseEncounter(rec(`ENCOUNTER_START,3009,"Vexie and the Geargrinders",16,20,2769`), false)
	if !ok || e.ID != 3009 || e.Name != "Vexie and the Geargrinders" || e.Difficulty != 16 || e.GroupSize != 20 {
		t.Errorf("ENCOUNTER_START = %+v, %v", e, ok)
	}
	e, ok = parseEncounter(rec(`ENCOUNTER_END,3009,"Vexie and the Geargrinders",16,20,1,120000`), true)
	if !ok || !e.Success || e.FightMS != 120000 {
		t.Errorf("ENCOUNTER_END = %+v, %v", e, ok)
	}
	if _, ok := parseEncounter(rec(`ENCOUNTER_END,3009`), true); ok {
		t.Error("a truncated ENCOUNTER_END was accepted")
	}
	if !isPlayer("Player-1168-0A1B2C3D") || isPlayer("Pet-0-1") || !isPet("Pet-0-1") || !isPet("Creature-0-1") || isPet("Player-1") {
		t.Error("GUID kinds")
	}
}
