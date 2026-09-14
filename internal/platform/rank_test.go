package platform

import "testing"

// TestRankLabelNamesWhatItKnows covers the one piece of the roster Blizzard
// does not supply.
//
// Rank names are set in-game and appear in no endpoint, so an unconfigured rank
// must read as unconfigured. Guessing that rank 4 is "Member" would look
// correct and be a lie about somebody's standing in the guild.
func TestRankLabelNamesWhatItKnows(t *testing.T) {
	configured := GuildConfig{Ranks: []string{"Guild Master", "Officer", "", "Veteran"}}

	for _, tc := range []struct {
		name string
		cfg  GuildConfig
		rank int
		want string
	}{
		{"configured rank", configured, 1, "Officer"},
		{"configured, later", configured, 3, "Veteran"},

		// A blank entry in the middle keeps its position -- every rank below it
		// would otherwise shift up by one and mislabel everybody.
		{"blank entry falls back", configured, 2, "Rank 2"},

		{"past the end", configured, 7, "Rank 7"},
		{"nothing configured", GuildConfig{}, 3, "Rank 3"},

		// Rank 0 is the guild master by Blizzard's definition, not by
		// convention, so it is safe to name without being told.
		{"rank 0 unconfigured", GuildConfig{}, 0, "Guild Master"},
		{"rank 0 configured wins", GuildConfig{Ranks: []string{"Overlord"}}, 0, "Overlord"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.RankLabel(tc.rank); got != tc.want {
				t.Errorf("RankLabel(%d) = %q, want %q", tc.rank, got, tc.want)
			}
		})
	}
}
