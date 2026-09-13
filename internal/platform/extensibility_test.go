package platform

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// This file is acceptance scenario 6 and FR-011: a new app can be added without
// any change to the login flow, session handling, or another app.
//
// The stub apps here implement nothing but Meta() and Routes(). If making them
// work ever requires editing internal/auth or internal/apps/dashboard, the
// extension point has regressed.

// sessionedCore returns a Core plus a handler where the viewer is treated as a
// signed-in guild member, so gated app routes can be exercised.
func sessionedCore(t *testing.T, member bool, apps ...App) (*Core, http.Handler) {
	t.Helper()

	templates, err := LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates() error = %v", err)
	}

	guildName := "TOMB"
	if !member {
		guildName = "Some Other Guild"
	}

	fake := &fakeClient{
		refs: refsFor("Maintank"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return memberOf(ref.Name, guildName), nil
		},
	}

	core := &Core{
		Deps:      Deps{Logger: discardLogger(), Blizzard: fake},
		Sessions:  &auth.SessionManager{Store: &auth.Store{}},
		Profiles:  newFetcher(fake),
		CSRF:      &CSRF{},
		Templates: templates,
	}
	core.Deps.RenderInLayout = core.RenderInLayout

	// Let stubs render through the shared shell, so navigation and the
	// signed-in header are exercised the way a real app exercises them.
	for _, a := range apps {
		if stub, ok := a.(*stubApp); ok {
			stub.render = core.RenderInLayout
		}
	}

	handler, err := Mount(core, &auth.Handlers{Logger: discardLogger()}, apps)
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	// Inject a session the way the real middleware would, without a database.
	withFakeSession := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = withSession(r, auth.Session{
			User:        auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"},
			AccessToken: "token",
			ExpiresAt:   time.Now().Add(time.Hour),
		})
		handler.ServeHTTP(w, r)
	})

	return core, withFakeSession
}

// TestSecondAppMountsWithNoCoreChanges is scenario 6 proper.
func TestSecondAppMountsWithNoCoreChanges(t *testing.T) {
	first := newStub("dashboard", "My Character", "first-app-body", false)
	second := newStub("roster", "Roster", "second-app-body", false)

	_, handler := sessionedCore(t, true, first, second)

	for _, tc := range []struct{ path, wantBody string }{
		{"/app/dashboard", "first-app-body"},
		{"/app/roster", "second-app-body"},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", tc.path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("GET %s body does not contain %q", tc.path, tc.wantBody)
		}
	}
}

// TestStubAppInheritsGuildGate: a stub declaring RequiresGuild is gated exactly
// like the dashboard, proving gating comes from the core rather than being
// reimplemented per app (contracts/app-registration.md guarantee 3).
func TestStubAppInheritsGuildGate(t *testing.T) {
	tests := []struct {
		name          string
		member        bool
		requiresGuild bool
		wantBody      bool
	}{
		{"member reaches a gated app", true, true, true},
		{"non-member is denied a gated app", false, true, false},
		{"non-member reaches an ungated app", false, false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := newStub("roster", "Roster", "gated-body", tc.requiresGuild)
			_, handler := sessionedCore(t, tc.member, stub)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/roster", nil))

			hasBody := strings.Contains(rec.Body.String(), "gated-body")
			if hasBody != tc.wantBody {
				t.Errorf("handler reached = %v, want %v (status %d)", hasBody, tc.wantBody, rec.Code)
			}

			if !tc.wantBody {
				// The denial must be the FR-013a non-member page.
				if !strings.Contains(rec.Body.String(), "TOMB members only") {
					t.Error("non-member response is not the members-only page")
				}
			}
		})
	}
}

// TestNavigationBuiltFromAppMeta covers guarantee 4.
func TestNavigationBuiltFromAppMeta(t *testing.T) {
	_, handler := sessionedCore(t, true,
		newStub("roster", "Roster", "body", false),
		newStub("dashboard", "My Character", "body", false),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/roster", nil))

	body := rec.Body.String()
	for _, want := range []string{"Roster", "My Character", "/app/roster", "/app/dashboard"} {
		if !strings.Contains(body, want) {
			t.Errorf("navigation is missing %q", want)
		}
	}
}

// TestAppsDoNotTouchAuthOrEachOther is a static check backing scenario 6's
// "without any code changes to the login flow, session handling, or other
// existing apps". It reads the dashboard's source and asserts it imports no
// auth package and no sibling app.
func TestAppsDoNotTouchAuthOrEachOther(t *testing.T) {
	appsDir := filepath.Join("..", "apps")

	entries, err := os.ReadDir(appsDir)
	if err != nil {
		t.Skipf("cannot read %s: %v", appsDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		appName := entry.Name()

		files, err := filepath.Glob(filepath.Join(appsDir, appName, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", appName, err)
		}

		for _, file := range files {
			// The constraint is on an app's production source. A test may
			// legitimately import auth to construct a session.
			if strings.HasSuffix(file, "_test.go") {
				continue
			}

			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			text := string(src)

			if strings.Contains(text, `"github.com/anthony-hopkins/tomb/internal/auth"`) {
				t.Errorf("%s imports tomb/internal/auth; apps must never handle auth themselves", file)
			}

			// An app must not import a sibling app.
			for _, other := range entries {
				if !other.IsDir() || other.Name() == appName {
					continue
				}
				if strings.Contains(text, `"github.com/anthony-hopkins/tomb/internal/apps/`+other.Name()+`"`) {
					t.Errorf("%s imports sibling app %q; cross-app coupling is forbidden",
						file, other.Name())
				}
			}
		}
	}
}

// TestGuildGateRunsBeforeAppHandler proves ordering: a denied request must
// never execute the app's handler at all.
func TestGuildGateRunsBeforeAppHandler(t *testing.T) {
	reached := false
	spy := &stubApp{meta: AppMeta{
		Slug:          "spy",
		NavLabel:      "Spy",
		RoutePrefix:   "/app/spy",
		RequiresGuild: true,
	}}
	// Replace Routes' handler with one that records being reached.
	spyApp := &recordingApp{stubApp: spy, reached: &reached}

	_, handler := sessionedCore(t, false, spyApp)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/spy", nil))

	if reached {
		t.Error("the app handler ran for a non-member; the gate must run first")
	}
}

type recordingApp struct {
	*stubApp
	reached *bool
}

func (r *recordingApp) Routes(reg Registrar) {
	reg.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		*r.reached = true
		w.Write([]byte("should not happen"))
	}))
}
