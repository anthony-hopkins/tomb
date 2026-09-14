package guild

import (
	"bytes"
	"html"
	"html/template"
	"strings"
	"testing"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

func render(t *testing.T, v view) string {
	t.Helper()

	tmpl, err := template.ParseFS(templateFS, "templates/guild.html")
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
	})

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
		}),
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
		}),
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
	groups := a.group(members)
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
		sum := a.summarise(members, a.group(members))
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
		Groups:  a.group(members),
		Total:   1,
		Summary: a.summarise(members, a.group(members)),
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
		Groups:       a.group([]blizzard.GuildMember{{Name: "Someone", Rank: 4, Level: 80}}),
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
