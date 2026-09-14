package guild

import (
	"bytes"
	"context"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

func render(t *testing.T, v view) string {
	t.Helper()

	// The app's own constructor, not a hand-rolled parse: the page references a
	// partial that lives in another package, and a test that parsed only
	// guild.html would be testing a template that does not exist in production.
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parsing the template: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "guild.html", v); err != nil {
		t.Fatalf("executing the template: %v", err)
	}
	return buf.String()
}

func appWith(ranks []string) *App {
	return &App{deps: platform.Deps{
		Guild: platform.GuildConfig{Name: "TOMB", RealmSlug: "elune", Ranks: ranks},
	}}
}

// TestGroupsFollowTheRoster covers the shape of the rail: one heading per rank,
// members under it, in the order the roster arrived.
//
// The grouping is done here and the ORDERING is not — the client sorts, and
// doing it twice would be two places to get it wrong.
func TestGroupsFollowTheRoster(t *testing.T) {
	a := appWith([]string{"Guild Master", "Officer", "Member", "Trainee"})

	groups := a.group([]blizzard.GuildMember{
		{Name: "Nekromoo", Rank: 0, Level: 90, Class: "Death Knight"},
		{Name: "Alpha", Rank: 1, Level: 90},
		{Name: "Beta", Rank: 1, Level: 90},
		{Name: "Zed", Rank: 3, Level: 71},
	}, nil)

	if len(groups) != 3 {
		t.Fatalf("got %d rank groups, want 3 (ranks 0, 1 and 3)", len(groups))
	}
	if groups[0].Label != "Guild Master" || groups[2].Label != "Trainee" {
		t.Errorf("labels = %q..%q, want Guild Master..Trainee", groups[0].Label, groups[2].Label)
	}
	if len(groups[1].Members) != 2 {
		t.Errorf("rank 1 has %d members, want 2", len(groups[1].Members))
	}

	// A rank nobody holds gets no heading: rank 2 is skipped entirely rather
	// than rendering an empty "Member" group.
	for _, g := range groups {
		if g.Label == "Member" {
			t.Error("an unheld rank produced an empty group")
		}
	}
}

// TestRosterRendersRankHeadings checks the rail itself, since the headings are
// what make the order legible at all.
func TestRosterRendersRankHeadings(t *testing.T) {
	a := appWith([]string{"Guild Master", "Officer"})
	v := view{
		Groups: a.group([]blizzard.GuildMember{
			{Name: "Nekromoo", Rank: 0, Level: 90, Class: "Death Knight", RealmName: "Area 52"},
			{Name: "Lazzlowe", Rank: 1, Level: 90, Class: "Paladin", RealmName: "Area 52"},
		}, nil),
		Total: 2,
	}

	body := html.UnescapeString(render(t, v))

	for _, want := range []string{
		"Guild Master", "Officer",
		"Nekromoo", "Lazzlowe",
		"Death Knight", "Paladin", "Area 52",
		"2 characters on the roster",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the roster is missing %q", want)
		}
	}

	// The guild master must appear above the officer, or the ordering that the
	// whole page is about is invisible.
	if strings.Index(body, "Nekromoo") > strings.Index(body, "Lazzlowe") {
		t.Error("the guild master is not at the top of the roster")
	}
}

// TestRosterRowsBehaveLikeCharacterRows is the point of this page reusing the
// character rail rather than looking like it.
//
// The hover card is not styling an app can opt into halfway: the stylesheet
// reveals .character-card.popover inside a .character-row on :hover and
// :focus-within, and it needs the row to be focusable for the keyboard half to
// work. A row with the right class and none of the rest highlights on hover and
// then does nothing, which is exactly what it did.
func TestRosterRowsBehaveLikeCharacterRows(t *testing.T) {
	a := appWith([]string{"Guild Master", "Officer"})
	v := view{
		Groups: a.group([]blizzard.GuildMember{
			{Name: "Nekromoo", Rank: 0, Level: 90, Class: "Death Knight", RealmName: "Area 52"},
			{Name: "Lazzlowe", Rank: 1, Level: 90, Class: "Paladin", RealmName: "Area 52"},
		}, nil),
		Total: 2,
	}

	body := render(t, v)

	// Every row focusable, or tabbing the roster reveals nothing.
	rows := strings.Count(body, `class="character-row"`)
	focusable := strings.Count(body, `class="character-row" tabindex="0"`)
	if rows == 0 || rows != focusable {
		t.Errorf("%d rows but %d focusable; the card is keyboard-unreachable on the rest",
			rows, focusable)
	}

	// Every row carries a card, using the same classes the stylesheet keys off.
	if cards := strings.Count(body, "character-card popover"); cards != rows {
		t.Errorf("%d rows but %d cards; a row without one highlights and then does nothing",
			rows, cards)
	}

	// The card names the rank, so a card opened over another group still says
	// which rank it belongs to.
	if !strings.Contains(body, `<p class="badge">Guild Master</p>`) {
		t.Error("the card does not name the member's rank")
	}
}

// TestRosterIsOnePanel pins the rail's shape against the character rail it is
// meant to match.
//
// Every rank group used to be its own bordered list, which rendered as five
// stacked boxes rather than one roster. The chrome belongs to the wrapper now
// and the rank headings divide it from the inside.
func TestRosterIsOnePanel(t *testing.T) {
	a := appWith([]string{"Guild Master", "Officer", "Member"})
	v := view{
		Groups: a.group([]blizzard.GuildMember{
			{Name: "Cwds", Rank: 0, Level: 90},
			{Name: "Arcanost", Rank: 1, Level: 90},
			{Name: "Azelora", Rank: 1, Level: 90},
			{Name: "Someone", Rank: 2, Level: 71},
		}, nil),
		Total: 4,
	}

	body := render(t, v)

	if n := strings.Count(body, `class="roster-groups"`); n != 1 {
		t.Errorf("found %d roster panels, want exactly 1 wrapping every rank", n)
	}

	// Three ranks are held, so three headings and three lists -- inside the one
	// panel, which is what stops them reading as three separate boxes.
	if n := strings.Count(body, `class="rank-heading"`); n != 3 {
		t.Errorf("found %d rank headings, want 3", n)
	}

	panel := body[strings.Index(body, `class="roster-groups"`):]
	panel = panel[:strings.Index(panel, `class="dashboard-main"`)]
	if n := strings.Count(panel, `class="character-list"`); n != 3 {
		t.Errorf("found %d lists inside the panel, want 3 -- one per held rank", n)
	}
}

// TestSummaryCountsTheRoster covers the panel beside the rail.
//
// Every number is counted from the roster already fetched, which is what makes
// the panel free: a guild summary that needed its own calls would be a second
// cost on the page every member lands on.
func TestSummaryCountsTheRoster(t *testing.T) {
	a := appWith([]string{"Guild Master", "Officer"})
	members := []blizzard.GuildMember{
		{Name: "Cwds", Rank: 0, Level: 90, Class: "Monk"},
		{Name: "Arcanost", Rank: 1, Level: 90, Class: "Mage"},
		{Name: "Azelora", Rank: 1, Level: 90, Class: "Mage"},
		{Name: "Boodytv", Rank: 1, Level: 64, Class: "Hunter"},
	}
	groups := a.group(members, nil)
	sum := a.summarise(members, groups)

	if sum == nil {
		t.Fatal("no summary for a roster with members in it")
	}
	if sum.Total != 4 {
		t.Errorf("Total = %d, want 4", sum.Total)
	}

	// The cap is derived, not hardcoded: it moves every expansion, and a
	// hardcoded level would quietly start counting nothing.
	if sum.CapLevel != 90 {
		t.Errorf("CapLevel = %d, want 90 (the highest level present)", sum.CapLevel)
	}
	if sum.AtCap != 3 {
		t.Errorf("AtCap = %d, want 3 (the level 64 is below it)", sum.AtCap)
	}

	// Rank counts come from the groups, so the panel and the rail cannot
	// disagree about how many officers there are.
	if len(sum.Ranks) != 2 || sum.Ranks[0].Count != 1 || sum.Ranks[1].Count != 3 {
		t.Errorf("ranks = %v, want Guild Master 1 and Officer 3", sum.Ranks)
	}

	// Commonest class first.
	if len(sum.Classes) != 3 || sum.Classes[0].Label != "Mage" || sum.Classes[0].Count != 2 {
		t.Errorf("classes = %v, want Mage 2 first", sum.Classes)
	}
}

// TestClassOrderIsStable: map iteration is random, so equal counts must be
// broken alphabetically. A list that reshuffles itself on every refresh looks
// broken even when the numbers are right.
func TestClassOrderIsStable(t *testing.T) {
	a := appWith(nil)
	members := []blizzard.GuildMember{
		{Name: "A", Rank: 0, Level: 90, Class: "Warrior"},
		{Name: "B", Rank: 0, Level: 90, Class: "Mage"},
		{Name: "C", Rank: 0, Level: 90, Class: "Druid"},
	}

	var first []string
	for i := 0; i < 20; i++ {
		sum := a.summarise(members, a.group(members, nil))
		var order []string
		for _, c := range sum.Classes {
			order = append(order, c.Label)
		}
		if first == nil {
			first = order
			continue
		}
		for j := range order {
			if order[j] != first[j] {
				t.Fatalf("class order changed between runs: %v then %v", first, order)
			}
		}
	}
	if first[0] != "Druid" {
		t.Errorf("equal counts should break alphabetically, got %v", first)
	}
}

// TestSummaryRenders checks the panel reaches the page, since an empty right
// hand side is what this was added to fix.
func TestSummaryRenders(t *testing.T) {
	a := appWith([]string{"Guild Master"})
	members := []blizzard.GuildMember{{Name: "Cwds", Rank: 0, Level: 90, Class: "Monk"}}
	v := view{
		Groups:  a.group(members, nil),
		Total:   1,
		Summary: a.summarise(members, a.group(members, nil)),
	}

	body := html.UnescapeString(render(t, v))

	for _, want := range []string{"guild-summary", "TOMB", "Characters", "At level 90", "By rank", "By class", "Monk"} {
		if !strings.Contains(body, want) {
			t.Errorf("the summary panel is missing %q", want)
		}
	}
}

// TestNoSummaryWithoutARoster: an unavailable roster must not render a panel
// full of zeroes, which would read as a guild with nobody in it.
func TestNoSummaryWithoutARoster(t *testing.T) {
	a := appWith(nil)
	if sum := a.summarise(nil, nil); sum != nil {
		t.Errorf("summarised an empty roster into %v", sum)
	}

	if strings.Contains(render(t, view{Unavailable: true}), "guild-summary") {
		t.Error("a summary panel rendered for an unavailable roster")
	}
}

// TestUnnamedRanksSaySo: an unconfigured rank shows as a number, and the page
// explains why rather than leaving it looking broken.
func TestUnnamedRanksSaySo(t *testing.T) {
	a := appWith(nil)
	v := view{
		RanksUnnamed: true,
		Groups:       a.group([]blizzard.GuildMember{{Name: "Someone", Rank: 4, Level: 80}}, nil),
		Total:        1,
	}

	body := html.UnescapeString(render(t, v))

	if !strings.Contains(body, "Rank 4") {
		t.Error("an unconfigured rank should show its number rather than a guess")
	}
	if !strings.Contains(body, "TOMB_GUILD_RANKS") {
		t.Error("the page does not say how to name the ranks")
	}
	if !strings.Contains(body, "1 character on the roster") {
		t.Error("the total is not singular for one character")
	}
}

// TestUnavailableRosterSaysSo: losing the roster must not render an empty guild,
// which would read as "TOMB has no members".
func TestUnavailableRosterSaysSo(t *testing.T) {
	body := render(t, view{Unavailable: true})

	if !strings.Contains(body, "roster is unavailable") {
		t.Error("a failed roster fetch does not explain itself")
	}
	if strings.Contains(body, "character-list") {
		t.Error("an empty rail was rendered for an unavailable roster")
	}
}

// TestMetaIsTheHome pins the registration contract: this app is what the TOMB
// brand link leads to, and it is deliberately absent from the navigation.
//
// Absent because it would otherwise be the same destination listed twice, right
// next to itself.
func TestMetaIsTheHome(t *testing.T) {
	meta := (&App{}).Meta()

	if meta.RoutePrefix != "/app/"+meta.Slug {
		t.Errorf("RoutePrefix = %q, must be /app/ + %q", meta.RoutePrefix, meta.Slug)
	}
	if !meta.RequiresGuild {
		t.Error("RequiresGuild is false; a guild roster is guild business")
	}
	if !meta.Home {
		t.Error("Home is false, so signing in would not land here")
	}
	if meta.NavLabel != "" {
		t.Errorf("NavLabel = %q, want empty: the brand link already goes here", meta.NavLabel)
	}
}

// --- Clicking a name ---------------------------------------------------------

// fakeClient is a blizzard.Client that records which characters were asked for.
//
// The recording is the point: the interesting property of ?c= is not only what
// it renders but what it REFUSES to fetch, and that is invisible from the HTML.
type fakeClient struct {
	blizzard.Client // embedded: the methods these tests never call stay nil

	mu       sync.Mutex
	profiled []blizzard.CharacterRef
	profile  blizzard.Character
	profErr  error

	// byName answers CharacterProfile per character when set, so a snapshot
	// test can give each member their own spec and item level; otherwise
	// every call returns profile.
	byName map[string]blizzard.Character

	roster     []blizzard.GuildMember
	rosterErr  error
	rosterGets atomic.Int32

	// ratingFor gives a member a Mythic+ rating by lowercase name; absent
	// means unrated. Raids are not driven here: the row rendering is covered
	// by the armory and template tests, and the snapshot only needs to prove
	// it carries what it fetched.
	ratingFor map[string]int
}

func (f *fakeClient) MythicPlusRating(_ context.Context, _ string, ref blizzard.CharacterRef) (int, error) {
	return f.ratingFor[strings.ToLower(ref.Name)], nil
}

func (f *fakeClient) RaidProgression(context.Context, string, blizzard.CharacterRef) ([]blizzard.RaidProgress, error) {
	return nil, nil
}

func (f *fakeClient) CharacterProfile(_ context.Context, _ string, ref blizzard.CharacterRef) (blizzard.Character, error) {
	f.mu.Lock()
	f.profiled = append(f.profiled, ref)
	f.mu.Unlock()
	if f.profErr != nil {
		return blizzard.Character{}, f.profErr
	}
	if f.byName != nil {
		c, ok := f.byName[strings.ToLower(ref.Name)]
		if !ok {
			return blizzard.Character{}, errors.New("no such character")
		}
		return c, nil
	}
	return f.profile, nil
}

func (f *fakeClient) GuildRoster(context.Context, string, string, string) ([]blizzard.GuildMember, error) {
	f.rosterGets.Add(1)
	return f.roster, f.rosterErr
}

func (f *fakeClient) profileCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.profiled)
}

func (f *fakeClient) CharacterMedia(context.Context, string, blizzard.CharacterRef) (blizzard.Media, error) {
	return blizzard.Media{}, errors.New("no media in this test")
}

func (f *fakeClient) CharacterEquipment(context.Context, string, blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
	return nil, errors.New("no equipment in this test")
}

// selecting drives the real handler path: ?c=<key> against a known roster.
func selecting(t *testing.T, f *fakeClient, ranks []string, key string, members []blizzard.GuildMember) view {
	t.Helper()

	a := &App{deps: platform.Deps{
		Guild:    platform.GuildConfig{Name: "TOMB", RealmSlug: "elune", Ranks: ranks},
		Blizzard: f,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}

	r := httptest.NewRequest(http.MethodGet, "/app/guild?c="+key, nil)
	r = r.WithContext(platform.ContextWithSession(r.Context(), auth.Session{AccessToken: "t"}))

	v := view{Groups: a.group(members, nil), Total: len(members)}
	a.selectMember(r, &v, members, key)
	return v
}

var twoMembers = []blizzard.GuildMember{
	{Name: "Nekromoo", Rank: 0, Level: 90, Class: "Death Knight", RealmSlug: "area-52"},
	{Name: "Lazzlowe", Rank: 1, Level: 90, Class: "Paladin", RealmSlug: "elune"},
}

// TestNamesLinkToTheirArmory is the thing the roster was missing: the name on a
// guild card is a link, exactly as it is on My Characters.
func TestNamesLinkToTheirArmory(t *testing.T) {
	a := appWith([]string{"Guild Master", "Officer"})
	body := render(t, view{Groups: a.group(twoMembers, nil), Total: 2, Home: routePrefix})

	// %2f, not "/": html/template escapes the separator inside a URL query,
	// and r.URL.Query().Get decodes it again on the way back in. Asserting the
	// unescaped form would fail against markup that is entirely correct.
	for _, want := range []string{
		"href=\"?c=area-52%2fnekromoo\"",
		"href=\"?c=elune%2flazzlowe\"",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the roster is missing %s; the name is not clickable", want)
		}
	}

	// Realm-qualified, because a character name is only unique within a realm.
	// A key of just the name would collide the moment two realms are on one
	// roster -- which, on this roster, they already are.
	if strings.Contains(body, "href=\"?c=nekromoo\"") {
		t.Error("the key is not realm-qualified, so cross-realm names will collide")
	}
}

// TestSelectingAMemberRendersTheArmoryPanel proves the guild page renders the
// SAME panel as My Characters, not a guild-flavoured imitation of it.
func TestSelectingAMemberRendersTheArmoryPanel(t *testing.T) {
	f := &fakeClient{profile: blizzard.Character{
		Name: "Nekromoo", RealmSlug: "area-52", RealmName: "Area 52",
		Class: "Death Knight", ActiveSpec: "Frost", Level: 90, AverageItemLevel: 606,
	}}

	v := selecting(t, f, []string{"Guild Master", "Officer"}, "area-52/nekromoo", twoMembers)
	if v.Selected == nil {
		t.Fatal("no panel was built for a member who is on the roster")
	}

	// The rank becomes the badge, which is the one line that differs between
	// this page's panel and the dashboard's.
	if v.Selected.Badge != "Guild Master" {
		t.Errorf("badge = %q, want the member's rank", v.Selected.Badge)
	}

	// The summary steps aside: two panels competing for one column is not what
	// clicking a name asked for.
	if v.Summary != nil {
		t.Error("the guild summary is still set alongside a selected member")
	}

	body := render(t, v)
	for _, want := range []string{"class=\"armory\"", "Frost", "606", "Area 52", "Guild overview"} {
		if !strings.Contains(body, want) {
			t.Errorf("the rendered panel is missing %q", want)
		}
	}
}

// TestSelectedRowIsMarked: the rail has to show which member the panel belongs
// to, or the page looks unrelated to the click that produced it.
func TestSelectedRowIsMarked(t *testing.T) {
	f := &fakeClient{profile: blizzard.Character{Name: "Lazzlowe", RealmSlug: "elune"}}
	v := selecting(t, f, []string{"Guild Master", "Officer"}, "elune/lazzlowe", twoMembers)

	var marked []string
	for _, g := range v.Groups {
		for _, m := range g.Members {
			if m.Selected {
				marked = append(marked, m.Name)
			}
		}
	}
	if len(marked) != 1 || marked[0] != "Lazzlowe" {
		t.Fatalf("marked rows = %v, want exactly [Lazzlowe]", marked)
	}
	if !strings.Contains(render(t, v), "is-selected") {
		t.Error("the selected row carries no is-selected class")
	}
}

// TestOffRosterCharacterIsNeverFetched is the security-shaped one.
//
// Blizzard will return a profile for ANY character in the region. If ?c= were
// passed through, this page would be an open proxy for the character API --
// anyone could walk arbitrary names through it on our rate limit, from a URL
// that looks like part of the guild site. The roster is the allow-list, and
// this asserts that no call leaves the process for a name that is not on it.
func TestOffRosterCharacterIsNeverFetched(t *testing.T) {
	f := &fakeClient{profile: blizzard.Character{Name: "Someone"}}
	v := selecting(t, f, []string{"Guild Master"}, "silvermoon/stranger", twoMembers)

	if len(f.profiled) != 0 {
		t.Errorf("fetched %v for a character who is not on the roster", f.profiled)
	}
	if v.Selected != nil {
		t.Error("an off-roster character produced a panel")
	}
	if v.NotFound == "" {
		t.Error("an off-roster character said nothing; the link would look ignored")
	}
}

// TestUnreachableProfileIsNotMistakenForAbsence keeps the two failures apart.
//
// "Not on the roster" and "Blizzard is having a bad minute" have different
// causes and different advice, and collapsing them tells a member their
// guildmate left the guild because an API call timed out.
func TestUnreachableProfileIsNotMistakenForAbsence(t *testing.T) {
	f := &fakeClient{profErr: errors.New("blizzard is down")}
	v := selecting(t, f, []string{"Guild Master"}, "area-52/nekromoo", twoMembers)

	if v.Selected != nil {
		t.Error("a failed profile fetch still produced a panel")
	}
	if v.NotFound != "" {
		t.Errorf("NotFound = %q; a fetch failure is not an absent member", v.NotFound)
	}
	if v.Unreachable != "Nekromoo" {
		t.Errorf("Unreachable = %q, want Nekromoo", v.Unreachable)
	}
}

// TestKeyLookupIsCaseInsensitive: the key is lowercased into the href, but a
// bookmark or a hand-typed URL may not be.
func TestKeyLookupIsCaseInsensitive(t *testing.T) {
	f := &fakeClient{profile: blizzard.Character{Name: "Nekromoo", RealmSlug: "area-52"}}
	v := selecting(t, f, []string{"Guild Master"}, "Area-52/Nekromoo", twoMembers)

	if v.Selected == nil {
		t.Fatal("a differently-cased key found nobody")
	}
	if len(f.profiled) != 1 || f.profiled[0].Name != "Nekromoo" {
		t.Errorf("fetched %v, want the roster's own spelling of the name", f.profiled)
	}
}

// TestBackLinkIsAbsolute: from /app/guild?c=x a relative href resolves against
// /app/, not back to the roster. This is the kind of thing that looks right in
// the markup and lands on a 404 in a browser.
func TestBackLinkIsAbsolute(t *testing.T) {
	f := &fakeClient{profile: blizzard.Character{Name: "Nekromoo", RealmSlug: "area-52"}}
	v := selecting(t, f, []string{"Guild Master"}, "area-52/nekromoo", twoMembers)
	v.Home = routePrefix

	if got := render(t, v); !strings.Contains(got, "href=\"/app/guild\"") {
		t.Error("the way back out of a member's panel is not an absolute path")
	}
}

// --- The card, and the snapshot behind it -----------------------------------

func signedIn(t *testing.T) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/app/guild", nil)
	return r.WithContext(platform.ContextWithSession(r.Context(), auth.Session{AccessToken: "t"}))
}

func snapshotApp(f *fakeClient, refresh time.Duration) *App {
	return &App{
		deps: platform.Deps{
			Guild:    platform.GuildConfig{Name: "TOMB", RealmSlug: "elune", Ranks: []string{"Guild Master", "Officer"}},
			Blizzard: f,
			Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
		refreshEvery: refresh,
	}
}

var profiled = map[string]blizzard.Character{
	"nekromoo": {Name: "Nekromoo", RealmSlug: "area-52", RealmName: "Area 52", Class: "Death Knight",
		ActiveSpec: "Blood", Level: 90, AverageItemLevel: 311, LastLogin: time.Date(2026, 9, 14, 7, 5, 0, 0, time.UTC)},
	"lazzlowe": {Name: "Lazzlowe", RealmSlug: "elune", RealmName: "Elune", Class: "Paladin",
		ActiveSpec: "Retribution", Level: 90, AverageItemLevel: 298, LastLogin: time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)},
}

// byKey re-keys a name-keyed profile map the way group expects it -- by
// memberKey, realm first -- which is also how load builds the real one.
func byKey(m map[string]blizzard.Character) map[string]memberDetail {
	out := make(map[string]memberDetail, len(m))
	for _, c := range m {
		out[c.RealmSlug+"/"+strings.ToLower(c.Name)] = memberDetail{Character: c}
	}
	return out
}

// TestCardsMatchMyCharacters is the request that produced this: the guild rail's
// card was missing specialisation, item level and last played, which My
// Characters shows. The rows the two cards have must be the same rows.
func TestCardsMatchMyCharacters(t *testing.T) {
	a := snapshotApp(&fakeClient{}, time.Hour)
	groups := a.group(twoMembers, byKey(profiled))

	nek := groups[0].Members[0]
	if nek.ActiveSpec != "Blood" || nek.AverageItemLevel != 311 || nek.LastLogin != "14 Sep 2026, 07:05 UTC" {
		t.Errorf("card = spec %q ilvl %d played %q; want Blood, 311, 14 Sep 2026, 07:05 UTC",
			nek.ActiveSpec, nek.AverageItemLevel, nek.LastLogin)
	}
	// The profile's realm display name, not the roster's slug.
	if nek.Realm != "Area 52" {
		t.Errorf("realm = %q, want the display name from the profile", nek.Realm)
	}

	body := html.UnescapeString(render(t, view{Groups: groups, Total: 2, Home: routePrefix}))
	for _, want := range []string{
		"Specialization", "Blood", "Retribution",
		"Average item level", "311", "298",
		"Last played", "14 Sep 2026, 07:05 UTC",
		"Area 52 · <TOMB>", // the realm line reads exactly as it does on My Characters
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the guild card is missing %q", want)
		}
	}
}

// TestMissingProfileKeepsTheRosterRow: a member Blizzard would not serve a
// profile for is still on the roster, with what the roster knows and nothing
// invented for the rest.
func TestMissingProfileKeepsTheRosterRow(t *testing.T) {
	a := snapshotApp(&fakeClient{}, time.Hour)
	only := byKey(map[string]blizzard.Character{"nekromoo": profiled["nekromoo"]})
	groups := a.group(twoMembers, only)

	laz := groups[1].Members[0]
	if laz.Name != "Lazzlowe" || laz.Level != 90 || laz.Class != "Paladin" {
		t.Errorf("roster fields lost: %+v", laz)
	}
	if laz.ActiveSpec != "" || laz.AverageItemLevel != 0 || laz.LastLogin != "" {
		t.Errorf("profile fields invented for a member with no profile: %+v", laz)
	}

	// And the card omits the rows rather than printing a zero.
	body := render(t, view{Groups: groups, Total: 2, Home: routePrefix})
	if strings.Count(body, "Average item level") != 1 {
		t.Errorf("expected exactly one item-level row (Nekromoo's), the card for the profile-less member should omit it")
	}
}

// TestSnapshotLoadsOnceAndIsReused: the first view pays for the roster and
// every profile; the next view within the interval pays for nothing.
func TestSnapshotLoadsOnceAndIsReused(t *testing.T) {
	f := &fakeClient{roster: twoMembers, byName: profiled}
	a := snapshotApp(f, time.Hour)

	first, err := a.snapshot(signedIn(t))
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if f.rosterGets.Load() != 1 || f.profileCalls() != len(twoMembers) {
		t.Fatalf("first view made %d roster and %d profile calls; want 1 and %d",
			f.rosterGets.Load(), f.profileCalls(), len(twoMembers))
	}
	if len(first.details) != 2 {
		t.Errorf("snapshot holds %d member details, want 2", len(first.details))
	}

	second, err := a.snapshot(signedIn(t))
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if second != first {
		t.Error("a fresh snapshot was replaced rather than reused")
	}
	if f.rosterGets.Load() != 1 || f.profileCalls() != len(twoMembers) {
		t.Errorf("a view inside the interval fetched again: %d roster, %d profile calls",
			f.rosterGets.Load(), f.profileCalls())
	}
}

// TestStaleSnapshotIsServedWhileRefreshing is the property that keeps the
// front page fast: a view after the interval gets the OLD snapshot at once,
// and the new one arrives behind it.
func TestStaleSnapshotIsServedWhileRefreshing(t *testing.T) {
	f := &fakeClient{roster: twoMembers, byName: profiled}
	a := snapshotApp(f, time.Hour)

	old, err := a.snapshot(signedIn(t))
	if err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	// Age it past the interval by hand.
	a.mu.Lock()
	a.snap.fetched = time.Now().Add(-2 * time.Hour)
	a.mu.Unlock()

	got, err := a.snapshot(signedIn(t))
	if err != nil {
		t.Fatalf("stale snapshot: %v", err)
	}
	if got != old {
		t.Error("a stale view waited for the refresh instead of being served the last snapshot")
	}

	// The refresh lands shortly after, on its own.
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		replaced := a.snap != old
		a.mu.Unlock()
		if replaced {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background refresh never replaced the stale snapshot")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if f.rosterGets.Load() != 2 {
		t.Errorf("roster fetched %d times, want 2 (load + one refresh)", f.rosterGets.Load())
	}
}

// TestFailedRefreshKeepsTheSnapshot: a bad minute at Blizzard must not empty
// the front page. The stale snapshot stands until a refresh succeeds.
func TestFailedRefreshKeepsTheSnapshot(t *testing.T) {
	f := &fakeClient{roster: twoMembers, byName: profiled}
	a := snapshotApp(f, time.Hour)

	old, err := a.snapshot(signedIn(t))
	if err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	f.rosterErr = errors.New("blizzard is down")
	a.mu.Lock()
	a.snap.fetched = time.Now().Add(-2 * time.Hour)
	a.mu.Unlock()

	if _, err := a.snapshot(signedIn(t)); err != nil {
		t.Fatalf("a stale view errored instead of serving the last snapshot: %v", err)
	}

	// Wait for the refresh goroutine to give up.
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		busy := a.refreshing
		a.mu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.mu.Lock()
	kept := a.snap == old
	a.mu.Unlock()
	if !kept {
		t.Error("a failed refresh replaced the snapshot")
	}
}

// TestFirstViewsShareOneLoad: with nothing cached yet, concurrent views must
// not each start their own two-hundred-call fan-out.
func TestFirstViewsShareOneLoad(t *testing.T) {
	f := &fakeClient{roster: twoMembers, byName: profiled}
	a := snapshotApp(f, time.Hour)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := a.snapshot(signedIn(t)); err != nil {
				t.Errorf("snapshot: %v", err)
			}
		})
	}
	wg.Wait()

	if f.rosterGets.Load() != 1 {
		t.Errorf("eight first views fetched the roster %d times, want 1", f.rosterGets.Load())
	}
}

// TestNoSnapshotAndNoRosterIsUnavailable: with nothing kept and Blizzard down,
// the page says so rather than showing an empty guild.
func TestNoSnapshotAndNoRosterIsUnavailable(t *testing.T) {
	f := &fakeClient{rosterErr: errors.New("down")}
	a := snapshotApp(f, time.Hour)

	if _, err := a.snapshot(signedIn(t)); err == nil {
		t.Error("expected an error with no snapshot and no roster")
	}
}

// --- Season standing on the card --------------------------------------------

// TestCardsShowSeasonStanding: the Mythic+ rating and raid progress reach the
// guild card, from the snapshot, the same way the profile fields do.
func TestCardsShowSeasonStanding(t *testing.T) {
	a := snapshotApp(&fakeClient{}, time.Hour)
	details := byKey(profiled)
	nek := details["area-52/nekromoo"]
	nek.MythicPlusRating = 2431
	nek.Raids = []blizzard.RaidProgress{{
		Name: "Liberation of Undermine",
		Modes: []blizzard.RaidMode{
			{Difficulty: "HEROIC", Completed: 8, Total: 8},
			{Difficulty: "MYTHIC", Completed: 3, Total: 8},
		},
	}}
	details["area-52/nekromoo"] = nek

	groups := a.group(twoMembers, details)
	body := html.UnescapeString(render(t, view{Groups: groups, Total: 2, Home: routePrefix}))

	for _, want := range []string{"Mythic+ rating", "2431", "Liberation of Undermine", "8/8 H · 3/8 M"} {
		if !strings.Contains(body, want) {
			t.Errorf("the guild card is missing %q", want)
		}
	}
	// Lazzlowe has no standing: exactly one rating row on the page.
	if n := strings.Count(body, "Mythic+ rating"); n != 1 {
		t.Errorf("found %d rating rows, want 1; an unrated member must not get a zero", n)
	}
}

// TestSnapshotCarriesSeasonStanding proves the load path fetches it, not only
// that the template can show it.
func TestSnapshotCarriesSeasonStanding(t *testing.T) {
	f := &fakeClient{roster: twoMembers, byName: profiled, ratingFor: map[string]int{"nekromoo": 2431}}
	a := snapshotApp(f, time.Hour)

	snap, err := a.snapshot(signedIn(t))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := snap.details["area-52/nekromoo"].MythicPlusRating; got != 2431 {
		t.Errorf("Nekromoo's rating = %d, want 2431", got)
	}
	if got := snap.details["elune/lazzlowe"].MythicPlusRating; got != 0 {
		t.Errorf("Lazzlowe's rating = %d, want 0 (unrated)", got)
	}
}
