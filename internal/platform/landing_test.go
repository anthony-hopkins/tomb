package platform

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The front door (spec 004, FR-042): a Public app is served with no session,
// a Landing app draws "/" for an anonymous visitor with the core's notice in
// hand, and an app that misdescribes itself is refused at Mount.

// frontStub is a Public, Landing app that writes the notice it was handed.
type frontStub struct{ meta AppMeta }

func (f *frontStub) Meta() AppMeta { return f.meta }

func (f *frontStub) Routes(r Registrar) {
	r.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("front door. Sign in with Battle.net. notice=" + LandingMessage(req)))
	}))
}

func front() *frontStub {
	return &frontStub{meta: AppMeta{Slug: "front", RoutePrefix: "/app/front", Public: true, Landing: true}}
}

func TestPublicAppServesAnonymousVisitors(t *testing.T) {
	pub := newStub("open", "", "wide open", false)
	pub.meta.Public = true
	handler, err := Mount(testCore(t), emptyAuthHandlers(), []App{pub, newStub("dash", "Dash", "dash", false)})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/open", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "wide open") {
		t.Errorf("GET /app/open anonymously = %d %q, want 200 with the body", rec.Code, rec.Body.String())
	}
	// The gate on everything else is untouched.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/dash", nil))
	if rec.Code != http.StatusFound {
		t.Errorf("GET /app/dash anonymously = %d, want 302 to the landing page", rec.Code)
	}
}

func TestMountRefusesMisdescribedPublicApps(t *testing.T) {
	gated := newStub("x", "X", "x", true)
	gated.meta.Public = true
	landingOnly := &frontStub{meta: AppMeta{Slug: "front", RoutePrefix: "/app/front", Landing: true}}
	second := front()
	second.meta.Slug, second.meta.RoutePrefix = "again", "/app/again"

	for _, tc := range []struct {
		name string
		apps []App
	}{
		{"public and guild-gated", []App{gated}},
		{"landing but not public", []App{landingOnly}},
		{"two landing apps", []App{front(), second}},
	} {
		if _, err := Mount(testCore(t), emptyAuthHandlers(), tc.apps); err == nil {
			t.Errorf("%s: Mount accepted it", tc.name)
		}
	}
}

func TestLandingAppAnswersTheRoot(t *testing.T) {
	handler, err := Mount(testCore(t), emptyAuthHandlers(), []App{front(), newStub("dash", "Dash", "dash", false)})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	for _, tc := range []struct {
		path   string
		notice string
	}{
		{"/", "notice=\n"},
		{"/?signed_out=1", "notice=You have been signed out"},
		{"/?reauth=1", "notice=Your Battle.net authorization is no longer valid"},
		{"/app/front", "notice=\n"},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		body := rec.Body.String() + "\n"
		if rec.Code != http.StatusOK || !strings.Contains(body, "Sign in with Battle.net") || !strings.Contains(body, tc.notice) {
			t.Errorf("GET %s = %d %q; want 200 with the sign-in action and %q", tc.path, rec.Code, body, tc.notice)
		}
	}
	// A path that is not the root is not the front door.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nowhere", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /nowhere = %d, want 404", rec.Code)
	}
}

// TestSignedInStillLeavesTheRoot: a viewer with a session goes home from "/"
// whether or not an app claims the front door, and can still open that app
// at its own path.
func TestSignedInStillLeavesTheRoot(t *testing.T) {
	_, handler := sessionedCore(t, true, front(), newStub("dashboard", "My Characters", "dash", true))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/app/dashboard" {
		t.Errorf("GET / signed in = %d to %q, want 302 to /app/dashboard", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/front", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "front door") {
		t.Errorf("GET /app/front signed in = %d, want the page", rec.Code)
	}
}
