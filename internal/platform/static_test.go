package platform

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// cssURL matches url(...) references in the stylesheet, quoted or not.
var cssURL = regexp.MustCompile(`url\(\s*["']?([^"')]+)["']?\s*\)`)

// TestStylesheetAssetsExist keeps the stylesheet honest about what it asks for.
//
// The page background is a CSS url() into the embedded static FS. A missing
// file there fails the way missing decoration always does -- silently, with the
// page still rendering and nobody noticing until someone asks why the artwork
// went. Nothing else in the build connects the two, so this does.
func TestStylesheetAssetsExist(t *testing.T) {
	css, err := fs.ReadFile(staticFS, "static/style.css")
	if err != nil {
		t.Fatalf("reading the stylesheet: %v", err)
	}

	matches := cssURL.FindAllStringSubmatch(string(css), -1)
	if len(matches) == 0 {
		t.Skip("the stylesheet references no assets")
	}

	for _, m := range matches {
		ref := m[1]
		if strings.HasPrefix(ref, "data:") || strings.Contains(ref, "://") {
			continue // inline or remote; not ours to resolve
		}
		if !strings.HasPrefix(ref, "/static/") {
			t.Errorf("url(%q) is not served from /static/, so it will 404", ref)
			continue
		}

		embedded := "static/" + strings.TrimPrefix(ref, "/static/")
		if _, err := fs.Stat(staticFS, embedded); err != nil {
			t.Errorf("style.css references %s but %s is not in the embedded FS: %v",
				ref, embedded, err)
		}
	}
}

// TestStaticHandlerServesTheBanner proves the asset is reachable over HTTP with
// the content type a browser needs, not merely present in the binary.
func TestStaticHandlerServesTheBanner(t *testing.T) {
	rec := httptest.NewRecorder()
	StaticHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/banner.jpg", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/banner.jpg = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/") {
		t.Errorf("Content-Type = %q, want an image type", got)
	}
	if rec.Body.Len() == 0 {
		t.Error("the banner was served empty")
	}
}

// TestCSPAllowsTheStylesheetsImages is the pairing that bit this project twice
// tonight: a header that forbids what the page needs, failing invisibly.
//
// A background image is governed by img-src, not style-src. 'self' covers
// /static/banner.jpg, and this asserts it rather than leaving it to be
// discovered in a browser with no error to show for it.
func TestCSPAllowsTheStylesheetsImages(t *testing.T) {
	sources := directives(t, contentSecurityPolicy)["img-src"]
	if len(sources) == 0 {
		t.Fatal("img-src is absent; the page background would be blocked")
	}

	var hasSelf bool
	for _, s := range sources {
		if s == "'self'" {
			hasSelf = true
		}
	}
	if !hasSelf {
		t.Errorf("img-src = %v, missing 'self'; /static/banner.jpg would be refused", sources)
	}
}
