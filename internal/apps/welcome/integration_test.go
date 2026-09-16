package welcome_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/apps/welcome"
	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// siteClient is the least a Blizzard client needs for the front door: the
// roster, one profile, and the site's token.
type siteClient struct{ blizzard.Client }

func (siteClient) AppToken(context.Context) (string, error) { return "site-token", nil }

func (siteClient) GuildRoster(context.Context, string, string, string) ([]blizzard.GuildMember, error) {
	return []blizzard.GuildMember{
		{Name: "Azelora", RealmSlug: "elune", RealmName: "Elune", Rank: 0, Level: 90, Class: "Monk"},
		{Name: "Nekromoo", RealmSlug: "area-52", RealmName: "Area 52", Rank: 4, Level: 90, Class: "Death Knight"},
	}, nil
}

func (siteClient) CharacterProfile(_ context.Context, _ string, ref blizzard.CharacterRef) (blizzard.Character, error) {
	return blizzard.Character{Name: ref.Name, RealmSlug: ref.RealmSlug, RealmName: "Elune", Class: "Monk", ActiveSpec: "Mistweaver", Level: 90, AverageItemLevel: 300}, nil
}

func (siteClient) MythicPlusRating(context.Context, string, blizzard.CharacterRef) (int, error) {
	return 0, nil
}

func (siteClient) RaidProgression(context.Context, string, blizzard.CharacterRef) ([]blizzard.RaidProgress, error) {
	return nil, nil
}

// stack mounts the front door and a guild-gated home behind the real core.
func stack(t *testing.T) (*welcome.App, http.Handler) {
	t.Helper()
	templates, err := platform.LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := siteClient{}
	guild := platform.GuildConfig{Name: "TOMB", RealmSlug: "elune", OfficerRank: 1}
	core := &platform.Core{
		Deps: platform.Deps{
			Logger: logger, Blizzard: client, Guild: guild,
			Roster: &platform.RosterCache{Client: client, Guild: guild, Logger: logger, TTL: time.Hour},
			Config: platform.Config{Timezone: time.UTC},
		},
		Sessions:  &auth.SessionManager{Store: &auth.Store{}},
		Profiles:  &platform.ProfileFetcher{Client: client, Guild: guild, Logger: logger},
		CSRF:      &platform.CSRF{},
		Templates: templates,
	}
	core.Deps.RenderInLayout = core.RenderInLayout
	app, err := welcome.New(core.Deps)
	if err != nil {
		t.Fatal(err)
	}
	home := &homeApp{}
	handler, err := platform.Mount(core, &auth.Handlers{Logger: logger}, []platform.App{app, home})
	if err != nil {
		t.Fatal(err)
	}
	return app, handler
}

type homeApp struct{}

func (*homeApp) Meta() platform.AppMeta {
	return platform.AppMeta{Slug: "guild", RoutePrefix: "/app/guild", Home: true, RequiresGuild: true}
}

func (*homeApp) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("home")) }))
}

func get(handler http.Handler, path string, signedIn bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if signedIn {
		r = r.WithContext(platform.ContextWithSession(r.Context(), auth.Session{
			User: auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"}, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour),
		}))
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	return rec
}

// TestFrontDoorBehindTheCore: "/" for an anonymous visitor is this page, in
// the shared layout with no navigation, carrying the sign-in action and the
// core's notice; a signed-in viewer is sent home from "/" and can still open
// the page at its own path, where the layout does have navigation.
func TestFrontDoorBehindTheCore(t *testing.T) {
	app, handler := stack(t)
	app.Warm(context.Background())

	rec := get(handler, "/", false)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
	for _, want := range []string{"<title>Welcome · TOMB</title>", "Sign in with Battle.net", "Welcome to TOMB", "Azelora", "Guild Master", "TOMB Cares", "Top item level"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / is missing %q", want)
		}
	}
	for _, never := range []string{`<nav aria-label="Apps">`, "Log out", "Tester#1234"} {
		if strings.Contains(body, never) {
			t.Errorf("GET / contains %q", never)
		}
	}
	// Nekromoo, rank 4, is on a board but is not listed as running the place.
	if n := strings.Count(body, `class="officer-rank"`); n != 1 || !strings.Contains(body, "Nekromoo") {
		t.Errorf("%d officers listed, want 1 (the guild master), with Nekromoo on a board", n)
	}
	if rec.Header().Get("Cache-Control") == "" {
		t.Error("the page is served without a cache policy")
	}

	rec = get(handler, "/?signed_out=1", false)
	if !strings.Contains(rec.Body.String(), "You have been signed out") {
		t.Error("the signed-out notice is not shown on the front door")
	}

	rec = get(handler, "/", true)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/app/guild" {
		t.Errorf("GET / signed in = %d to %q, want 302 to /app/guild", rec.Code, rec.Header().Get("Location"))
	}
	rec = get(handler, "/app/welcome", true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Open the guild") || !strings.Contains(rec.Body.String(), "Log out") {
		t.Errorf("GET /app/welcome signed in = %d; want the signed-in page in the full shell", rec.Code)
	}
}
