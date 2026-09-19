package warroom_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/apps/warroom"
	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// rankedClient is a member whose one character holds the given rank on
// the roster, so the core can tell an officer from a member.
type rankedClient struct {
	blizzard.Client
	rank int
}

func (rankedClient) UserInfo(context.Context, string) (blizzard.Identity, error) {
	return blizzard.Identity{Sub: "sub", BattleTag: "Tester#1234"}, nil
}

func (rankedClient) AccountCharacters(context.Context, string) ([]blizzard.CharacterRef, error) {
	return []blizzard.CharacterRef{{Name: "Lazzlowe", RealmSlug: "elune"}}, nil
}

func (rankedClient) CharacterProfile(_ context.Context, _ string, ref blizzard.CharacterRef) (blizzard.Character, error) {
	return blizzard.Character{Name: ref.Name, RealmSlug: ref.RealmSlug, Class: "Warrior", Level: 90, LastLogin: time.Now(),
		Guild: &blizzard.Guild{Name: "TOMB", RealmSlug: "elune"}}, nil
}

func (c rankedClient) GuildRoster(context.Context, string, string, string) ([]blizzard.GuildMember, error) {
	return []blizzard.GuildMember{{Name: "Lazzlowe", RealmSlug: "elune", Rank: c.rank}}, nil
}

// TestOfficersOnlyBehindTheCore (SC-022): an officer reaches the War Room
// and sees it in the navigation; a member below the officer rank is
// refused and does not see it.
func TestOfficersOnlyBehindTheCore(t *testing.T) {
	for _, tc := range []struct {
		name string
		rank int
		want int
	}{
		{"officer", 1, http.StatusOK},
		{"member", 5, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			templates, err := platform.LoadTemplates()
			if err != nil {
				t.Fatal(err)
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			client := rankedClient{rank: tc.rank}
			guild := platform.GuildConfig{Name: "TOMB", RealmSlug: "elune", OfficerRank: 1}
			roster := &platform.RosterCache{Client: client, Guild: guild, Logger: logger, TTL: time.Hour}
			core := &platform.Core{
				Deps:      platform.Deps{Logger: logger, Blizzard: client, Guild: guild, Roster: roster, Config: platform.Config{Timezone: time.UTC}},
				Sessions:  &auth.SessionManager{Store: &auth.Store{}},
				Profiles:  &platform.ProfileFetcher{Client: client, Guild: guild, Logger: logger, Roster: roster},
				CSRF:      &platform.CSRF{},
				Templates: templates,
			}
			core.Deps.RenderInLayout = core.RenderInLayout
			app, err := warroom.New(core.Deps, &warroom.MemStore{})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := platform.Mount(core, &auth.Handlers{Logger: logger}, []platform.App{app})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodGet, "/app/war-room", nil)
			r = r.WithContext(platform.ContextWithSession(r.Context(), auth.Session{
				User: auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"}, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour),
			}))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)
			if rec.Code != tc.want {
				t.Fatalf("GET /app/war-room as %s = %d, want %d", tc.name, rec.Code, tc.want)
			}
			inNav := strings.Contains(rec.Body.String(), `href="/app/war-room">War Room</a>`)
			if inNav != (tc.want == http.StatusOK) {
				t.Errorf("War Room in the navigation = %v", inNav)
			}
		})
	}
}
