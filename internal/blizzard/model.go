package blizzard

import (
	"context"
	"strings"
	"time"
)

// Client is the whole Blizzard dependency, behind one narrow interface so
// handlers can be tested against a fake with no network access
// (contracts/blizzard-api.md, Principle VI).
type Client interface {
	// UserInfo resolves the account identity behind an access token.
	UserInfo(ctx context.Context, token string) (Identity, error)

	// AccountCharacters lists the account's characters in the configured region.
	AccountCharacters(ctx context.Context, token string) ([]CharacterRef, error)

	// CharacterProfile fetches one character's full summary.
	CharacterProfile(ctx context.Context, token string, ref CharacterRef) (Character, error)
}

// Identity is the subset of /userinfo this platform uses.
type Identity struct {
	// Sub is the Blizzard account subject claim: the stable identity key.
	Sub string
	// BattleTag is a display name only. User-mutable, never used as a key.
	BattleTag string
}

// CharacterRef locates one character well enough to fetch its profile.
type CharacterRef struct {
	Name      string
	RealmSlug string
}

// Guild is the guild summary attached to a character profile.
//
// Blizzard omits the guild field entirely for an unguilded character, so this
// is carried as a pointer on Character: nil means "no guild", which is the same
// outcome as "not in TOMB" (research.md D4).
type Guild struct {
	Name      string
	RealmSlug string
}

// Character is assembled per dashboard view from the account profile summary
// plus that character's profile summary. It is never persisted: FR-016 fetches
// live on every view, so there is no character table (data-model.md).
type Character struct {
	Name      string
	RealmSlug string
	RealmName string

	// Region is configuration, not per-character data: this platform serves a
	// single configured region (FR-014).
	Region string

	Class      string
	ActiveSpec string // optional; may be empty

	Level            int
	AverageItemLevel int

	// LastLogin is the primary selection key (FR-006). Blizzard reports it as
	// epoch milliseconds; it is converted on parse.
	LastLogin time.Time

	// Guild is nil when the character belongs to no guild.
	Guild *Guild

	// IsCurrent is set on exactly one character by the selection rule.
	IsCurrent bool
}

// InGuild reports whether this character belongs to the named guild on the
// named realm, compared on slug rather than raw display text (FR-013).
//
// A nil Guild returns false: an unguilded character is not a member.
func (c Character) InGuild(name, realmSlug string) bool {
	if c.Guild == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(c.Guild.Name), strings.TrimSpace(name)) &&
		strings.EqualFold(strings.TrimSpace(c.Guild.RealmSlug), strings.TrimSpace(realmSlug))
}
