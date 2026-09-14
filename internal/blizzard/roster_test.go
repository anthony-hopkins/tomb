package blizzard

import "testing"

// TestSortRosterGroupsByRankThenName pins the order the guild page reads in:
// rank first, guild master at the top, alphabetical inside each rank.
//
// Alphabetical WITHIN a rank rather than across the whole roster, because the
// grouping is the information — who outranks whom — and the alphabet is only
// there so a name can be found inside its group.
func TestSortRosterGroupsByRankThenName(t *testing.T) {
	members := []GuildMember{
		{Name: "zeta", Rank: 4},
		{Name: "Alpha", Rank: 4},
		{Name: "Yolanda", Rank: 0},
		{Name: "beta", Rank: 1},
		{Name: "Alan", Rank: 1},
		{Name: "mike", Rank: 4},
	}
	SortRoster(members)

	var got []string
	for _, m := range members {
		got = append(got, m.Name)
	}

	// Rank 0 first even though "Yolanda" sorts last alphabetically; rank 1
	// next, alphabetical within it; rank 4 last, likewise.
	want := []string{"Yolanda", "Alan", "beta", "Alpha", "mike", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestSortRosterIsCaseInsensitive: "alpha" and "Alpha" belong next to each
// other, not in separate halves of the rank.
func TestSortRosterIsCaseInsensitive(t *testing.T) {
	members := []GuildMember{
		{Name: "banana", Rank: 1},
		{Name: "Apple", Rank: 1},
		{Name: "cherry", Rank: 1},
	}
	SortRoster(members)

	if members[0].Name != "Apple" || members[1].Name != "banana" || members[2].Name != "cherry" {
		t.Errorf("order = %v, want Apple, banana, cherry", members)
	}
}

// TestGuildNameSlug covers the path segment the roster endpoint needs.
func TestGuildNameSlug(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"TOMB", "tomb"},
		{"Knights of the Round", "knights-of-the-round"},
		{"  Padded  ", "padded"},
	} {
		if got := GuildNameSlug(tc.in); got != tc.want {
			t.Errorf("GuildNameSlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
