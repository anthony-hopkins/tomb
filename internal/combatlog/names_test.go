package combatlog

import "testing"

// TestMatch: the log writes "Name-Realm" with the realm squashed; Blizzard
// gives a slug. Spaces, hyphens, apostrophes and case must not decide it.
func TestMatch(t *testing.T) {
	chars := []CharacterRef{
		{Name: "Nekromoo", RealmSlug: "area-52"},
		{Name: "Lazzarux", RealmSlug: "twisting-nether"},
		{Name: "Søvn", RealmSlug: "kel-thuzad"},
	}
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Nekromoo-Area52", "Nekromoo", true},
		{"nekromoo-area52", "Nekromoo", true},
		{"Nekromoo-Area 52", "Nekromoo", true},
		{"Lazzarux-TwistingNether", "Lazzarux", true},
		{"Søvn-KelThuzad", "Søvn", true},
		{"Søvn-Kel'Thuzad", "Søvn", true},
		{"Nekromoo-Illidan", "", false},
		{"Nekromo-Area52", "", false},
		{"Nekromoo", "", false},
		{"-Area52", "", false},
	}
	for _, tc := range tests {
		got, ok := Match(tc.in, chars)
		if ok != tc.ok || got.Name != tc.want {
			t.Errorf("Match(%q) = %q, %v; want %q, %v", tc.in, got.Name, ok, tc.want, tc.ok)
		}
	}
}

func TestSquash(t *testing.T) {
	for in, want := range map[string]string{"Area 52": "area52", "area-52": "area52", "Kel'Thuzad": "kelthuzad", "Twisting Nether": "twistingnether"} {
		if got := Squash(in); got != want {
			t.Errorf("Squash(%q) = %q, want %q", in, got, want)
		}
	}
}
