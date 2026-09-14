package platform

import (
	"io/fs"
	"math"
	"regexp"
	"strconv"
	"testing"
)

// The class colours in the stylesheet come in two roles: --cls for marks and
// --cls-text for names. Names are text, and text on this site clears WCAG AA
// (4.5:1) against the panel it sits on; a few canonical class colours do not,
// and were lightened for the text role. This test is what keeps that true.
//
// Computed, not eyeballed: the site's own theme was built by sampling the
// banner and computing contrast, and a colour "looking fine" is exactly how
// the ones that fail get in.

var (
	clsRule    = regexp.MustCompile(`\.cls-([a-z-]+)\s*\{\s*--cls:\s*(#[0-9a-fA-F]{6});\s*--cls-text:\s*(#[0-9a-fA-F]{6});`)
	surfaceVar = regexp.MustCompile(`--panel:\s*(#[0-9a-fA-F]{6});`)
	pageVar    = regexp.MustCompile(`--bg:\s*(#[0-9a-fA-F]{6});`)
)

// TestClassTextColoursClearAA holds every --cls-text to 4.5:1 against the
// panel colour and the page colour, since names appear on both.
func TestClassTextColoursClearAA(t *testing.T) {
	css, err := fs.ReadFile(staticFS, "static/style.css")
	if err != nil {
		t.Fatalf("reading the stylesheet: %v", err)
	}

	panel := mustHex(t, surfaceVar, css, "--panel")
	page := mustHex(t, pageVar, css, "--bg")

	rules := clsRule.FindAllStringSubmatch(string(css), -1)
	if len(rules) < 13 {
		t.Fatalf("found %d class colour rules, want the 13 classes", len(rules))
	}

	for _, r := range rules {
		class, mark, text := r[1], r[2], r[3]
		for name, surface := range map[string]string{"panel": panel, "page": page} {
			if got := contrast(text, surface); got < 4.5 {
				t.Errorf("%s: --cls-text %s is %.2f:1 on the %s (%s); names need 4.5:1",
					class, text, got, name, surface)
			}
		}
		// The text variant exists to fix contrast; where the canonical colour
		// already passes there is no reason for the two to differ, and a
		// drift between them would be a name in a colour that is no longer
		// the class's.
		if contrast(mark, panel) >= 4.5 && mark != text {
			t.Errorf("%s: canonical %s already clears AA, so --cls-text should be the same, not %s",
				class, mark, text)
		}
	}
}

func mustHex(t *testing.T, re *regexp.Regexp, css []byte, what string) string {
	t.Helper()
	m := re.FindSubmatch(css)
	if m == nil {
		t.Fatalf("%s not found in the stylesheet", what)
	}
	return string(m[1])
}

// contrast is WCAG 2.x contrast ratio between two opaque sRGB colours.
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(hex string) float64 {
	ch := func(i int) float64 {
		v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(1) + 0.7152*ch(3) + 0.0722*ch(5)
}
