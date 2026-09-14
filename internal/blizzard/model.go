package blizzard

import (
	"context"
	"sort"
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

	// CharacterMedia fetches the images Blizzard renders for a character.
	//
	// Separate from the profile because it is a separate endpoint and a
	// separate cost: fetched for the one character being looked at, never for
	// the whole roster, which would double the per-view fan-out (FR-016).
	CharacterMedia(ctx context.Context, token string, ref CharacterRef) (Media, error)

	// CharacterEquipment fetches what a character is currently wearing. Same
	// cost argument as CharacterMedia: one character, never the roster.
	CharacterEquipment(ctx context.Context, token string, ref CharacterRef) ([]EquippedItem, error)
}

// EquippedItem is one filled gear slot.
type EquippedItem struct {
	// SlotType is Blizzard's key, e.g. "HEAD" or "FINGER_1". Kept because the
	// display name is localised and the key is what ordering keys off.
	SlotType string
	SlotName string

	Name string

	// Quality is Blizzard's key, e.g. "EPIC". The colours players read gear by
	// are a presentation concern, so only the key travels this far.
	Quality string

	// Level is the item level of this piece, which is the number people
	// actually compare.
	Level int
}

// slotOrder is the order the game lays gear out in, which is the order players
// expect to read it in. Anything Blizzard returns that is not listed here sorts
// to the end rather than vanishing, so a new slot in a future patch shows up
// unordered instead of not at all.
var slotOrder = map[string]int{
	"HEAD": 0, "NECK": 1, "SHOULDER": 2, "BACK": 3, "CHEST": 4,
	"SHIRT": 5, "TABARD": 6, "WRIST": 7,
	"HANDS": 8, "WAIST": 9, "LEGS": 10, "FEET": 11,
	"FINGER_1": 12, "FINGER_2": 13, "TRINKET_1": 14, "TRINKET_2": 15,
	"MAIN_HAND": 16, "OFF_HAND": 17, "RANGED": 18,
}

// SortEquipment puts gear into the game's own slot order, in place.
func SortEquipment(items []EquippedItem) {
	sort.SliceStable(items, func(i, j int) bool {
		return slotRank(items[i].SlotType) < slotRank(items[j].SlotType)
	})
}

func slotRank(slot string) int {
	if rank, ok := slotOrder[slot]; ok {
		return rank
	}
	return len(slotOrder)
}

// Media is the set of images Blizzard renders for a character, in its current
// gear, on its own servers.
//
// This is how the site shows a character without shipping a 3D viewer: no
// extracted game assets, no WebGL, no JavaScript, and nothing to re-extract
// every patch. The trade is that these are stills -- there is no rotating it.
type Media struct {
	// Avatar is a square bust. Inset is waist-up on a scene background.
	Avatar string
	Inset  string

	// Main is full-body on a background; MainRaw is the same cut out, with
	// transparency, which is the one that suits a dark page.
	Main    string
	MainRaw string
}

// Hero is the largest usable image, preferring the cut-out so the character
// sits on the page's own background rather than in a grey box.
func (m Media) Hero() string {
	for _, candidate := range []string{m.MainRaw, m.Main, m.Inset, m.Avatar} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
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
