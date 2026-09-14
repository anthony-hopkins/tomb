// Package dashboard_test drives the real dashboard app through the real
// platform core, with only Blizzard faked. It covers acceptance scenarios 3, 4,
// 7 and 8, plus FR-010 and FR-016 behaviour that only shows up once the core's
// fetch, gate and rendering are all in play.
//
// It is an external test package so it can import the core without creating an
// import cycle (the core cannot import an app).
package dashboard_test

import (
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/apps/dashboard"
	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// fakeBlizzard implements the narrow client interface with no network access.
type fakeBlizzard struct {
	mediaFor   func(ref blizzard.CharacterRef) (blizzard.Media, error)
	mediaCalls atomic.Int32
	gearFor    func(ref blizzard.CharacterRef) ([]blizzard.EquippedItem, error)
	gearCalls  atomic.Int32
	iconFor    func(mediaID int) (string, error)
	iconCalls  atomic.Int32

	refs       []blizzard.CharacterRef
	profileFor func(ref blizzard.CharacterRef) (blizzard.Character, error)
	calls      atomic.Int32

	// ratingFor and raidsFor drive the season-standing lookups; nil means
	// "none", which is what an unrated or unraided character returns.
	ratingFor func(ref blizzard.CharacterRef) (int, error)
	raidsFor  func(ref blizzard.CharacterRef) ([]blizzard.RaidProgress, error)
}

func (f *fakeBlizzard) MythicPlusRating(_ context.Context, _ string, ref blizzard.CharacterRef) (int, error) {
	if f.ratingFor == nil {
		return 0, nil
	}
	return f.ratingFor(ref)
}

func (f *fakeBlizzard) RaidProgression(_ context.Context, _ string, ref blizzard.CharacterRef) ([]blizzard.RaidProgress, error) {
	if f.raidsFor == nil {
		return nil, nil
	}
	return f.raidsFor(ref)
}

func (f *fakeBlizzard) UserInfo(context.Context, string) (blizzard.Identity, error) {
	return blizzard.Identity{Sub: "sub", BattleTag: "Tester#1234"}, nil
}

func (f *fakeBlizzard) AccountCharacters(context.Context, string) ([]blizzard.CharacterRef, error) {
	return f.refs, nil
}

func (f *fakeBlizzard) CharacterProfile(_ context.Context, _ string, ref blizzard.CharacterRef) (blizzard.Character, error) {
	f.calls.Add(1)
	return f.profileFor(ref)
}

// mediaFor lets a test drive the render lookup; nil means "no images", which
// is the path a character Blizzard has never rendered takes.
func (f *fakeBlizzard) CharacterMedia(_ context.Context, _ string, ref blizzard.CharacterRef) (blizzard.Media, error) {
	f.mediaCalls.Add(1)
	if f.mediaFor == nil {
		return blizzard.Media{}, nil
	}
	return f.mediaFor(ref)
}

// gearFor lets a test drive the equipment lookup; nil means "no gear", which
// is what a character Blizzard cannot serve equipment for returns.
func (f *fakeBlizzard) CharacterEquipment(_ context.Context, _ string, ref blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
	f.gearCalls.Add(1)
	if f.gearFor == nil {
		return nil, nil
	}
	return f.gearFor(ref)
}

// iconFor drives icon resolution; iconCalls counts them, which is how the
// caching claim gets tested rather than asserted.
func (f *fakeBlizzard) ItemIcon(_ context.Context, _ string, mediaID int) (string, error) {
	f.iconCalls.Add(1)
	if f.iconFor == nil {
		return "", nil
	}
	return f.iconFor(mediaID)
}

func (f *fakeBlizzard) GuildRoster(context.Context, string, string, string) ([]blizzard.GuildMember, error) {
	return nil, nil
}

var _ blizzard.Client = (*fakeBlizzard)(nil)

func refs(names ...string) []blizzard.CharacterRef {
	out := make([]blizzard.CharacterRef, 0, len(names))
	for _, n := range names {
		out = append(out, blizzard.CharacterRef{Name: n, RealmSlug: "area-52"})
	}
	return out
}

func character(name string, when time.Time, level, ilvl int, guild string) blizzard.Character {
	c := blizzard.Character{
		Name:             name,
		RealmSlug:        "area-52",
		RealmName:        "Area 52",
		Class:            "Warrior",
		ActiveSpec:       "Protection",
		Level:            level,
		AverageItemLevel: ilvl,
		LastLogin:        when,
	}
	if guild != "" {
		c.Guild = &blizzard.Guild{Name: guild, RealmSlug: "area-52"}
	}
	return c
}

// stack wires the real dashboard behind the real core middleware.
func stack(t *testing.T, client blizzard.Client, signedIn bool) http.Handler {
	t.Helper()

	templates, err := platform.LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	core := &platform.Core{
		Deps:     platform.Deps{Logger: logger, Blizzard: client},
		Sessions: &auth.SessionManager{Store: &auth.Store{}},
		Profiles: &platform.ProfileFetcher{
			Client: client,
			Guild:  platform.GuildConfig{Name: "TOMB", RealmSlug: "area-52"},
			Logger: logger,
		},
		CSRF:      &platform.CSRF{},
		Templates: templates,
	}
	core.Deps.RenderInLayout = core.RenderInLayout

	app, err := dashboard.New(core.Deps)
	if err != nil {
		t.Fatalf("dashboard.New() error = %v", err)
	}

	handler, err := platform.Mount(core, &auth.Handlers{Logger: logger}, []platform.App{app})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	if !signedIn {
		return handler
	}

	// Stand in for the session middleware, which needs a database.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := platform.ContextWithSession(r.Context(), auth.Session{
			User:        auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"},
			AccessToken: "token",
			ExpiresAt:   time.Now().Add(time.Hour),
		})
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestShowsMostRecentCharacter is acceptance scenario 3: the card carries name,
// realm, class, level and average item level (FR-007).
func TestShowsMostRecentCharacter(t *testing.T) {
	now := time.Now()
	chars := map[string]blizzard.Character{
		// Higher level and better geared, but played two days ago.
		"Older": character("Older", now.Add(-48*time.Hour), 80, 700, "TOMB"),
		// The one actually played most recently.
		"Newest": character("Newest", now.Add(-1*time.Hour), 62, 410, "TOMB"),
	}

	fake := &fakeBlizzard{
		refs: refs("Older", "Newest"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return chars[ref.Name], nil
		},
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /app/dashboard = %d, want 200", rec.Code)
	}

	body := rec.Body.String()

	// The roster shows every character now, so presence is no longer the
	// question -- ORDER and the current marking are.
	if !strings.Contains(body, "Newest") || !strings.Contains(body, "Older") {
		t.Error("the roster should list every character on the account")
	}
	if strings.Index(body, "Newest") > strings.Index(body, "Older") {
		t.Error("the most recently played character should lead the roster (FR-006)")
	}

	// The first ROW must be the current one and must say so. is-current sits on
	// the row rather than the card so the list still shows which character is
	// the most recent while every card is closed. Splitting on the row class is
	// what lets this assert about one row rather than the page as a whole,
	// where both names appear either way.
	rows := strings.Split(body, `class="character-row`)
	if len(rows) < 3 {
		t.Fatalf("expected two character rows, found %d", len(rows)-1)
	}
	if !strings.Contains(rows[1], "Newest") {
		t.Error("the first row is not the most recently played character")
	}
	if !strings.Contains(rows[1], "is-current") {
		t.Error("the most recently played row is not marked as current")
	}
	if strings.Contains(rows[2], "is-current") {
		t.Error("a stale character is marked as the current one")
	}

	// Every row must be reachable without a mouse. The card is disclosed by
	// :hover and :focus-within, and focus-within needs something focusable.
	for i, row := range rows[1:] {
		if !strings.Contains(row, "tabindex") {
			t.Errorf("row %d is not focusable, so its card is keyboard-unreachable", i+1)
		}
	}

	for _, want := range []string{"Area 52", "Warrior", "62", "410"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard is missing required field value %q (FR-007)", want)
		}
	}
}

// TestSpecializationIsItsOwnRow pins the card's label/value alignment.
//
// The class and spec used to share one row as "Death Knight (Blood)", which
// wrapped for long class names and sat on one line for short ones -- so the
// rows below it lined up differently from character to character. They are two
// rows now, and the definition list keeps every label in the same column.
func TestSpecializationIsItsOwnRow(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Main"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			c := character(ref.Name, time.Now(), 90, 700, "TOMB")
			c.Class, c.ActiveSpec = "Death Knight", "Blood"
			return c, nil
		},
	}

	body := get(t, stack(t, fake, true), "/app/dashboard").Body.String()

	for _, want := range []string{
		"<dt>Class</dt>",
		"<dd>Death Knight</dd>",
		"<dt>Specialization</dt>",
		"<dd>Blood</dd>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the card is missing %q", want)
		}
	}

	// The old inline form would still contain both words, so absence of the
	// parenthetical is the assertion that actually distinguishes them.
	if strings.Contains(body, "(Blood)") {
		t.Error("the specialization is still rendered inline with the class")
	}
}

// TestCharacterListIsANavigationPane covers the page shape: the roster is a
// rail beside the content, not a block within it. Asserted through the markup
// the stylesheet keys off, since CSS itself is not exercised here.
func TestCharacterListIsANavigationPane(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Main"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return character(ref.Name, time.Now(), 80, 600, "TOMB"), nil
		},
	}

	body := get(t, stack(t, fake, true), "/app/dashboard").Body.String()

	if !strings.Contains(body, `<aside class="character-nav"`) {
		t.Error("the character list is not in an aside; main:has(.dashboard) and the " +
			"rail styling both key off this structure")
	}
	if !strings.Contains(body, `class="dashboard`) {
		t.Error("the dashboard wrapper is missing, so main will stay at its reading width")
	}
	// The rail must come before the content in source order, so it is the first
	// grid column and the first thing a screen reader reaches.
	if strings.Index(body, "character-nav") > strings.Index(body, "dashboard-main") {
		t.Error("the rail is rendered after the content; it should lead")
	}
}

// twoChars is a fake with a clear most-recent winner and a clear runner-up.
func twoChars() *fakeBlizzard {
	now := time.Now()
	set := map[string]blizzard.Character{
		"Newest": character("Newest", now.Add(-1*time.Hour), 90, 700, "TOMB"),
		"Older":  character("Older", now.Add(-72*time.Hour), 80, 600, "TOMB"),
	}
	return &fakeBlizzard{
		refs: refs("Newest", "Older"),
		profileFor: func(r blizzard.CharacterRef) (blizzard.Character, error) {
			return set[r.Name], nil
		},
		mediaFor: func(r blizzard.CharacterRef) (blizzard.Media, error) {
			return blizzard.Media{
				MainRaw: "https://render.worldofwarcraft.com/" + r.Name + "-main-raw.png",
			}, nil
		},
	}
}

// armoryPanel returns just the Armory section, so assertions are about the
// panel rather than the page -- every name appears in the rail regardless.
func armoryPanel(t *testing.T, body string) string {
	t.Helper()

	i := strings.Index(body, `<section class="armory"`)
	if i < 0 {
		t.Fatal("no Armory panel was rendered")
	}
	j := strings.Index(body[i:], "</section>")
	return body[i : i+j]
}

// TestArmoryDefaultsToTheMostRecent is the landing state: no URL parameter, so
// the panel shows the character FR-006 ranks first.
func TestArmoryDefaultsToTheMostRecent(t *testing.T) {
	fake := twoChars()
	body := get(t, stack(t, fake, true), "/app/dashboard").Body.String()

	panel := armoryPanel(t, body)
	if !strings.Contains(panel, "Newest") {
		t.Error("the Armory panel does not show the most recently played character")
	}
	if strings.Contains(panel, "Older") {
		t.Error("the Armory panel shows a character that was not selected")
	}
	if !strings.Contains(panel, "Newest-main-raw.png") {
		t.Error("the panel is missing Blizzard's render of the character")
	}

	// Exactly one media call: for the character on display, never the roster.
	// Fetching renders for every character would double the per-view fan-out
	// that FR-016 already re-pays on every single view.
	if n := fake.mediaCalls.Load(); n != 1 {
		t.Errorf("made %d character-media calls, want exactly 1", n)
	}
}

// TestArmorySwitchesByLink is the interaction: clicking a name in a card loads
// that character. It is a plain GET, so it is bookmarkable and needs no script.
func TestArmorySwitchesByLink(t *testing.T) {
	fake := twoChars()
	handler := stack(t, fake, true)

	// Find the link the card actually renders and follow it, rather than
	// asserting a URL this test made up. html/template percent-encodes the
	// slash in the key, so the href is not the string you would write by hand
	// -- and following it is what proves the encoding round-trips.
	body := get(t, handler, "/app/dashboard").Body.String()

	var href string
	for _, m := range regexp.MustCompile(`href="(\?c=[^"]+)"`).FindAllStringSubmatch(body, -1) {
		candidate := html.UnescapeString(m[1])
		if strings.Contains(strings.ToLower(candidate), "older") {
			href = candidate
		}
	}
	if href == "" {
		t.Fatal("no switch link for Older; the character names in the card must be links")
	}

	panel := armoryPanel(t, get(t, handler, "/app/dashboard"+href).Body.String())
	if !strings.Contains(panel, "Older") {
		t.Error("following the link did not switch the Armory panel")
	}
	if strings.Contains(panel, "Newest") {
		t.Error("the panel still shows the default character after switching")
	}
	if !strings.Contains(panel, "Older-main-raw.png") {
		t.Error("the render did not follow the selection")
	}
}

// TestArmoryFallsBackForAnUnknownCharacter covers the stale bookmark: a
// character that has since been deleted, renamed or transferred away.
//
// It shows the member their main rather than a 404, because an error page is a
// worse answer to "this character is gone" than simply showing the one they
// actually play.
func TestArmoryFallsBackForAnUnknownCharacter(t *testing.T) {
	fake := twoChars()

	rec := get(t, stack(t, fake, true), "/app/dashboard?c=area-52/ghost")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET with an unknown character = %d, want 200", rec.Code)
	}
	if !strings.Contains(armoryPanel(t, rec.Body.String()), "Newest") {
		t.Error("an unknown character did not fall back to the most recently played")
	}
}

// TestArmorySurvivesAMissingRender: the page is about the character's data,
// which is already in hand. Losing the picture must not lose the page.
func TestArmorySurvivesAMissingRender(t *testing.T) {
	fake := twoChars()
	fake.mediaFor = func(blizzard.CharacterRef) (blizzard.Media, error) {
		return blizzard.Media{}, &blizzard.APIError{
			Endpoint: "character-media",
			Outcome:  blizzard.OutcomeUnavailable,
		}
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET with no render available = %d, want 200", rec.Code)
	}

	panel := armoryPanel(t, rec.Body.String())
	if !strings.Contains(panel, "Newest") {
		t.Error("the character's data vanished along with its image")
	}
	if !strings.Contains(panel, "no render for this character") {
		t.Error("the panel does not say why the image is absent")
	}
}

// TestArmoryShowsEquippedGear covers the gear list: present, in the game's slot
// order, coloured by quality.
func TestArmoryShowsEquippedGear(t *testing.T) {
	fake := twoChars()
	// Deliberately out of order, to prove the view sorts rather than trusting
	// whatever order Blizzard happened to return.
	fake.gearFor = func(blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
		items := []blizzard.EquippedItem{
			{SlotType: "MAIN_HAND", SlotName: "Main Hand", Name: "Big Axe", Quality: "LEGENDARY", Level: 720},
			{SlotType: "HEAD", SlotName: "Head", Name: "Sturdy Helm", Quality: "EPIC", Level: 710},
			{SlotType: "CHEST", SlotName: "Chest", Name: "Plate Chest", Quality: "RARE", Level: 700},
		}
		blizzard.SortEquipment(items)
		return items, nil
	}

	panel := armoryPanel(t, get(t, stack(t, fake, true), "/app/dashboard").Body.String())

	for _, want := range []string{"Sturdy Helm", "Plate Chest", "Big Axe", "720", "710", "700"} {
		if !strings.Contains(panel, want) {
			t.Errorf("the gear list is missing %q", want)
		}
	}

	// Head before Chest before Main Hand: the order the game lays gear out in.
	head, chest, hand := strings.Index(panel, "Sturdy Helm"),
		strings.Index(panel, "Plate Chest"), strings.Index(panel, "Big Axe")
	if head >= chest || chest >= hand {
		t.Error("gear is not in the game's slot order")
	}

	for _, class := range []string{"q-epic", "q-rare", "q-legendary"} {
		if !strings.Contains(panel, class) {
			t.Errorf("no %s colour class; gear is read by quality colour", class)
		}
	}

	// One equipment call, for the character on display, never the roster.
	if n := fake.gearCalls.Load(); n != 1 {
		t.Errorf("made %d equipment calls, want exactly 1", n)
	}
}

// TestUnknownQualityIsNotTurnedIntoAClass: the quality string comes from
// Blizzard and ends up on the page, so it is mapped through a known set rather
// than interpolated. An unrecognised value renders uncoloured instead of
// inventing a class name out of remote input.
func TestUnknownQualityIsNotTurnedIntoAClass(t *testing.T) {
	fake := twoChars()
	fake.gearFor = func(blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
		return []blizzard.EquippedItem{
			{SlotType: "HEAD", SlotName: "Head", Name: "Odd Hat", Quality: "MYTHIC_PLUS_SOMETHING", Level: 1},
		}, nil
	}

	panel := armoryPanel(t, get(t, stack(t, fake, true), "/app/dashboard").Body.String())

	if !strings.Contains(panel, "Odd Hat") {
		t.Error("an item with an unknown quality was dropped instead of shown plain")
	}
	if strings.Contains(panel, "MYTHIC_PLUS_SOMETHING") {
		t.Error("the raw quality string reached the page")
	}
}

// TestArmorySurvivesMissingGear: the same rule as a missing render. Everything
// above the gear list is already in hand, so losing the list must not lose it.
func TestArmorySurvivesMissingGear(t *testing.T) {
	fake := twoChars()
	fake.gearFor = func(blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
		return nil, &blizzard.APIError{Endpoint: "character-equipment", Outcome: blizzard.OutcomeUnavailable}
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET with no equipment available = %d, want 200", rec.Code)
	}
	if !strings.Contains(armoryPanel(t, rec.Body.String()), "Newest") {
		t.Error("the character's details vanished along with its gear")
	}
}

// TestItemTooltipCarriesTheDetail covers the tooltip contents: the display
// strings Blizzard already returns, rendered rather than discarded.
func TestItemTooltipCarriesTheDetail(t *testing.T) {
	fake := twoChars()
	fake.gearFor = func(blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
		return []blizzard.EquippedItem{{
			SlotType: "HEAD", SlotName: "Head", Name: "Baleful Casque",
			Quality: "EPIC", Level: 311, Subclass: "Plate", MediaID: 42,
			Binding:     "Binds when picked up",
			Stats:       []string{"+152 Strength", "+3,002 Stamina"},
			Sockets:     []blizzard.Socket{{Display: "Sockets: Ruby", Empty: false}},
			Transmog:    "Transmogrified to: Shadowghast Helm",
			Durability:  "Durability 100 / 100",
			Requirement: "Requires Level 90",
			Set: &blizzard.ItemSet{
				Display: "Grave-Knight's Crucible (3/5)",
				Pieces: []blizzard.SetPiece{
					{Name: "Baleful Casque", Equipped: true},
					{Name: "Baleful Gibbets", Equipped: false},
				},
				Effects: []string{"(4) Set: something considerable happens"},
			},
		}}, nil
	}
	fake.iconFor = func(int) (string, error) {
		return "https://render.worldofwarcraft.com/us/icons/56/inv_helm.jpg", nil
	}

	raw := armoryPanel(t, get(t, stack(t, fake, true), "/app/dashboard").Body.String())

	// Unescaped, so these assert what a member reads. html/template encodes
	// "+" as &#43; and "'" as &#39;, both correctly, and neither is what this
	// test is about.
	panel := html.UnescapeString(raw)

	for _, want := range []string{
		"+152 Strength", "+3,002 Stamina",
		"Transmogrified to: Shadowghast Helm",
		"Binds when picked up", "Sockets: Ruby",
		"Grave-Knight's Crucible (3/5)",
		"(4) Set: something considerable happens",
		"Durability 100 / 100", "Requires Level 90",
		"inv_helm.jpg",
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("the tooltip is missing %q", want)
		}
	}

	// A set piece the character is NOT wearing is shown greyed, not hidden --
	// the game shows the whole set so you can see what is left to collect.
	if !strings.Contains(raw, "is-missing") {
		t.Error("an unequipped set piece is not marked as missing")
	}
}

// TestIconsAreFetchedOncePerItem is the claim that makes icons affordable: an
// item's icon never changes, so it is cached and the per-view cost disappears
// after the first look.
func TestIconsAreFetchedOncePerItem(t *testing.T) {
	fake := twoChars()
	fake.gearFor = func(blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
		return []blizzard.EquippedItem{
			{SlotType: "HEAD", SlotName: "Head", Name: "Helm", MediaID: 1},
			{SlotType: "CHEST", SlotName: "Chest", Name: "Chest", MediaID: 2},
			{SlotType: "HANDS", SlotName: "Hands", Name: "Gloves", MediaID: 0},
		}, nil
	}
	fake.iconFor = func(id int) (string, error) {
		return "https://render.worldofwarcraft.com/us/icons/56/i.jpg", nil
	}

	get(t, stack(t, fake, true), "/app/dashboard")

	// Two items carry a media id; the third does not and must not be asked for.
	if n := fake.iconCalls.Load(); n != 2 {
		t.Errorf("made %d icon calls, want 2 (the item without a media id is skipped)", n)
	}
}

// TestGearSurvivesAMissingIcon: an item without a picture is still an item.
func TestGearSurvivesAMissingIcon(t *testing.T) {
	fake := twoChars()
	fake.gearFor = func(blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
		return []blizzard.EquippedItem{
			{SlotType: "HEAD", SlotName: "Head", Name: "Unpictured Helm", MediaID: 7, Level: 300},
		}, nil
	}
	fake.iconFor = func(int) (string, error) {
		return "", &blizzard.APIError{Endpoint: "item-media", Outcome: blizzard.OutcomeUnavailable}
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET with no icon available = %d, want 200", rec.Code)
	}
	panel := armoryPanel(t, rec.Body.String())
	if !strings.Contains(panel, "Unpictured Helm") {
		t.Error("the item vanished along with its icon")
	}
	if !strings.Contains(panel, "gear-icon-blank") {
		t.Error("no placeholder where the icon should be, so the row will not line up")
	}
}

// TestSocketsShowTheirGems covers jewellery, which is where sockets are usually
// the point: a ring with a gem in it should show that gem, and a ring with an
// open socket should show that the socket is open.
func TestSocketsShowTheirGems(t *testing.T) {
	fake := twoChars()
	fake.gearFor = func(blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
		return []blizzard.EquippedItem{
			{
				SlotType: "FINGER_1", SlotName: "Ring 1", Name: "Ritual Binder's Ring",
				Quality: "EPIC", Level: 311, MediaID: 100,
				Sockets: []blizzard.Socket{
					{GemName: "Masterful Ysemerald", Type: "Prismatic Socket", MediaID: 900},
					{Display: "Prismatic Socket", Type: "Prismatic Socket", Empty: true},
				},
			},
			{
				SlotType: "NECK", SlotName: "Neck", Name: "Pendant of Maleficence",
				Quality: "EPIC", Level: 311, MediaID: 101,
				Sockets: []blizzard.Socket{
					{GemName: "Quick Onyx", Type: "Prismatic Socket", MediaID: 901},
				},
			},
			// No sockets at all: must render without an empty gem column.
			{SlotType: "HEAD", SlotName: "Head", Name: "Plain Helm", Level: 300, MediaID: 102},
		}, nil
	}
	fake.iconFor = func(id int) (string, error) {
		return fmt.Sprintf("https://render.worldofwarcraft.com/us/icons/56/i%d.jpg", id), nil
	}

	raw := armoryPanel(t, get(t, stack(t, fake, true), "/app/dashboard").Body.String())
	panel := html.UnescapeString(raw)

	// The gems themselves, by name and by icon.
	for _, want := range []string{"Masterful Ysemerald", "Quick Onyx", "i900.jpg", "i901.jpg"} {
		if !strings.Contains(panel, want) {
			t.Errorf("the gem %q is not shown", want)
		}
	}

	// An open socket is information, especially on jewellery, so it is drawn
	// rather than omitted.
	if !strings.Contains(raw, "gem-empty") {
		t.Error("an empty socket is not shown; an unsocketed ring looks identical to a socketed one")
	}

	// Three items and three sockets, one of which is empty: five icons. The
	// empty socket has no gem and so no media id, and must not be requested --
	// which is the assertion, not the arithmetic.
	if n := fake.iconCalls.Load(); n != 5 {
		t.Errorf("made %d icon calls, want 5 (3 items + 2 gems; the empty socket has no media)", n)
	}

	// The item with no sockets must not sprout an empty gem container.
	helm := raw[strings.Index(raw, "Plain Helm"):]
	if i := strings.Index(helm, "</button>"); i > 0 && strings.Contains(helm[:i], "gear-gems") {
		t.Error("an item with no sockets rendered a gem container anyway")
	}
}

// TestNonMemberDenied is acceptance scenario 7 and FR-013a.
func TestNonMemberDenied(t *testing.T) {
	tests := []struct {
		name  string
		guild string
	}{
		{"different guild", "Not Tomb"},
		{"no guild at all", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeBlizzard{
				refs: refs("Stranger"),
				profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
					return character(ref.Name, time.Now(), 80, 600, tc.guild), nil
				},
			}

			rec := get(t, stack(t, fake, true), "/app/dashboard")

			body := rec.Body.String()
			if !strings.Contains(body, "TOMB members only") {
				t.Error("a non-member did not get the members-only page")
			}
			if strings.Contains(body, "Stranger") {
				t.Error("a non-member's character data was rendered; no app should be reachable")
			}
		})
	}
}

// TestAnonymousRedirected is scenario 8's mechanism: an absent or expired
// session is unauthenticated, and no character data is rendered or fetched.
func TestAnonymousRedirected(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Maintank"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			t.Error("Blizzard was called for an unauthenticated request")
			return blizzard.Character{}, nil
		},
	}

	rec := get(t, stack(t, fake, false), "/app/dashboard")
	if rec.Code != http.StatusFound {
		t.Errorf("GET /app/dashboard as anonymous = %d, want 302", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "Maintank") {
		t.Error("character data leaked to an unauthenticated request")
	}
}

// TestPartialDataNotice is research.md D9's user-visible half: a character that
// Blizzard could not serve right now is worth telling the member about, because
// it will probably be there next time.
func TestPartialDataNotice(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Good", "Broken"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			if ref.Name == "Broken" {
				return blizzard.Character{}, &blizzard.APIError{
					Endpoint:   "character-profile-summary",
					StatusCode: http.StatusInternalServerError,
					Outcome:    blizzard.OutcomeUnavailable,
				}
			}
			return character(ref.Name, time.Now(), 80, 600, "TOMB"), nil
		},
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /app/dashboard = %d, want 200 despite partial failure", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Good") {
		t.Error("the character that did load is not shown")
	}
	if !strings.Contains(body, "could not be loaded") {
		t.Error("the page does not tell the member the data is incomplete")
	}
}

// TestGoneCharactersAreIgnoredSilently is the other half of that judgement.
//
// Blizzard's account summary keeps listing characters that have been deleted,
// renamed or transferred off the account, and their profiles answer 404 forever
// after. Three of them turn up on a real account here. Treating that as
// "incomplete data" puts a warning on the page on every single visit, for
// characters that are never coming back -- which is how a notice becomes
// wallpaper and stops being read when it matters.
func TestGoneCharactersAreIgnoredSilently(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Live", "Deleted"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			if ref.Name == "Deleted" {
				return blizzard.Character{}, &blizzard.APIError{
					Endpoint:   "character-profile-summary",
					StatusCode: http.StatusNotFound,
					Outcome:    blizzard.OutcomeNotFound,
				}
			}
			return character(ref.Name, time.Now(), 80, 600, "TOMB"), nil
		},
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /app/dashboard = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Live") {
		t.Error("the character that does exist is not shown")
	}
	if strings.Contains(body, "Deleted") {
		t.Error("a character Blizzard reports as gone was rendered anyway")
	}
	if strings.Contains(body, "could not be loaded") {
		t.Error("a character that no longer exists produced an incomplete-data notice")
	}
}

// TestBlizzardUnavailable is FR-010: a clear retry-able error, never a raw
// failure, with Blizzard's own Retry-After honoured.
func TestBlizzardUnavailable(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Maintank"),
		profileFor: func(blizzard.CharacterRef) (blizzard.Character, error) {
			return blizzard.Character{}, &blizzard.APIError{
				Endpoint:   "character-profile-summary",
				StatusCode: http.StatusTooManyRequests,
				Outcome:    blizzard.OutcomeUnavailable,
				RetryAfter: 30 * time.Second,
			}
		},
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")

	body := rec.Body.String()
	if !strings.Contains(body, "not responding") {
		t.Error("expected a retry-able error message")
	}
	if !strings.Contains(body, "Try again") {
		t.Error("the error page offers no way to retry (FR-010)")
	}
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q, want 30", got)
	}
}

// TestRevokedTokenPromptsReLogin is FR-012.
func TestRevokedTokenPromptsReLogin(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Maintank"),
		profileFor: func(blizzard.CharacterRef) (blizzard.Character, error) {
			return blizzard.Character{}, &blizzard.APIError{
				Endpoint:   "character-profile-summary",
				StatusCode: http.StatusUnauthorized,
				Outcome:    blizzard.OutcomeRevoked,
			}
		},
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")

	if rec.Code != http.StatusFound {
		t.Errorf("status = %d, want a 302 to re-login", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "reauth") {
		t.Errorf("Location = %q, want a re-authentication prompt", loc)
	}
	if strings.Contains(rec.Body.String(), "Maintank") {
		t.Error("stale character data was rendered after the token was revoked")
	}
}

// TestNoStoreOnAuthenticatedPages is scenario 4's mechanism.
func TestNoStoreOnAuthenticatedPages(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Maintank"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return character(ref.Name, time.Now(), 80, 600, "TOMB"), nil
		},
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control = %q, want it to contain no-store", got)
	}
}

// TestFetchesLiveOnEveryView is FR-016: no caching between views.
func TestFetchesLiveOnEveryView(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Maintank"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return character(ref.Name, time.Now(), 80, 600, "TOMB"), nil
		},
	}
	h := stack(t, fake, true)

	const views = 3
	for i := 0; i < views; i++ {
		if rec := get(t, h, "/app/dashboard"); rec.Code != http.StatusOK {
			t.Fatalf("view %d = %d, want 200", i+1, rec.Code)
		}
	}

	// One character call per view. A cache would make this 1.
	if got := fake.calls.Load(); got != views {
		t.Errorf("character fetches = %d, want %d (one per view; FR-016 forbids caching)",
			got, views)
	}
}

// TestDashboardRequiresGuild pins the declaration the core gates on. If this
// ever flips to false the site silently opens to any Battle.net user.
func TestDashboardRequiresGuild(t *testing.T) {
	app, err := dashboard.New(platform.Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("dashboard.New() error = %v", err)
	}

	meta := app.Meta()
	if !meta.RequiresGuild {
		t.Error("RequiresGuild = false; FR-013a requires the dashboard to be guild-gated")
	}
	if meta.RoutePrefix != "/app/"+meta.Slug {
		t.Errorf("RoutePrefix = %q, want /app/%s", meta.RoutePrefix, meta.Slug)
	}
}

// TestDashboardCardsShowSeasonStanding: the dashboard fetches each character's
// rating and raid progress itself -- the core's fan-out does not -- and the
// card shows them, omitting the rows for a character with none.
func TestDashboardCardsShowSeasonStanding(t *testing.T) {
	when := time.Date(2026, 9, 14, 7, 5, 0, 0, time.UTC)
	fake := &fakeBlizzard{
		refs: refs("Nekromoo", "Fresh"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			// Nekromoo played last, so the panel is Nekromoo's: FR-006 would
			// otherwise break the tie alphabetically and select Fresh.
			played := when
			if ref.Name == "Nekromoo" {
				played = when.Add(time.Hour)
			}
			return character(ref.Name, played, 90, 311, "TOMB"), nil
		},
		ratingFor: func(ref blizzard.CharacterRef) (int, error) {
			if ref.Name == "Nekromoo" {
				return 2431, nil
			}
			return 0, nil
		},
		raidsFor: func(ref blizzard.CharacterRef) ([]blizzard.RaidProgress, error) {
			if ref.Name != "Nekromoo" {
				return nil, nil
			}
			return []blizzard.RaidProgress{{Name: "Liberation of Undermine",
				Modes: []blizzard.RaidMode{{Difficulty: "MYTHIC", Completed: 3, Total: 8}}}}, nil
		},
	}

	body := html.UnescapeString(get(t, stack(t, fake, true), "/app/dashboard").Body.String())

	for _, want := range []string{
		"Mythic+ rating", "2431", "Liberation of Undermine", "3/8 M",
		// Names wear their class colour: in the rail, on the card, and on the
		// Armory panel's heading.
		`class="row-name class-name cls-warrior"`,
		`<a class="class-name cls-warrior" href=`,
		`<h2 class="class-name cls-warrior">Nekromoo</h2>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the dashboard is missing %q", want)
		}
	}
	// Nekromoo's card and the Armory panel both show the rating; Fresh's card
	// must not show a zero. So: exactly two rating rows.
	if n := strings.Count(body, "Mythic+ rating"); n != 2 {
		t.Errorf("found %d rating rows, want 2 (card + panel); an unrated character must not get a zero", n)
	}
}
