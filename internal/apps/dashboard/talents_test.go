package dashboard_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/apps/dashboard"
	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// talentedBlizzard is the fake with the optional talents and Game Data
// reads, driven by the test.
type talentedBlizzard struct {
	*fakeBlizzard
	specFor func(ref blizzard.CharacterRef) (blizzard.Loadout, error)
	talents map[int]string
}

func (f *talentedBlizzard) CharacterSpecializations(_ context.Context, _ string, ref blizzard.CharacterRef) (blizzard.Loadout, error) {
	return f.specFor(ref)
}

func (f *talentedBlizzard) Talent(_ context.Context, id int) (blizzard.TalentInfo, error) {
	if n, ok := f.talents[id]; ok {
		return blizzard.TalentInfo{ID: id, Name: n}, nil
	}
	return blizzard.TalentInfo{}, errors.New("404")
}

func (f *talentedBlizzard) Item(context.Context, int) (blizzard.ItemInfo, error) {
	return blizzard.ItemInfo{}, errors.New("404")
}

// stackWith is stack with a fights store handed to the app.
func stackWith(t *testing.T, client blizzard.Client, store fights.Store) http.Handler {
	t.Helper()
	templates, err := platform.LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	core := &platform.Core{
		Deps:      platform.Deps{Logger: logger, Blizzard: client, Config: platform.Config{Timezone: time.UTC}},
		Sessions:  &auth.SessionManager{Store: &auth.Store{}},
		Profiles:  &platform.ProfileFetcher{Client: client, Guild: platform.GuildConfig{Name: "TOMB", RealmSlug: "area-52"}, Logger: logger},
		CSRF:      &platform.CSRF{},
		Templates: templates,
	}
	core.Deps.RenderInLayout = core.RenderInLayout
	app, err := dashboard.New(core.Deps)
	if err != nil {
		t.Fatal(err)
	}
	app.Fights = store
	handler, err := platform.Mount(core, &auth.Handlers{Logger: logger}, []platform.App{app})
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := platform.ContextWithSession(r.Context(), auth.Session{
			User: auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"}, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour),
		})
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TestTalentsBlock: Blizzard's loadout when it answers; the latest parsed
// pull when it does not; "unavailable" with neither; and the rest of the
// card renders in every case (FR-033, US2 scenarios 1 and 2).
func TestTalentsBlock(t *testing.T) {
	now := time.Now()
	base := &fakeBlizzard{
		refs: refs("Nekromoo"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return character("Nekromoo", now, 90, 312, "TOMB"), nil
		},
	}
	loadout := blizzard.Loadout{
		Spec: "Blood", HeroTree: "Deathbringer",
		Class:       []blizzard.TalentChoice{{ID: 1, Name: "Icebound Fortitude", Rank: 1}},
		SpecTalents: []blizzard.TalentChoice{{ID: 2, Name: "Marrowrend", Rank: 1}, {ID: 3, Name: "Improved Death Strike", Rank: 2}},
		Hero:        []blizzard.TalentChoice{{ID: 4, Name: "Reaper's Mark", Rank: 1}},
	}
	withPull := func() *fights.MemStore {
		store := fights.NewMemStore()
		u, _ := store.Begin(context.Background(), fights.Upload{UserID: 1, Fingerprint: []byte("x"), PiecesTotal: 1})
		_ = store.AddFights(context.Background(), u.ID, []fights.Fight{{
			EncounterName: "Vexie", StartedAt: time.Date(2026, 9, 14, 20, 0, 0, 0, time.UTC),
			Summaries: []fights.Summary{{Name: "Nekromoo", RealmSlug: "area-52", Talents: []fights.Talent{{Node: 1, Entry: 96301, Rank: 1}, {Node: 2, Entry: 99999, Rank: 1}}}},
		}})
		return store
	}

	tests := []struct {
		name    string
		specFor func(blizzard.CharacterRef) (blizzard.Loadout, error)
		store   fights.Store
		want    []string
		wantNot []string
	}{
		{
			"Blizzard answers",
			func(blizzard.CharacterRef) (blizzard.Loadout, error) { return loadout, nil },
			withPull(),
			[]string{"Current build, from Blizzard", "Deathbringer", "Icebound Fortitude", "Marrowrend", "Improved Death Strike", "&times;2", "Reaper&#39;s Mark"},
			[]string{"From your pull", "unavailable"},
		},
		{
			"Blizzard empty, a pull recorded the build",
			func(blizzard.CharacterRef) (blizzard.Loadout, error) {
				return blizzard.Loadout{}, blizzard.ErrNoLoadout
			},
			withPull(),
			[]string{"From your pull on 14 Sep 2026", "Marrowrend", "99999"},
			[]string{"Current build, from Blizzard", "unavailable"},
		},
		{
			"Blizzard errors, a pull recorded the build",
			func(blizzard.CharacterRef) (blizzard.Loadout, error) { return blizzard.Loadout{}, errors.New("boom") },
			withPull(),
			[]string{"From your pull on 14 Sep 2026"},
			nil,
		},
		{
			"Blizzard empty, no pull",
			func(blizzard.CharacterRef) (blizzard.Loadout, error) {
				return blizzard.Loadout{}, blizzard.ErrNoLoadout
			},
			fights.NewMemStore(),
			[]string{"Talents are unavailable"},
			[]string{"Marrowrend"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &talentedBlizzard{fakeBlizzard: base, specFor: tc.specFor, talents: map[int]string{96301: "Marrowrend"}}
			rec := get(t, stackWith(t, client, tc.store), "/app/dashboard")
			if rec.Code != http.StatusOK {
				t.Fatalf("GET = %d", rec.Code)
			}
			body := rec.Body.String()
			// The rest of the card is there regardless.
			if !strings.Contains(body, "Nekromoo") || !strings.Contains(body, "Average item level") {
				t.Error("the card itself did not render")
			}
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, not := range tc.wantNot {
				if strings.Contains(body, not) {
					t.Errorf("unexpected %q", not)
				}
			}
		})
	}

	// A client without the optional reads at all: the card still renders,
	// with the pull's record.
	rec := get(t, stackWith(t, base, withPull()), "/app/dashboard")
	if body := rec.Body.String(); !strings.Contains(body, "From your pull") {
		t.Error("a client without the talents read did not fall through to the pull")
	}
}
