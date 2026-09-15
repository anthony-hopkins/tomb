package guild

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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
	sum := a.summarise(members, groups, nil)

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
		sum := a.summarise(members, a.group(members, nil), nil)
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
		Summary: a.summarise(members, a.group(members, nil), nil),
	}

	body := html.UnescapeString(render(t, v))

	for _, want := range []string{"guild-summary", "TOMB", "Characters", "At level 90", "By class", "Monk"} {
		if !strings.Contains(body, want) {
			t.Errorf("the summary panel is missing %q", want)
		}
	}
}

// TestNoSummaryWithoutARoster: an unavailable roster must not render a panel
// full of zeroes, which would read as a guild with nobody in it.
func TestNoSummaryWithoutARoster(t *testing.T) {
	a := appWith(nil)
	if sum := a.summarise(nil, nil, nil); sum != nil {
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
	guild := platform.GuildConfig{Name: "TOMB", RealmSlug: "elune", Ranks: []string{"Guild Master", "Officer"}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &App{
		deps: platform.Deps{
			Guild:    guild,
			Blizzard: f,
			Logger:   logger,
			// The roster reaches the app through the core's cache now; a
			// fresh one per app, over the same fake, so counts still mean
			// what they say.
			Roster: &platform.RosterCache{Client: f, Guild: guild, Logger: logger, TTL: time.Hour},
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
		// The name wears its class colour, in the rail and on the card.
		`class="row-name class-name cls-death-knight"`,
		`<a class="class-name cls-paladin" href="?c=elune%2flazzlowe">`,
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
	// The roster is the cache's business now and is still fresh; what the
	// refresh redid is this app's own work, the profiles.
	if f.profileCalls() != 2*len(twoMembers) {
		t.Errorf("profiles fetched %d times, want %d (load + one refresh)", f.profileCalls(), 2*len(twoMembers))
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
	// The cache would keep serving the roster it holds, so to make this
	// app's refresh fail the roster has to be genuinely unobtainable: a cold
	// cache over a client that is down.
	f.rosterErr = errors.New("blizzard is down")
	a.deps.Roster = &platform.RosterCache{Client: f, Guild: a.deps.Guild, Logger: a.deps.Logger, TTL: time.Hour}
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

// --- Charts and leaderboards ------------------------------------------------

// TestBarsScaleToTheLongest: a bar chart of counts compares rows to each other,
// so the biggest row fills the track and the rest are read against it.
func TestBarsScaleToTheLongest(t *testing.T) {
	a := appWith([]string{"Guild Master", "Officer"})
	members := []blizzard.GuildMember{
		{Name: "A", Rank: 0, Level: 90, Class: "Mage"},
		{Name: "B", Rank: 1, Level: 90, Class: "Mage"},
		{Name: "C", Rank: 1, Level: 90, Class: "Death Knight"},
		{Name: "D", Rank: 1, Level: 64, Class: "Death Knight"},
		{Name: "E", Rank: 1, Level: 90, Class: "Mage"},
	}
	sum := a.summarise(members, a.group(members, nil), nil)

	if sum.Classes[0].Label != "Mage" || sum.Classes[0].Pct != 100 || sum.Classes[0].Class != "mage" {
		t.Errorf("first class bar = %+v, want Mage at 100%% with slug mage", sum.Classes[0])
	}
	if sum.Classes[1].Class != "death-knight" {
		t.Errorf("class slug = %q, want death-knight", sum.Classes[1].Class)
	}
	// 4 of 5 at the cap: 80%.
	if sum.CapPct != 80 {
		t.Errorf("CapPct = %d, want 80", sum.CapPct)
	}
}

// TestBoardsRankAndCut covers each board's order and that a member with
// nothing to rank on is absent rather than placed last. The cut itself is
// TestBoardsCutAtTen.
func TestBoardsRankAndCut(t *testing.T) {
	a := snapshotApp(&fakeClient{}, time.Hour)

	var members []blizzard.GuildMember
	details := map[string]memberDetail{}
	add := func(name string, ilvl, rating, m, h, n int) {
		mem := blizzard.GuildMember{Name: name, RealmSlug: "elune", Rank: 1, Level: 90, Class: "Rogue"}
		members = append(members, mem)
		d := memberDetail{Character: blizzard.Character{Name: name, RealmSlug: "elune", Class: "Rogue", AverageItemLevel: ilvl}}
		d.MythicPlusRating = rating
		if m+h+n > 0 {
			d.Raids = []blizzard.RaidProgress{{Name: "Raid", Modes: []blizzard.RaidMode{
				{Difficulty: "NORMAL", Completed: n, Total: 8},
				{Difficulty: "HEROIC", Completed: h, Total: 8},
				{Difficulty: "MYTHIC", Completed: m, Total: 8},
			}}}
		}
		details[memberKey(mem)] = d
	}
	add("Alpha", 300, 2000, 0, 8, 8)   // most heroic
	add("Bravo", 320, 0, 2, 8, 8)      // two mythic kills beat eight heroic
	add("Charlie", 310, 2500, 0, 0, 8) // normal only
	add("Delta", 305, 2400, 1, 3, 8)
	add("Echo", 290, 2100, 0, 0, 0) // never raided
	add("Foxtrot", 315, 0, 0, 0, 0)
	add("Golf", 0, 0, 0, 0, 0)                                                                  // nothing fetched worth ranking
	members = append(members, blizzard.GuildMember{Name: "Hotel", RealmSlug: "elune", Rank: 1}) // no detail at all

	sum := a.summarise(members, a.group(members, details), details)
	if len(sum.Boards) != 3 {
		t.Fatalf("got %d boards, want 3: %+v", len(sum.Boards), sum.Boards)
	}

	names := func(b boardView) []string {
		var out []string
		for _, e := range b.Entries {
			out = append(out, e.Name)
		}
		return out
	}

	ilvl := sum.Boards[0]
	if ilvl.Title != "Top item level" || strings.Join(names(ilvl), ",") != "Bravo,Foxtrot,Charlie,Delta,Alpha,Echo" {
		t.Errorf("item level board = %v; Golf has no item level and is absent", names(ilvl))
	}
	if ilvl.Entries[0].Value != "320" || ilvl.Entries[0].Rank != 1 || ilvl.Entries[0].Class != "rogue" {
		t.Errorf("first entry = %+v", ilvl.Entries[0])
	}

	rating := sum.Boards[1]
	if strings.Join(names(rating), ",") != "Charlie,Delta,Echo,Alpha" {
		t.Errorf("rating board = %v; the unrated must be absent, not last", names(rating))
	}

	bosses := sum.Boards[2]
	if strings.Join(names(bosses), ",") != "Bravo,Delta,Alpha,Charlie" {
		t.Errorf("bosses board = %v; mythic outranks heroic outranks normal", names(bosses))
	}
	if bosses.Entries[0].Value != "2M · 8H" || bosses.Entries[3].Value != "8N" {
		t.Errorf("boss labels = %q and %q, want 2M · 8H and 8N", bosses.Entries[0].Value, bosses.Entries[3].Value)
	}

	// The bar behind each row is its standing between the board's lowest
	// and highest, not a length from zero: 320 down to 290 runs from the
	// full row to the floor, with 305 halfway between.
	pcts := func(b boardView) []int {
		var out []int
		for _, e := range b.Entries {
			out = append(out, e.Pct)
		}
		return out
	}
	if got := pcts(ilvl); fmt.Sprint(got) != "[100 88 75 63 50 25]" {
		t.Errorf("item level bars = %v, want [100 88 75 63 50 25]", got)
	}
	// Bosses are ordered by difficulty but the bar is bosses down altogether:
	// Alpha's sixteen (3rd) outbars Delta's twelve (2nd), and that is right.
	if got := pcts(bosses); fmt.Sprint(got) != "[100 55 85 25]" {
		t.Errorf("bosses bars = %v, want [100 55 85 25]", got)
	}
}

// TestStandingFloorsAndTies: one entry, or a board where everyone is level,
// gets the full row; nobody gets less than the floor.
func TestStandingFloorsAndTies(t *testing.T) {
	for _, tc := range []struct{ score, lo, hi, want int }{
		{300, 300, 300, 100},
		{7, 7, 69, barFloor},
		{69, 7, 69, 100},
		{38, 7, 69, 63},
	} {
		if got := standing(tc.score, tc.lo, tc.hi); got != tc.want {
			t.Errorf("standing(%d, %d, %d) = %d, want %d", tc.score, tc.lo, tc.hi, got, tc.want)
		}
	}
}

// TestNoDetailNoBoards: a snapshot with no member detail yet gives no boards,
// and the page shows the summary without an empty leaderboard column.
func TestNoDetailNoBoards(t *testing.T) {
	a := appWith([]string{"Guild Master"})
	members := []blizzard.GuildMember{{Name: "A", Rank: 0, Level: 90, Class: "Mage"}}
	sum := a.summarise(members, a.group(members, nil), nil)
	if len(sum.Boards) != 0 {
		t.Errorf("boards = %+v, want none", sum.Boards)
	}
	body := render(t, view{Groups: a.group(members, nil), Summary: sum, Total: 1, Home: routePrefix})
	if strings.Contains(body, "leaderboards") {
		t.Error("an empty leaderboard column was rendered")
	}
}

// TestOverviewRendersChartsWithoutInlineStyles: the CSP allows no style
// attributes, so every chart must be attributes and classes. This holds the
// rendered markup to that, and checks the pieces are there at all.
func TestOverviewRendersChartsWithoutInlineStyles(t *testing.T) {
	f := &fakeClient{}
	a := snapshotApp(f, time.Hour)
	details := byKey(profiled)
	nek := details["area-52/nekromoo"]
	nek.MythicPlusRating = 2431
	details["area-52/nekromoo"] = nek

	groups := a.group(twoMembers, details)
	sum := a.summarise(twoMembers, groups, details)
	// Unescaped: html/template writes "+" as &#43; in text, and the check is
	// on what a member reads, not on the encoding.
	body := html.UnescapeString(render(t, view{Groups: groups, Summary: sum, Total: 2, Home: routePrefix}))

	if strings.Contains(body, "style=") {
		t.Error("the overview uses an inline style attribute, which the CSP blocks")
	}
	for _, want := range []string{
		`class="stat-value"`,
		`stroke-dasharray="100 100"`, // both at the cap
		`<rect class="bar-fill cls-death-knight" width="100%"`,
		`class="leaderboards"`,
		"Top item level", "Top Mythic+ rating",
		`href="?c=area-52%2fnekromoo"`,
		`class="dashboard-main dashboard-main--wide"`,
		// Leaderboard names are written in their class colour.
		`<a class="class-name cls-death-knight" href="?c=area-52%2fnekromoo">`,
		// And each row has the class's bar behind it, sized by attribute.
		`<svg class="board-bar" aria-hidden="true">`,
		`<rect class="bar-fill cls-death-knight" width="100%" height="100%" rx="4"/>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the overview is missing %s", want)
		}
	}
	// Nobody raided: that board is absent rather than empty.
	if strings.Contains(body, "Most raid bosses down") {
		t.Error("an empty bosses board was rendered")
	}
}

// TestBoardsCutAtTen: twelve qualify, ten are shown, and they are the best ten.
func TestBoardsCutAtTen(t *testing.T) {
	a := snapshotApp(&fakeClient{}, time.Hour)

	var members []blizzard.GuildMember
	details := map[string]memberDetail{}
	for i := 1; i <= 12; i++ {
		m := blizzard.GuildMember{Name: "Member" + strconv.Itoa(i), RealmSlug: "elune", Rank: 1, Level: 90}
		members = append(members, m)
		details[memberKey(m)] = memberDetail{Character: blizzard.Character{
			Name: m.Name, RealmSlug: "elune", AverageItemLevel: 300 + i,
		}}
	}

	sum := a.summarise(members, a.group(members, details), details)
	board := sum.Boards[0]
	if len(board.Entries) != boardSize || boardSize != 10 {
		t.Fatalf("board shows %d places, want %d", len(board.Entries), boardSize)
	}
	if board.Entries[0].Name != "Member12" || board.Entries[9].Name != "Member3" {
		t.Errorf("places 1 and 10 are %s and %s, want Member12 and Member3", board.Entries[0].Name, board.Entries[9].Name)
	}
	if board.Entries[9].Rank != 10 {
		t.Errorf("tenth place is numbered %d", board.Entries[9].Rank)
	}
}

// TestAuthorityMarks: the guild master wears a crown and an officer a shield,
// in the rail, on the card, and on a leaderboard; everyone else wears nothing,
// because the mark means something only if most names lack it.
func TestAuthorityMarks(t *testing.T) {
	a := snapshotApp(&fakeClient{}, time.Hour)
	members := []blizzard.GuildMember{
		{Name: "Cwds", Rank: 0, Level: 90, Class: "Monk", RealmSlug: "elune"},
		{Name: "Arcanost", Rank: 1, Level: 90, Class: "Mage", RealmSlug: "sargeras"},
		{Name: "Azelora", Rank: 2, Level: 90, Class: "Priest", RealmSlug: "stormrage"},
	}
	details := map[string]memberDetail{}
	for i, m := range members {
		details[memberKey(m)] = memberDetail{Character: blizzard.Character{
			Name: m.Name, RealmSlug: m.RealmSlug, Class: m.Class, AverageItemLevel: 300 + i,
		}}
	}
	groups := a.group(members, details)
	sum := a.summarise(members, groups, details)
	body := render(t, view{Groups: groups, Summary: sum, Total: 3, Home: routePrefix})

	// The rail, the card and the board: two crowns' worth of places for the
	// guild master's name, two shields' worth for the officer -- plus the
	// item-level board, where all three are placed.
	if n := strings.Count(body, `aria-label="Guild Master"`); n != 3 {
		t.Errorf("found %d crowns, want 3 (rail row, card, board)", n)
	}
	if n := strings.Count(body, `aria-label="Officer"`); n != 3 {
		t.Errorf("found %d shields, want 3 (rail row, card, board)", n)
	}
	// The rank 2 member's name has no mark before it anywhere.
	if strings.Contains(body, `rank-mark`+"\"></svg>Azelora") || strings.Contains(body, `</svg>Azelora`) {
		t.Error("a rank 2 member was given an authority mark")
	}
	// The crown sits inside the coloured name, so the mark and the class
	// colour travel together.
	if !strings.Contains(body, `<span class="row-name class-name cls-monk"><svg class="rank-mark rank-mark--gm"`) {
		t.Error("the crown is not inside the guild master's name in the rail")
	}
}

// --- The roster search --------------------------------------------------------

// searchApp is an App with a roster loaded and a layout that just writes the
// body, so show() can be driven end to end.
func searchApp(t *testing.T, members []blizzard.GuildMember) *App {
	t.Helper()
	f := &fakeClient{roster: members, byName: map[string]blizzard.Character{}}
	a := snapshotApp(f, time.Hour)
	a.tmpl, _ = parseTemplates()
	a.deps.RenderInLayout = func(w http.ResponseWriter, _ *http.Request, status int, _ string, content template.HTML) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(content))
	}
	return a
}

func search(t *testing.T, a *App, q string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/app/guild?q="+url.QueryEscape(q), nil)
	r = r.WithContext(platform.ContextWithSession(r.Context(), auth.Session{AccessToken: "t"}))
	rec := httptest.NewRecorder()
	a.show(rec, r)
	return rec
}

var searchable = []blizzard.GuildMember{
	{Name: "Cwds", Rank: 0, Level: 90, Class: "Monk", RealmSlug: "elune", RealmName: "Elune"},
	{Name: "Cwds", Rank: 3, Level: 80, Class: "Rogue", RealmSlug: "illidan", RealmName: "Illidan"},
	{Name: "Lazzlowe", Rank: 1, Level: 90, Class: "Paladin", RealmSlug: "elune", RealmName: "Elune"},
	{Name: "Lazzloe", Rank: 2, Level: 71, Class: "Warlock", RealmSlug: "area-52", RealmName: "Area 52"},
}

// TestSearchRedirectsToTheOneMember: a name that means one member becomes that
// member's own URL, exactly as a click would have produced.
func TestSearchRedirectsToTheOneMember(t *testing.T) {
	a := searchApp(t, searchable)

	for q, want := range map[string]string{
		"Lazzlowe":      "/app/guild?c=elune%2Flazzlowe",
		"lazzlowe":      "/app/guild?c=elune%2Flazzlowe", // case does not matter
		"lazzlow":       "/app/guild?c=elune%2Flazzlowe", // a unique prefix is enough
		"cwds illidan":  "/app/guild?c=illidan%2Fcwds",   // a realm tells namesakes apart
		"Cwds (Elune)":  "/app/guild?c=elune%2Fcwds",
		"cwds/elune":    "/app/guild?c=elune%2Fcwds",
		"  Lazzlowe   ": "/app/guild?c=elune%2Flazzlowe",
	} {
		rec := search(t, a, q)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("search %q = %d %q, want 302 %q", q, rec.Code, rec.Header().Get("Location"), want)
		}
	}
}

// TestSearchOffersAChoiceWhenAmbiguous: namesakes, or a prefix several names
// share, render a list to pick from rather than guessing.
func TestSearchOffersAChoiceWhenAmbiguous(t *testing.T) {
	a := searchApp(t, searchable)

	for q, wantNames := range map[string][]string{
		"Cwds": {"elune%2fcwds", "illidan%2fcwds"},        // two Cwds
		"lazz": {"elune%2flazzlowe", "area-52%2flazzloe"}, // two Lazz-somethings
	} {
		rec := search(t, a, q)
		if rec.Code != http.StatusOK {
			t.Fatalf("search %q = %d, want 200 with a choice", q, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Several characters match") {
			t.Errorf("search %q did not offer a choice", q)
		}
		for _, key := range wantNames {
			if !strings.Contains(body, `href="?c=`+key+`"`) {
				t.Errorf("search %q: the choice is missing %s", q, key)
			}
		}
		// The box keeps what was typed.
		if !strings.Contains(body, `value="`+q+`"`) {
			t.Errorf("search %q: the query was not echoed into the box", q)
		}
	}
}

// TestSearchSaysWhenNobodyMatches: the same notice a stale link gets.
func TestSearchSaysWhenNobodyMatches(t *testing.T) {
	rec := search(t, searchApp(t, searchable), "Nobody")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No character called <strong>Nobody</strong>") {
		t.Errorf("search for nobody = %d, body lacks the notice", rec.Code)
	}
}

// TestSearchExactBeatsPrefix: "Lazzloe" is also a prefix of nothing else, but
// were a name both an exact match and a prefix of another, exact wins --
// searching "Cwds" must not also drag in a hypothetical "Cwdsalt".
func TestSearchExactBeatsPrefix(t *testing.T) {
	members := append([]blizzard.GuildMember{}, searchable...)
	members = append(members, blizzard.GuildMember{Name: "Lazzlowealt", Rank: 5, Level: 60, Class: "Mage", RealmSlug: "elune"})
	rec := search(t, searchApp(t, members), "Lazzlowe")
	if rec.Code != http.StatusFound {
		t.Errorf("an exact match was outweighed by a longer name: %d", rec.Code)
	}
}

// TestRosterRendersTheSearchBox: the form, and a datalist with every name.
func TestRosterRendersTheSearchBox(t *testing.T) {
	a := appWith([]string{"Guild Master", "Officer"})
	body := render(t, view{Groups: a.group(searchable, nil), Total: 4, Home: routePrefix})

	for _, want := range []string{
		`<form class="roster-search" method="get" action="/app/guild" role="search">`,
		`<input id="roster-q" name="q" list="roster-names"`,
		`<datalist id="roster-names">`,
		`<option value="Lazzlowe">90 Paladin &middot; Elune</option>`,
		// Namesakes carry their realm, so a pick is never ambiguous.
		`<option value="Cwds (Elune)">90 Monk &middot; Elune</option>`,
		`<option value="Cwds (Illidan)">80 Rogue &middot; Illidan</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the rail is missing %s", want)
		}
	}
	if n := strings.Count(body, "<option value="); n != len(searchable) {
		t.Errorf("datalist has %d options, want one per member (%d)", n, len(searchable))
	}
}

// TestPickedSuggestionResolvesToOneMember: what the datalist offers for a
// namesake is exactly what the server needs to tell them apart, so a pick
// from the suggestions never lands on the choice page.
func TestPickedSuggestionResolvesToOneMember(t *testing.T) {
	a := searchApp(t, searchable)
	rec := search(t, a, "Cwds (Illidan)")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/app/guild?c=illidan%2Fcwds" {
		t.Errorf("picking the Illidan Cwds = %d %q, want a redirect to illidan/cwds",
			rec.Code, rec.Header().Get("Location"))
	}
}

// TestClassBarsCarryRoles: under each class, how many tank, heal and DPS by
// their active spec -- only roles anyone fills, only members with a spec.
func TestClassBarsCarryRoles(t *testing.T) {
	a := snapshotApp(&fakeClient{}, time.Hour)
	members := []blizzard.GuildMember{
		{Name: "Tankpal", Rank: 1, Level: 90, Class: "Paladin", RealmSlug: "elune"},
		{Name: "Holypal", Rank: 1, Level: 90, Class: "Paladin", RealmSlug: "elune"},
		{Name: "Retpal", Rank: 1, Level: 90, Class: "Paladin", RealmSlug: "elune"},
		{Name: "Retpaltwo", Rank: 1, Level: 90, Class: "Paladin", RealmSlug: "elune"},
		{Name: "Mystery", Rank: 1, Level: 90, Class: "Paladin", RealmSlug: "elune"}, // no profile
		{Name: "Icemage", Rank: 1, Level: 90, Class: "Mage", RealmSlug: "elune"},
	}
	details := map[string]memberDetail{}
	spec := map[string]string{"Tankpal": "Protection", "Holypal": "Holy", "Retpal": "Retribution", "Retpaltwo": "Retribution", "Icemage": "Frost"}
	for _, m := range members {
		if sp, ok := spec[m.Name]; ok {
			details[memberKey(m)] = memberDetail{Character: blizzard.Character{Name: m.Name, RealmSlug: m.RealmSlug, Class: m.Class, ActiveSpec: sp}}
		}
	}

	sum := a.summarise(members, a.group(members, details), details)
	pal := sum.Classes[0]
	if pal.Label != "Paladin" || pal.Count != 5 {
		t.Fatalf("first class = %+v, want Paladin with 5", pal)
	}
	want := []roleCount{{blizzard.RoleTank, 1}, {blizzard.RoleHealer, 1}, {blizzard.RoleDPS, 2}}
	if len(pal.Roles) != 3 || pal.Roles[0] != want[0] || pal.Roles[1] != want[1] || pal.Roles[2] != want[2] {
		t.Errorf("Paladin roles = %+v, want %+v (Mystery, with no spec, in no role)", pal.Roles, want)
	}
	// One Frost Mage: DPS only, tank and healer omitted rather than shown as 0.
	mage := sum.Classes[1]
	if len(mage.Roles) != 1 || mage.Roles[0] != (roleCount{blizzard.RoleDPS, 1}) {
		t.Errorf("Mage roles = %+v, want DPS 1 alone", mage.Roles)
	}

	body := html.UnescapeString(render(t, view{Groups: a.group(members, details), Summary: sum, Total: 6, Home: routePrefix}))
	for _, want := range []string{
		`<span class="bar-role"><span class="bar-role-name">Tank</span> 1</span>`,
		`<span class="bar-role-name">Healer</span> 1`,
		`<span class="bar-role-name">DPS</span> 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the class chart is missing %s", want)
		}
	}
}

// TestRoleShareSumsToExactly100: three shares of seven cannot each round to a
// whole percent and still add up; largest remainder makes them.
func TestRoleShareSumsToExactly100(t *testing.T) {
	shares, total := shareOf(map[blizzard.Role]int{blizzard.RoleTank: 1, blizzard.RoleHealer: 2, blizzard.RoleDPS: 4})
	if total != 7 || len(shares) != 3 {
		t.Fatalf("total %d, %d shares", total, len(shares))
	}
	sum := 0
	for _, sh := range shares {
		sum += sh.Pct
	}
	if sum != 100 {
		t.Errorf("shares sum to %d, want exactly 100: %+v", sum, shares)
	}
	// 1/7 = 14.28, 2/7 = 28.57, 4/7 = 57.14: floors 14+28+57 = 99, and the
	// leftover point goes to the healer, who lost the most.
	if shares[0].Pct != 14 || shares[1].Pct != 29 || shares[2].Pct != 57 {
		t.Errorf("shares = %d/%d/%d, want 14/29/57", shares[0].Pct, shares[1].Pct, shares[2].Pct)
	}
	if shares[0].Offset != 0 || shares[1].Offset != 14 || shares[2].Offset != 43 {
		t.Errorf("offsets = %d/%d/%d, want 0/14/43", shares[0].Offset, shares[1].Offset, shares[2].Offset)
	}
	if shares[0].Class != "tank" || shares[2].Class != "dps" {
		t.Errorf("classes = %q, %q", shares[0].Class, shares[2].Class)
	}

	// Nobody with a spec: no bar, rather than a bar of nothing.
	if sh, n := shareOf(nil); sh != nil || n != 0 {
		t.Errorf("empty totals gave %+v, %d", sh, n)
	}
}

// TestRoleBarRenders: the stacked bar's slices sit at their offsets with a
// gap between them, and the legend reads the same numbers.
func TestRoleBarRenders(t *testing.T) {
	a := snapshotApp(&fakeClient{}, time.Hour)
	members := []blizzard.GuildMember{
		{Name: "T", Rank: 1, Level: 90, Class: "Paladin", RealmSlug: "elune"},
		{Name: "H", Rank: 1, Level: 90, Class: "Priest", RealmSlug: "elune"},
		{Name: "D1", Rank: 1, Level: 90, Class: "Mage", RealmSlug: "elune"},
		{Name: "D2", Rank: 1, Level: 90, Class: "Mage", RealmSlug: "elune"},
	}
	details := map[string]memberDetail{}
	for _, m := range members {
		spec := map[string]string{"T": "Protection", "H": "Holy", "D1": "Frost", "D2": "Fire"}[m.Name]
		details[memberKey(m)] = memberDetail{Character: blizzard.Character{Name: m.Name, RealmSlug: m.RealmSlug, Class: m.Class, ActiveSpec: spec}}
	}
	sum := a.summarise(members, a.group(members, details), details)
	body := html.UnescapeString(render(t, view{Groups: a.group(members, details), Summary: sum, Total: 4, Home: routePrefix}))

	for _, want := range []string{
		`<rect class="role-slice role-tank" x="0%" width="25%" height="12"/>`,
		`<rect class="role-slice role-healer" x="25%" width="25%" height="12"/>`,
		`<rect class="role-gap" x="25%" width="2" height="12"/>`,
		`<rect class="role-slice role-dps" x="50%" width="50%" height="12"/>`,
		"Tank <b>25%</b>", "Healer <b>25%</b>", "DPS <b>50%</b>",
		"of 4 with a known spec",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the role bar is missing %s", want)
		}
	}
	if strings.Contains(body, "style=") {
		t.Error("the role bar uses an inline style, which the CSP blocks")
	}
}
