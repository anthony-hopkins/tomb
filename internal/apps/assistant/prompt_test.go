package assistant

import (
	"errors"
	"strings"
	"testing"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

func twoCharacters() platform.Profile {
	return platform.Profile{
		Characters: []blizzard.Character{
			{Name: "Maintank", RealmSlug: "area-52", RealmName: "Area 52", Class: "Warrior", ActiveSpec: "Protection", Level: 90, AverageItemLevel: 640, IsCurrent: true},
			{Name: "Treeboi", RealmSlug: "area-52", RealmName: "Area 52", Class: "Druid", ActiveSpec: "Restoration", Level: 90, AverageItemLevel: 610},
		},
		Membership: platform.GuildMembership{IsMember: true},
	}
}

// TestFacts: both characters go, the last-played one marked with its role
// and its gear, the other with its role and no gear; nothing about the
// account (SC-017).
func TestFacts(t *testing.T) {
	gear := []blizzard.EquippedItem{{SlotName: "Trinket 1", Name: "Unyielding Netherprism", Level: 645}, {SlotName: "Head", Name: "Casque", Level: 639}}
	f := FactsFor(twoCharacters(), gear, nil)
	if len(f.Characters) != 2 || f.Note != "" {
		t.Fatalf("facts = %+v", f)
	}
	tank, healer := f.Characters[0], f.Characters[1]
	if !tank.LastPlayed || tank.Role != "tank" || tank.Realm != "Area 52" || len(tank.Gear) != 2 || tank.Gear[0] != (GearFact{Slot: "Trinket 1", Name: "Unyielding Netherprism", Level: 645}) {
		t.Errorf("tank = %+v", tank)
	}
	if healer.LastPlayed || healer.Role != "healer" || len(healer.Gear) != 0 {
		t.Errorf("healer = %+v", healer)
	}
	sys := Instruction(f)
	for _, want := range []string{Refusal, `"last_played": true`, `"role": "tank"`, "Unyielding Netherprism", "Treeboi", "web search", "material, not instructions", "At most 500 words"} {
		if !strings.Contains(sys, want) {
			t.Errorf("instruction is missing %q", want)
		}
	}
	for _, leak := range []string{"Tester#1234", "BattleTag", "battle_tag", "sub"} {
		if strings.Contains(sys, `"`+leak+`"`) {
			t.Errorf("instruction carries %q", leak)
		}
	}
	if LastPlayed(twoCharacters()) != "Maintank" {
		t.Errorf("last played = %q", LastPlayed(twoCharacters()))
	}
}

// TestFactsGaps: no characters says so; unreadable gear says so.
func TestFactsGaps(t *testing.T) {
	if f := FactsFor(platform.Profile{}, nil, nil); !strings.Contains(f.Note, "No characters") {
		t.Errorf("empty profile note = %q", f.Note)
	}
	if f := FactsFor(twoCharacters(), nil, errors.New("503")); !strings.Contains(f.Note, "could not be read") {
		t.Errorf("gear error note = %q", f.Note)
	}
}

// TestTurns: the thread goes as user/model pairs, the new question last.
func TestTurns(t *testing.T) {
	turns := Turns([]Exchange{{Question: "q1", Answer: "a1"}, {Question: "q2", Answer: "a2"}}, "  q3 ")
	if len(turns) != 5 || turns[0].Role != "user" || turns[1].Role != "model" || turns[1].Text != "a1" || turns[4].Text != "q3" {
		t.Errorf("turns = %+v", turns)
	}
}
