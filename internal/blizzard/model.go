package blizzard

import (
	"context"
	"net/url"
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

	// GuildRoster lists every character in a guild.
	//
	// Characters, not people: somebody with five alts in the guild appears five
	// times, which is what the game means by a roster and what the API returns.
	GuildRoster(ctx context.Context, token, realmSlug, guildName string) ([]GuildMember, error)

	// ItemIcon resolves an item's media id to an icon URL.
	//
	// One call per item, which is the expensive one -- a full set of gear is
	// sixteen. Implementations are expected to cache: an item's icon never
	// changes, so this is static game data rather than character data and
	// FR-016's ban on caching does not reach it.
	ItemIcon(ctx context.Context, token string, mediaID int) (string, error)
}

// GuildMember is one character on a guild's roster.
type GuildMember struct {
	Name      string
	RealmSlug string
	RealmName string
	Level     int

	// Rank is the guild rank INDEX, 0 being the guild master. Blizzard does not
	// publish the rank NAMES -- those are set in-game and appear nowhere in the
	// API -- so turning 3 into "Veteran" is configuration, not data. See
	// GuildRanks.
	Rank int

	// Class is resolved from the roster's numeric class id, and is empty when
	// the id is one this build does not know.
	Class string
}

// classNames maps Blizzard's playable class ids to names.
//
// The roster returns an id and nothing else, and resolving each one properly
// would be a call per class on a page that is already a roster-sized fetch.
// These ids have been stable for the life of the API; a new one renders with no
// class rather than a wrong one.
var classNames = map[int]string{
	1: "Warrior", 2: "Paladin", 3: "Hunter", 4: "Rogue", 5: "Priest",
	6: "Death Knight", 7: "Shaman", 8: "Mage", 9: "Warlock", 10: "Monk",
	11: "Druid", 12: "Demon Hunter", 13: "Evoker",
}

// GuildNameSlug turns a guild's display name into the slug its API path uses.
func GuildNameSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SortRoster orders a roster the way a guild page should read: by rank, guild
// master first, and alphabetically within each rank.
//
// Alphabetical WITHIN a rank rather than across the whole list, because the
// grouping is the information -- who outranks whom -- and the alphabety is only
// there so a name can be found inside its group.
func SortRoster(members []GuildMember) {
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].Rank != members[j].Rank {
			return members[i].Rank < members[j].Rank
		}
		return strings.ToLower(members[i].Name) < strings.ToLower(members[j].Name)
	})
}

// IconHosts are the origins item icons may be served from.
//
// Checked rather than trusted, because an icon on an origin the page's
// Content-Security-Policy does not allow fails the way CSP failures always do:
// silently, with a blank space and a console line nobody is reading. Dropping
// the URL here gives the same blank space but with the reason recorded.
var IconHosts = []string{"render.worldofwarcraft.com"}

// AllowedIconURL reports whether an icon URL is one the page can actually load.
func AllowedIconURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	for _, h := range IconHosts {
		if u.Host == h || strings.HasSuffix(u.Host, "."+h) {
			return true
		}
	}
	return false
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

	// Everything below is the tooltip. Blizzard returns most of it already
	// formatted and localised as display strings -- "+152 Strength",
	// "Item Level 311", "Binds when picked up" -- so these are carried through
	// verbatim rather than reassembled from parts. Reformatting them here would
	// mean reimplementing Blizzard's own number formatting and localisation,
	// badly, in order to arrive back where we started.
	Subclass     string   // "Plate", "Sword"
	Binding      string   // "Binds when picked up"
	Armor        string   // "1,234 Armor"
	Stats        []string // "+152 Strength", "+3,002 Stamina"
	Enchantments []string // "Enchanted: Council's Intellect"
	Sockets      []Socket
	Transmog     string // "Transmogrified to: Shadowghast Helm"
	Durability   string // "Durability 100 / 100"
	Requirement  string // "Requires Level 90"
	Set          *ItemSet

	// MediaID identifies the item's icon. Resolving it to a URL is a separate
	// call per item, so it is fetched lazily and cached -- an item's icon never
	// changes, which is why caching it does not run into FR-016's ban on
	// caching character data. This is item data, not character data.
	MediaID int
	IconURL string
}

// Socket is one gem socket, filled or empty.
//
// Jewellery is where these matter most -- rings and necks are the reliably
// socketed slots -- but nothing here is jewellery-specific: a socket is a
// socket wherever the game puts one.
type Socket struct {
	// Display is Blizzard's own line, which already reads correctly whether or
	// not there is a gem in it.
	Display string

	// Type names the socket itself, e.g. "Prismatic Socket". It is what an
	// empty socket has to be described by, since there is no gem to name.
	Type string

	// GemName is the gem sitting in the socket, empty when nothing is.
	GemName string

	// MediaID identifies the gem's icon, and IconURL is it once resolved. A gem
	// is an item like any other, so it resolves through the same cache.
	MediaID int
	IconURL string

	Empty bool
}

// ItemSet is the tier-set block: which set, how many pieces are worn, and what
// the bonuses do.
type ItemSet struct {
	// Display is "Baleful Grave-Knight's Crucible (3/5)".
	Display string
	Pieces  []SetPiece
	Effects []string
}

// SetPiece is one item in a set, and whether this character is wearing it.
// Blizzard tells us which are equipped, which is what lets the tooltip grey out
// the ones that are not -- the same way the game does.
type SetPiece struct {
	Name     string
	Equipped bool
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
