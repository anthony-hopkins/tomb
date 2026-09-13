package platform

import "github.com/anthony-hopkins/tomb/internal/blizzard"

// GuildConfig is the configured TOMB guild identity used for verification
// (FR-013). Both values come from the environment, never from a hand-maintained
// list (Principle III).
type GuildConfig struct {
	Name      string
	RealmSlug string
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
