package comingsoon

import (
	"bytes"
	"html"
	"html/template"
	"strings"
	"testing"
)

// render executes the app's own embedded template, without standing up the
// platform: this app has no data of its own and no behaviour worth mounting a
// core for.
func render(t *testing.T) string {
	t.Helper()

	tmpl, err := template.ParseFS(templateFS, "templates/comingsoon.html")
	if err != nil {
		t.Fatalf("parsing the template: %v", err)
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "comingsoon.html", view{Roadmap: Roadmap}); err != nil {
		t.Fatalf("executing the template: %v", err)
	}
	return buf.String()
}

// TestRoadmapIsListed is copy rather than behaviour, but it is copy that makes
// promises, so a silently empty list is worth catching.
func TestRoadmapIsListed(t *testing.T) {
	raw := render(t)

	// Unescaped, so this asserts what a member reads rather than how
	// html/template chose to encode it: an apostrophe arrives as &#39;, which
	// is correct and not what the test is about.
	body := html.UnescapeString(raw)

	for _, want := range []string{
		"Ask TOMB Bot",
		"Combat log analysis",
		"Gear analysis",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the roadmap is missing %q", want)
		}
	}

	if n := strings.Count(raw, `class="tag-ai"`); n != 3 {
		t.Errorf("found %d AI tags, want 3 (Ask TOMB Bot, combat log, gear)", n)
	}

	if !strings.Contains(body, "None of this is built yet") {
		t.Error("the page does not say it is unbuilt; that is the one thing it must say")
	}
}

// TestOnlyAIEntriesAreTagged keeps the tag honest. Labelling the calendar as
// AI-driven would be a claim about the product, not a styling slip, so which
// entries carry it is asserted by name rather than by count alone.
func TestOnlyAIEntriesAreTagged(t *testing.T) {
	want := map[string]bool{
		"Ask TOMB Bot":        true,
		"Combat log analysis": true,
		"Gear analysis":       true,
	}

	if len(Roadmap) != len(want) {
		t.Fatalf("the roadmap has %d entries, this test knows about %d", len(Roadmap), len(want))
	}

	for _, item := range Roadmap {
		expected, known := want[item.Title]
		if !known {
			t.Errorf("unexpected roadmap entry %q; decide whether it is AI-driven", item.Title)
			continue
		}
		if item.AI != expected {
			t.Errorf("%q has AI=%v, want %v", item.Title, item.AI, expected)
		}
		if strings.TrimSpace(item.Blurb) == "" {
			t.Errorf("%q has no description", item.Title)
		}
	}
}

// TestMetaPlacesItInNav covers the registration contract: the nav entry and the
// route prefix have to agree, or Mount rejects the app.
func TestMetaPlacesItInNav(t *testing.T) {
	meta := (&App{}).Meta()

	if meta.RoutePrefix != "/app/"+meta.Slug {
		t.Errorf("RoutePrefix = %q, must be /app/ + %q", meta.RoutePrefix, meta.Slug)
	}
	if meta.NavLabel == "" {
		t.Error("no NavLabel, so the page would be unreachable from the nav")
	}
	if !meta.RequiresGuild {
		t.Error("RequiresGuild is false; the whole site is members-only")
	}
}
