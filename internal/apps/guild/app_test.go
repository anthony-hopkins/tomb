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

// TestMetaIsTheHome pins the registration contract. Being first in cmd/tomb is
// what makes this the landing page, and the core sends "/" to the first app.
func TestMetaIsTheHome(t *testing.T) {
	meta := (&App{}).Meta()

	if meta.RoutePrefix != "/app/"+meta.Slug {
		t.Errorf("RoutePrefix = %q, must be /app/ + %q", meta.RoutePrefix, meta.Slug)
	}
	if !meta.RequiresGuild {
		t.Error("RequiresGuild is false; a guild roster is guild business")
	}
	if meta.NavLabel == "" {
		t.Error("no NavLabel, so the page would be unreachable from the nav")
	}
}
