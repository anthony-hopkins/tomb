package platform

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthony-hopkins/tomb/internal/auth"
)

// stubApp is a minimal App: nothing but Meta() and Routes(). It exists to prove
// the extension point needs no more than that (contracts/app-registration.md).
type stubApp struct {
	meta AppMeta
	body string

	// render, when set, draws the body through the shared layout the way a real
	// app does. Left nil the stub writes its body raw, which is enough for
	// routing and gating assertions.
	render func(w http.ResponseWriter, r *http.Request, status int, title string, content template.HTML)
}

func (s *stubApp) Meta() AppMeta { return s.meta }

func (s *stubApp) Routes(r Registrar) {
	r.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if s.render != nil {
			s.render(w, req, http.StatusOK, s.meta.NavLabel, template.HTML(s.body))
			return
		}
		_, _ = w.Write([]byte(s.body))
	}))
}

func newStub(slug, label, body string, requiresGuild bool) *stubApp {
	return &stubApp{
		meta: AppMeta{
			Slug:          slug,
			NavLabel:      label,
			RoutePrefix:   "/app/" + slug,
			RequiresGuild: requiresGuild,
		},
		body: body,
	}
}

// testCore builds a Core with no database and no Blizzard client, sufficient
// for registration and routing assertions.
func testCore(t *testing.T) *Core {
	t.Helper()

	templates, err := LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates() error = %v", err)
	}

	return &Core{
		Deps:      Deps{Logger: discardLogger(), Config: Config{}},
		Sessions:  &auth.SessionManager{Store: &auth.Store{}},
		CSRF:      &CSRF{},
		Templates: templates,
	}
}

func emptyAuthHandlers() *auth.Handlers {
	return &auth.Handlers{Logger: discardLogger()}
}

// TestMountRegistersApps covers the registration contract's guarantee table.
func TestMountRegistersApps(t *testing.T) {
	tests := []struct {
		name    string
		apps    []App
		wantErr string // substring; empty means success
	}{
		{
			name: "two apps mount side by side",
			apps: []App{
				newStub("dashboard", "My Character", "dash", false),
				newStub("roster", "Roster", "roster", false),
			},
		},
		{
			name: "duplicate slug fails fast",
			apps: []App{
				newStub("dashboard", "One", "a", false),
				newStub("dashboard", "Two", "b", false),
			},
			wantErr: "duplicate Slug",
		},
		{
			name: "route prefix inconsistent with slug fails fast",
			apps: []App{
				// Hand-built so the prefix disagrees with the slug.
				&stubApp{meta: AppMeta{
					Slug:        "dashboard",
					NavLabel:    "Dash",
					RoutePrefix: "/app/something-else",
				}},
			},
			wantErr: `want "/app/dashboard"`,
		},
		{
			name: "empty slug fails fast",
			apps: []App{
				&stubApp{meta: AppMeta{Slug: "", NavLabel: "X", RoutePrefix: "/app/"}},
			},
			wantErr: "empty Slug",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core := testCore(t)
			_, err := Mount(core, emptyAuthHandlers(), tc.apps)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Mount() error = %v, want nil", err)
				}
				if len(core.Registry()) != len(tc.apps) {
					t.Errorf("registry has %d apps, want %d", len(core.Registry()), len(tc.apps))
				}
				return
			}

			if err == nil {
				t.Fatalf("Mount() error = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Mount() error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestMountedRoutesAreReachableUnderTheirPrefix proves an app's relative
// patterns land under its own prefix and nowhere else.
func TestMountedRoutesAreReachableUnderTheirPrefix(t *testing.T) {
	core := testCore(t)

	// RequiresGuild false so no Blizzard fetch is attempted; the session gate
	// is still active, which is what the 302 below confirms.
	handler, err := Mount(core, emptyAuthHandlers(), []App{
		newStub("alpha", "Alpha", "alpha-body", false),
		newStub("beta", "Beta", "beta-body", false),
	})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	// Anonymous requests are redirected by requireSession, proving the core
	// gates app routes rather than the app doing it (guarantee 3).
	for _, path := range []string{"/app/alpha", "/app/beta"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusFound {
			t.Errorf("GET %s as anonymous = %d, want %d (session gate)",
				path, rec.Code, http.StatusFound)
		}
	}

	// A path no app registered must 404, not fall through to another app.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/gamma", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /app/gamma = %d, want 404", rec.Code)
	}
}

// TestCoreRoutesAlwaysMounted checks the routes the core owns regardless of
// which apps exist (contracts/http-routes.md).
func TestCoreRoutesAlwaysMounted(t *testing.T) {
	core := testCore(t)
	handler, err := Mount(core, emptyAuthHandlers(), nil)
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	// /healthz must not touch the database: this Core has none, and it must
	// still report healthy.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /healthz = %d, want 200 with no database configured", rec.Code)
	}

	// /readyz must fail when there is no database.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz = %d, want 503 with no database configured", rec.Code)
	}

	// The landing page renders for an anonymous visitor (FR-001).
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Sign in with Battle.net") {
		t.Error("landing page is missing the Sign in with Battle.net action (FR-001)")
	}
}

// TestSecurityHeadersApplied covers the hardening middleware.
func TestSecurityHeadersApplied(t *testing.T) {
	core := testCore(t)
	handler, err := Mount(core, emptyAuthHandlers(), nil)
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("Content-Security-Policy is missing")
	}
}
