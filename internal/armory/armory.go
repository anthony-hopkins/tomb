// Package armory renders one character the way the in-game Armory does: a
// portrait, a summary, and every equipped item with its tooltip.
//
// It lives outside both apps that use it because both need exactly the same
// thing. My Characters shows you your own characters; the guild overview shows
// you anyone on the roster. The only difference is where the character came
// from, so that is the only part left to the caller.
//
// Splitting it out is not speculative reuse -- it is the second caller arriving
// and the markup being a hundred lines long. Duplicating it would mean a
// tooltip fix landing on one page and not the other.
package armory

import (
	"context"
	"embed"
	"log/slog"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// FS carries the shared partial. Apps parse it into their own template set
// alongside their page, so the panel is defined once and rendered from two
// places:
//
//	tmpl, err := template.ParseFS(templateFS, "templates/page.html")
//	tmpl, err = tmpl.ParseFS(armory.FS, "templates/armory.html")
//
//go:embed templates/armory.html
var FS embed.FS

// maxConcurrentIconFetches bounds the icon fan-out, on the same reasoning as
// the character fan-out in the platform: far inside Blizzard's 100/second.
const maxConcurrentIconFetches = 8

// knownQualities are the item qualities the stylesheet has a colour for.
//
// A map rather than trusting the API's string straight into a class name: that
// value reaches the page, and building a CSS class out of unvalidated input is
// how markup gets injected. Anything unrecognised renders uncoloured.
var knownQualities = map[string]string{
	"POOR": "q-poor", "COMMON": "q-common", "UNCOMMON": "q-uncommon",
	"RARE": "q-rare", "EPIC": "q-epic", "LEGENDARY": "q-legendary",
	"ARTIFACT": "q-artifact", "HEIRLOOM": "q-heirloom",
}

// Panel is one character, ready for the shared template.
type Panel struct {
	Name             string
	RealmName        string
	Class            string
	ActiveSpec       string
	Level            int
	AverageItemLevel int
	LastLogin        string
	Guild            string

	// Badge is the one line above the name: "Most recently played" on your own
	// dashboard, the member's rank on the guild roster. The caller supplies it
	// because it is the one thing that genuinely differs between the two pages.
	Badge string

	// Render is Blizzard's own image of this character in its current gear, or
	// empty when there is none. Empty is normal, not an error: a character
	// Blizzard has never rendered simply has no assets.
	Render string

	// Gear is what the character is wearing, in the game's own slot order.
	Gear []Item
}

// Item is one equipped item, ready for the template.
type Item struct {
	Slot    string
	Name    string
	Level   int
	Quality string

	// QualityClass is the CSS class for the item's colour, pre-computed so the
	// template does not have to lowercase anything. Empty for an unknown
	// quality, which renders in the ordinary text colour rather than guessing.
	QualityClass string

	// The tooltip. Blizzard's own display strings, carried through unchanged.
	Icon         string
	Subclass     string
	Binding      string
	Armor        string
	Stats        []string
	Enchantments []string
	Sockets      []blizzard.Socket
	Transmog     string
	Durability   string
	Requirement  string
	Set          *blizzard.ItemSet

	// SetKey groups the pieces of one tier set, so hovering a set line can
	// highlight the other pieces the character is wearing. Empty when the item
	// is not part of a set.
	SetKey string
}

// Builder fetches the parts of a panel that are not already in hand.
type Builder struct {
	Client blizzard.Client
	Logger *slog.Logger
}

// Of builds the panel for a character whose summary the caller already has.
//
// This is the cheap path, and the one My Characters takes: the account summary
// was fetched once for the request, so the only calls left are the render and
// the gear for the single character on display.
func (b *Builder) Of(ctx context.Context, token string, c blizzard.Character, badge string) *Panel {
	p := &Panel{
		Name:             c.Name,
		RealmName:        realmLabel(&c),
		Class:            c.Class,
		ActiveSpec:       c.ActiveSpec,
		Level:            c.Level,
		AverageItemLevel: c.AverageItemLevel,
		LastLogin:        c.LastLogin.Format("2 Jan 2006, 15:04 MST"),
		Guild:            guildLabel(&c),
		Badge:            badge,
	}

	ref := blizzard.CharacterRef{Name: c.Name, RealmSlug: c.RealmSlug}
	p.Render = b.render(ctx, token, ref)
	p.Gear = b.gear(ctx, token, ref)
	return p
}

// For builds the panel for a character the caller knows only by name and realm.
//
// This is the guild roster's path. The roster endpoint carries a name, a realm,
// a level and a class -- not a specialisation, an item level or a last-played
// time -- so the summary has to be fetched before there is anything worth
// showing. That is one extra call compared with Of, paid only when somebody
// actually clicks a name.
func (b *Builder) For(ctx context.Context, token string, ref blizzard.CharacterRef, badge string) (*Panel, error) {
	c, err := b.Client.CharacterProfile(ctx, token, ref)
	if err != nil {
		b.Logger.Warn("character profile unavailable",
			"realm", ref.RealmSlug,
			"outcome", blizzard.OutcomeOf(err).String(),
		)
		return nil, err
	}
	return b.Of(ctx, token, c, badge), nil
}

// render fetches Blizzard's image of one character.
//
// A failure here costs the picture and nothing else. The page is about the
// character's data, which is already in hand; refusing to render it because an
// image was unavailable would turn a cosmetic outage into a broken site.
func (b *Builder) render(ctx context.Context, token string, ref blizzard.CharacterRef) string {
	media, err := b.Client.CharacterMedia(ctx, token, ref)
	if err != nil {
		b.Logger.Warn("character media unavailable",
			"realm", ref.RealmSlug,
			"outcome", blizzard.OutcomeOf(err).String(),
		)
		return ""
	}
	return media.Hero()
}

// gear fetches what the character is wearing.
//
// Failing costs the gear list and nothing else: everything above it on the page
// is already in hand, and a missing equipment endpoint is no reason to refuse
// to show somebody their character.
func (b *Builder) gear(ctx context.Context, token string, ref blizzard.CharacterRef) []Item {
	items, err := b.Client.CharacterEquipment(ctx, token, ref)
	if err != nil {
		b.Logger.Warn("character equipment unavailable",
			"realm", ref.RealmSlug,
			"outcome", blizzard.OutcomeOf(err).String(),
		)
		return nil
	}

	b.resolveIcons(ctx, token, items)

	gear := make([]Item, 0, len(items))
	for _, it := range items {
		g := Item{
			Slot:         it.SlotName,
			Name:         it.Name,
			Level:        it.Level,
			Quality:      it.Quality,
			QualityClass: knownQualities[it.Quality],
			Icon:         it.IconURL,
			Subclass:     it.Subclass,
			Binding:      it.Binding,
			Armor:        it.Armor,
			Stats:        it.Stats,
			Enchantments: it.Enchantments,
			Sockets:      it.Sockets,
			Transmog:     it.Transmog,
			Durability:   it.Durability,
			Requirement:  it.Requirement,
			Set:          it.Set,
		}
		if it.Set != nil {
			g.SetKey = setKey(it.Set.Display)
		}
		gear = append(gear, g)
	}
	return gear
}

// setKey turns a set's display name into something usable as an HTML id
// fragment, so the pieces of one set can be grouped without putting the raw
// name -- which comes from a remote API and contains apostrophes and spaces --
// into an attribute selector.
func setKey(display string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(display) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' && b.Len() > 0:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// resolveIcons fills in each item's icon URL, in parallel and bounded.
//
// Sixteen items is sixteen calls the first time anyone looks at a character.
// After that the client's cache answers them, because an item's icon never
// changes -- so the cost is paid once per process, not once per view.
//
// A failure is per-item and silent in the page: an item without an icon renders
// without one. Losing a picture is not a reason to lose a tooltip.
func (b *Builder) resolveIcons(ctx context.Context, token string, items []blizzard.EquippedItem) {
	// Collect what needs an icon first -- the items, and the gems sitting in
	// their sockets -- so both kinds fan out together under one limit rather
	// than in two passes. A gem is an item, and resolves through the same cache.
	type target struct {
		mediaID int
		assign  func(string)
	}
	var targets []target

	for i := range items {
		if items[i].MediaID != 0 {
			targets = append(targets, target{items[i].MediaID, func(u string) { items[i].IconURL = u }})
		}
		for j := range items[i].Sockets {
			if items[i].Sockets[j].MediaID != 0 {
				targets = append(targets, target{
					items[i].Sockets[j].MediaID,
					func(u string) { items[i].Sockets[j].IconURL = u },
				})
			}
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentIconFetches)

	for _, t := range targets {
		g.Go(func() error {
			icon, err := b.Client.ItemIcon(gctx, token, t.mediaID)
			if err != nil {
				b.Logger.Warn("icon unavailable",
					"media_id", t.mediaID,
					"outcome", blizzard.OutcomeOf(err).String(),
				)
				return nil
			}
			t.assign(icon)
			return nil
		})
	}
	_ = g.Wait()
}

// guildLabel is the character's guild, or empty when it has none.
func guildLabel(c *blizzard.Character) string {
	if c.Guild == nil {
		return ""
	}
	return c.Guild.Name
}

// realmLabel prefers the display name, falling back to the slug.
func realmLabel(c *blizzard.Character) string {
	if c.RealmName != "" {
		return c.RealmName
	}
	return c.RealmSlug
}
