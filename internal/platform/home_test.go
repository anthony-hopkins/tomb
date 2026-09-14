package platform

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHomePathIsDeclaredNotPositional covers where a signed-in member lands.
//
// This was briefly "the first registered app", which was wrong in a way worth
// keeping a test about: Mount SORTS the registry for navigation, so "first"
// meant alphabetically first by nav label, and signing in landed on Coming
// Soon. Home is declared by the app now, so sorting cannot move it.
func TestHomePathIsDeclaredNotPositional(t *testing.T) {
	tests := []struct {
		name string
		home string
		reg  []AppMeta
		want string
	}{
		{
			name: "the declared home wins",
			home: "/app/guild",
			reg: []AppMeta{
				{NavLabel: "Coming Soon", RoutePrefix: "/app/coming-soon"},
				{NavLabel: "My Characters", RoutePrefix: "/app/dashboard"},
			},
			want: "/app/guild",
		},
		{
			// The bug this replaced: sorted first is not registered first.
			name: "nothing declared falls back to nav order",
			reg: []AppMeta{
				{NavLabel: "Coming Soon", RoutePrefix: "/app/coming-soon"},
				{NavLabel: "My Characters", RoutePrefix: "/app/dashboard"},
			},
			want: "/app/coming-soon",
		},
		{
			// A redirect to "/" from "/" would loop. A bare landing page is the
			// less bad failure.
			name: "no apps at all does not loop",
			want: "/",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Core{registry: tc.reg, home: tc.home}
			if got := c.homePath(); got != tc.want {
				t.Errorf("homePath() = %q, want %q", got, tc.want)
			}
		})
	}
}

// homeStub is a stub app that can claim to be Home, which newStub cannot.
func homeStub(slug, label string, home bool) *stubApp {
	a := newStub(slug, label, slug+"-body", false)
	a.meta.Home = home
	return a
}

// TestMountRejectsTwoHomes: "where does signing in go" must have one answer.
// Two apps claiming it is a configuration mistake that would otherwise resolve
// silently to whichever happened to be registered first.
func TestMountRejectsTwoHomes(t *testing.T) {
	_, err := Mount(testCore(t), emptyAuthHandlers(), []App{
		homeStub("guild", "", true),
		homeStub("dashboard", "My Characters", true),
	})
	if err == nil {
		t.Fatal("Mount accepted two Home apps; which one is home is then arbitrary")
	}
	if !strings.Contains(err.Error(), "Home") {
		t.Errorf("error = %v, should name the problem", err)
	}
}

// TestUnlabelledAppsAreNotInTheNav covers the guild overview's shape: it is the
// brand link's destination, so listing it in the navigation beside that link
// would be the same place twice.
func TestUnlabelledAppsAreNotInTheNav(t *testing.T) {
	core := testCore(t)
	handler, err := Mount(core, emptyAuthHandlers(), []App{
		homeStub("guild", "", true),
		newStub("dashboard", "My Characters", "dash", false),
		newStub("coming-soon", "Coming Soon", "soon", false),
	})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	if got := core.homePath(); got != "/app/guild" {
		t.Errorf("homePath() = %q, want /app/guild", got)
	}

	// Rendered nav, rather than the registry, because that is what a member
	// sees. An anonymous visitor gets no nav at all, so this only asserts the
	// unlabelled app is absent from the markup.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(rec.Body.String(), "/app/guild") {
		t.Error("the unlabelled Home app appeared in the page chrome")
	}
}
