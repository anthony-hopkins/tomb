package platform

import (
	"fmt"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// GuildConfig is the configured TOMB guild identity used for verification
// (FR-013). Both values come from the environment, never from a hand-maintained
// list (Principle III).
type GuildConfig struct {
	Name      string
	RealmSlug string

	// Ranks names the guild's ranks by index, most senior first. Empty is fine;
	// see RankLabel.
	Ranks []string
}

// RankLabel names a rank index.
//
// Blizzard returns the index and never the name -- ranks are named in-game and
// are in no endpoint -- so an unconfigured rank falls back to "Rank N". That is
// visibly unconfigured rather than quietly wrong, which matters: guessing that
// rank 4 is "Member" would look correct and be a lie about somebody's standing
// in the guild.
func (g GuildConfig) RankLabel(rank int) string {
	if rank >= 0 && rank < len(g.Ranks) && g.Ranks[rank] != "" {
		return g.Ranks[rank]
	}
	if rank == 0 {
		// Rank 0 is the guild master in every guild, by Blizzard's definition
		// rather than by convention, so this one is safe to name.
		return "Guild Master"
	}
	return fmt.Sprintf("Rank %d", rank)
}

// GuildMembership is the FR-013 verification result.
//
// Derived per request and never persisted: data-model.md deliberately has no
// is_guild_member column, so a member who leaves the guild loses access on
// their very next view rather than waiting for a cache to expire.
type GuildMembership struct {
	IsMember bool

	// MatchedCharacter is the character that established membership, for
	// diagnostics. Nil when IsMember is false.
	MatchedCharacter *blizzard.Character
}

// Derive reports whether any of the supplied characters belongs to the
// configured guild.
//
// This reads the guild off each character's profile rather than fetching the
// guild roster and searching it. The characters were already fetched for
// last_login_timestamp and carry `guild` in the same response, so verification
// costs zero extra API calls (research.md D4). An absent guild — Blizzard omits
// the field entirely for an unguilded character — is not a match.
func (g GuildConfig) Derive(characters []blizzard.Character) GuildMembership {
	for i := range characters {
		if characters[i].InGuild(g.Name, g.RealmSlug) {
			return GuildMembership{IsMember: true, MatchedCharacter: &characters[i]}
		}
	}
	return GuildMembership{}
}
