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
	"sync"
	"time"

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

	// ClassSlug is Class as a CSS class fragment -- "death-knight" -- so the
	// name can be written in the class's colour. See ClassSlug.
	ClassSlug string

	// Badge is the one line above the name: "Most recently played" on your own
	// dashboard, the member's rank on the guild roster. The caller supplies it
	// because it is the one thing that genuinely differs between the two pages.
	Badge string

	// MythicPlusRating and Raids are this season's standing, from two
	// endpoints the profile does not cover. Zero and empty when the character
	// has none, which the template reads as "leave the row out".
	MythicPlusRating int
	Raids            []blizzard.RaidProgress

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

// ClassSlug turns a class name into the CSS class fragment the stylesheet
// keys its class colours on: "Death Knight" becomes "death-knight", so that
// "cls-death-knight" paints a mark or a name in the class's colour.
//
// Every name on the site is written in its class's colour, the way the game
// does it, so this lives with the other shared presentation rather than being
// three private copies of a lowercase-and-hyphenate.
func ClassSlug(class string) string {
	return strings.ReplaceAll(strings.ToLower(class), " ", "-")
}

// Stamp is how every time on the site is written: "14 Sep 2026, 20:15 EDT".
// The zone abbreviation is part of it, so a reader is never left guessing
// which zone a time is in.
const Stamp = "2 Jan 2006, 15:04 MST"

// LastPlayed formats a time the way every card on the site shows one, in the
// given zone. One place, because three did, and a date that reads differently
// between the rail and the panel looks like two different characters. A nil
// zone is UTC, which is what a test without configuration gets.
func LastPlayed(t time.Time, loc *time.Location) string {
	return t.In(Zone(loc)).Format(Stamp)
}

// Zone is loc, or UTC when there is none.
func Zone(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}

// Builder fetches the parts of a panel that are not already in hand.
type Builder struct {
	Client blizzard.Client
	Logger *slog.Logger

	// Zone is what the panel's times are shown in; nil is UTC.
	Zone *time.Location
}

// Of builds the panel for a character whose summary the caller already has.
//
// This is the cheap path, and the one My Characters takes: the account summary
// was fetched once for the request, so the only calls left are the render and
// the gear for the single character on display.
func (b *Builder) Of(ctx context.Context, token string, c blizzard.Character, badge string) *Panel {
	p := &Panel{
		Name:             c.Name,
		RealmName:        c.RealmLabel(),
		Class:            c.Class,
		ClassSlug:        ClassSlug(c.Class),
		ActiveSpec:       c.ActiveSpec,
		Level:            c.Level,
		AverageItemLevel: c.AverageItemLevel,
		LastLogin:        LastPlayed(c.LastLogin, b.Zone),
		Guild:            c.GuildName(),
		Badge:            badge,
	}

	// The render, the gear and the season's standing are independent calls to
	// independent endpoints, so they go out together: the panel costs one
	// round trip plus the icon fan-out, not four. Each writes its own fields,
	// so there is nothing to lock.
	ref := blizzard.CharacterRef{Name: c.Name, RealmSlug: c.RealmSlug}
	var wg sync.WaitGroup
	wg.Go(func() { p.Render = b.render(ctx, token, ref) })
	wg.Go(func() { p.Gear = b.gear(ctx, token, ref) })
	wg.Go(func() {
		pr := b.Progress(ctx, token, ref)
		p.MythicPlusRating, p.Raids = pr.MythicPlusRating, pr.Raids
	})
	wg.Wait()
	return p
}

// Progress fetches where a character stands this season: their Mythic+ rating
// and their raid progress, two endpoints, in parallel.
//
// Shared by every card on the site rather than only the panel, because the
// question "how far along is this character" is one people ask of a roster,
// not only of the one they have opened. Either call failing leaves its half
// empty and the card omits that row; logged at debug because on a roster of
// two hundred a warning each would be a page of them.
func (b *Builder) Progress(ctx context.Context, token string, ref blizzard.CharacterRef) blizzard.Progress {
	var p blizzard.Progress
	var wg sync.WaitGroup
	wg.Go(func() {
		rating, err := b.Client.MythicPlusRating(ctx, token, ref)
		if err != nil {
			b.Logger.Debug("mythic+ rating unavailable",
				"realm", ref.RealmSlug, "outcome", blizzard.OutcomeOf(err).String())
			return
		}
		p.MythicPlusRating = rating
	})
	wg.Go(func() {
		raids, err := b.Client.RaidProgression(ctx, token, ref)
		if err != nil {
			b.Logger.Debug("raid progression unavailable",
				"realm", ref.RealmSlug, "outcome", blizzard.OutcomeOf(err).String())
			return
		}
		p.Raids = raids
	})
	wg.Wait()
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
// name -- which comes from a remote API and contains apostrophes, parentheses
// and spaces -- into an attribute selector.
//
// Letters and digits pass through; any run of anything else becomes a single
// hyphen; the ends are trimmed. Two pieces of one set produce the same key
// because Blizzard sends the same display string for both, so the mapping only
// has to be deterministic and selector-safe, not reversible.
func setKey(display string) string {
	var b strings.Builder
	pendingSep := false
	for _, r := range strings.ToLower(display) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if pendingSep && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingSep = false
			b.WriteRune(r)
		default:
			pendingSep = true
		}
	}
	return b.String()
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
