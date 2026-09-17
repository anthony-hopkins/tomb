package assistant_test

import (
	"context"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/ai"
	"github.com/anthony-hopkins/tomb/internal/apps/assistant"
	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// memberClient is the least a Blizzard client needs for a member behind the
// real core: the account's one character, in the guild.
type memberClient struct{ blizzard.Client }

func (memberClient) UserInfo(context.Context, string) (blizzard.Identity, error) {
	return blizzard.Identity{Sub: "sub", BattleTag: "Tester#1234"}, nil
}

func (memberClient) AccountCharacters(context.Context, string) ([]blizzard.CharacterRef, error) {
	return []blizzard.CharacterRef{{Name: "Maintank", RealmSlug: "area-52"}}, nil
}

func (memberClient) CharacterProfile(_ context.Context, _ string, ref blizzard.CharacterRef) (blizzard.Character, error) {
	return blizzard.Character{Name: ref.Name, RealmSlug: ref.RealmSlug, RealmName: "Area 52", Class: "Warrior", ActiveSpec: "Protection", Level: 90,
		LastLogin: time.Now(), Guild: &blizzard.Guild{Name: "TOMB", RealmSlug: "area-52"}}, nil
}

func (memberClient) CharacterEquipment(context.Context, string, blizzard.CharacterRef) ([]blizzard.EquippedItem, error) {
	return nil, nil
}

type quietChat struct{}

func (quietChat) Ask(context.Context, string, []ai.Turn, ai.AskOptions) (ai.Answer, ai.Usage, error) {
	return ai.Answer{Text: "ok"}, ai.Usage{}, nil
}

// otherApp is another app's page, drawn through the shared shell the way
// a real app draws it, so the shell's panel is there to find.
type otherApp struct {
	render func(w http.ResponseWriter, r *http.Request, status int, title string, content template.HTML)
}

func (*otherApp) Meta() platform.AppMeta {
	return platform.AppMeta{Slug: "guild", RoutePrefix: "/app/guild", Home: true, RequiresGuild: true}
}

func (o *otherApp) Routes(r platform.Registrar) {
	r.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { o.render(w, r, http.StatusOK, "Guild", "guild-body") }))
}

// TestAssistantBehindTheCore: the page answers at the app's root as the
// core serves it -- the path the panel's "Open as a page" link and every
// redirect use -- and the panel is on another app's page for a member.
func TestAssistantBehindTheCore(t *testing.T) {
	templates, err := platform.LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := memberClient{}
	guild := platform.GuildConfig{Name: "TOMB", RealmSlug: "area-52", OfficerRank: 1}
	core := &platform.Core{
		Deps:      platform.Deps{Logger: logger, Blizzard: client, Guild: guild, Chat: quietChat{}, Config: platform.Config{Timezone: time.UTC}},
		Sessions:  &auth.SessionManager{Store: &auth.Store{}},
		Profiles:  &platform.ProfileFetcher{Client: client, Guild: guild, Logger: logger},
		CSRF:      &platform.CSRF{},
		Templates: templates,
	}
	core.Deps.RenderInLayout = core.RenderInLayout
	app, err := assistant.New(core.Deps, &assistant.MemStore{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := platform.Mount(core, &auth.Handlers{Logger: logger}, []platform.App{app, &otherApp{render: core.RenderInLayout}})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r = r.WithContext(platform.ContextWithSession(r.Context(), auth.Session{
			User: auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"}, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour),
		}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}

	// Another app's page carries the panel, and its link leads somewhere.
	rec := get("/app/guild")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "guild-body") || !strings.Contains(body, `class="assistant"`) {
		t.Fatalf("guild page = %d, panel present = %v", rec.Code, strings.Contains(body, `class="assistant"`))
	}
	link := "/app/assistant"
	if !strings.Contains(body, `href="`+link+`"`) {
		t.Fatalf("the panel does not link to %s", link)
	}
	rec = get(link)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `class="assistant-page"`) {
		t.Errorf("GET %s = %d, want the assistant's page", link, rec.Code)
	}
	// The wording on the page for a refusal reaches it at the same root.
	rec = get(link + "?e=long")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "at most 600 characters") {
		t.Errorf("GET %s?e=long = %d without the refusal", link, rec.Code)
	}
}
