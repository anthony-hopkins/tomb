package blizzard

import "testing"

// TestRoleOf pins the spec-to-role table on the names that are shared between
// classes, which is where a class-and-spec keyed table would have been needed
// if the game had ever given one name two roles. It has not.
func TestRoleOf(t *testing.T) {
	tests := map[string]Role{
		"Protection":    RoleTank, // Paladin and Warrior alike
		"Blood":         RoleTank,
		"Guardian":      RoleTank,
		"Holy":          RoleHealer, // Paladin and Priest alike
		"Restoration":   RoleHealer, // Druid and Shaman alike
		"Mistweaver":    RoleHealer,
		"Frost":         RoleDPS, // Death Knight and Mage alike
		"Fire":          RoleDPS,
		"Augmentation":  RoleDPS,
		"Some New Spec": RoleDPS, // unknown is damage, which most specs are
		"":              RoleUnknown,
	}
	for spec, want := range tests {
		if got := RoleOf(spec); got != want {
			t.Errorf("RoleOf(%q) = %q, want %q", spec, got, want)
		}
	}
}
