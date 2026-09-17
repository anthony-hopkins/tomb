package dashboard

import (
	"strings"
	"testing"
)

// TestRenderWriteup: the Markdown the model is asked for becomes the card's
// markup, everything else is text, and nothing the model writes reaches the
// page unescaped.
func TestRenderWriteup(t *testing.T) {
	in := strings.Join([]string{
		"## The build",
		"Two lines of a",
		"paragraph with **bold**, *italic* and `code`.",
		"",
		"### Cast rates",
		"| Ability | Yours | Theirs |",
		"|---|---:|---|",
		"| Death Strike | 12.1 | 16.8 |",
		"| Marrowrend | 2.2 | 2.0 |",
		"",
		"- first point",
		"- second <b>point</b>",
		"1. step one",
		"2) step two",
		"",
		"Do these first",
		"",
		"<script>alert(1)</script> and a [link](http://x)",
	}, "\n")
	got := string(renderWriteup(in))
	for _, want := range []string{
		"<h4>The build</h4>",
		"<p>Two lines of a paragraph with <strong>bold</strong>, <em>italic</em> and <code>code</code>.</p>",
		"<h5>Cast rates</h5>",
		"<div class=\"writeup-scroll\">\n<table class=\"writeup-table\">", "<thead>", "<th>Ability</th><th>Yours</th><th>Theirs</th>", "<td>Death Strike</td><td>12.1</td><td>16.8</td>", "</tbody>\n</table>\n</div>",
		"<ul>\n<li>first point</li>\n<li>second &lt;b&gt;point&lt;/b&gt;</li>\n</ul>",
		"<ol>\n<li>step one</li>\n<li>step two</li>\n</ol>",
		"<h4>Do these first</h4>",
		"&lt;script&gt;alert(1)&lt;/script&gt; and a [link](http://x)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<script") || strings.Contains(got, "|---") || strings.Contains(got, "<a ") {
		t.Errorf("unsafe or unrendered markup in:\n%s", got)
	}
	// The older plain form still reads: a lone short line is a heading.
	old := string(renderWriteup("Overview\n\nYou died once.\n\nDo these first\n\n- Press it more."))
	if !strings.Contains(old, "<h4>Overview</h4>") || !strings.Contains(old, "<p>You died once.</p>") || !strings.Contains(old, "<li>Press it more.</li>") {
		t.Errorf("plain write-up:\n%s", old)
	}
}
