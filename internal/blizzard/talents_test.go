package blizzard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestCharacterSpecializations reads the active loadout of the active spec,
// by name, and reports an empty answer as ErrNoLoadout rather than a failure.
func TestCharacterSpecializations(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    []string // spec talent names
		hero    string
		noLoad  bool
	}{
		{"loadouts present", "character-specializations.json", []string{"Marrowrend", "Improved Death Strike"}, "Deathbringer", false},
		{"loadouts absent since 11.2", "character-specializations-empty.json", nil, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotNS string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotNS = r.URL.Path, r.URL.Query().Get("namespace")
				_, _ = w.Write(fixture(t, tc.fixture))
			}))
			defer srv.Close()

			lo, err := newTestClient(srv).CharacterSpecializations(context.Background(), "tok", CharacterRef{Name: "Nekromoo", RealmSlug: "area-52"})
			if gotPath != "/profile/wow/character/area-52/nekromoo/specializations" || gotNS != "profile-us" {
				t.Errorf("path %q namespace %q", gotPath, gotNS)
			}
			if tc.noLoad {
				if !errors.Is(err, ErrNoLoadout) {
					t.Fatalf("err = %v, want ErrNoLoadout", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if lo.Spec != "Blood" || lo.HeroTree != tc.hero || lo.Code == "" {
				t.Errorf("loadout = %+v", lo)
			}
			var names []string
			for _, c := range lo.SpecTalents {
				names = append(names, c.Name)
			}
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Errorf("spec talents = %v, want %v", names, tc.want)
			}
			if len(lo.Class) != 2 || len(lo.Hero) != 1 || lo.SpecTalents[1].Rank != 2 {
				t.Errorf("class/hero/rank = %d/%d/%d", len(lo.Class), len(lo.Hero), lo.SpecTalents[1].Rank)
			}
		})
	}
}

// TestAppToken: minted once with the client credentials grant over basic
// auth, reused until a minute before expiry, refreshed after, and a refusal
// is an error the caller can read.
func TestAppToken(t *testing.T) {
	var mints atomic.Int32
	var gotAuth, gotBody string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		mints.Add(1)
		user, pass, _ := r.BasicAuth()
		gotAuth = user + ":" + pass
		b := make([]byte, 64)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.WriteHeader(status)
		_, _ = w.Write(fixture(t, "app-token.json"))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if _, err := c.AppToken(context.Background()); !errors.Is(err, ErrNoAppCredentials) {
		t.Errorf("without credentials: err = %v", err)
	}
	c.ClientID, c.ClientSecret = "id", "secret"
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	tok, err := c.AppToken(context.Background())
	if err != nil || tok != "APPTOKENxxxxxxxxxxxxxxxxxxxxxxxxxxxx" {
		t.Fatalf("AppToken = %q, %v", tok, err)
	}
	if gotAuth != "id:secret" || gotBody != "grant_type=client_credentials" {
		t.Errorf("auth %q body %q", gotAuth, gotBody)
	}
	if _, _ = c.AppToken(context.Background()); mints.Load() != 1 {
		t.Errorf("minted %d times within the lifetime, want 1", mints.Load())
	}
	now = now.Add(86399*time.Second - 30*time.Second) // inside the last minute
	if _, _ = c.AppToken(context.Background()); mints.Load() != 2 {
		t.Errorf("minted %d times after expiry, want 2", mints.Load())
	}

	status = http.StatusUnauthorized
	now = now.Add(48 * time.Hour)
	var apiErr *APIError
	if _, err := c.AppToken(context.Background()); !errors.As(err, &apiErr) || apiErr.Outcome != OutcomeRevoked {
		t.Errorf("401: err = %v", err)
	}
}

// TestGameData names talents and items with the app token against the
// static namespace, and a 404 is OutcomeNotFound.
func TestGameData(t *testing.T) {
	var gotNS, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write(fixture(t, "app-token.json"))
		case "/data/wow/talent/96301":
			gotNS, gotAuth = r.URL.Query().Get("namespace"), r.Header.Get("Authorization")
			_, _ = w.Write(fixture(t, "talent.json"))
		case "/data/wow/item/212345":
			_, _ = w.Write(fixture(t, "item.json"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.ClientID, c.ClientSecret = "id", "secret"

	tal, err := c.Talent(context.Background(), 96301)
	if err != nil || tal.Name != "Marrowrend" || tal.SpellID != 195182 {
		t.Errorf("Talent = %+v, %v", tal, err)
	}
	if gotNS != "static-us" || gotAuth != "Bearer APPTOKENxxxxxxxxxxxxxxxxxxxxxxxxxxxx" {
		t.Errorf("namespace %q auth %q", gotNS, gotAuth)
	}
	item, err := c.Item(context.Background(), 212345)
	if err != nil || item.Name != "Baleful Grave-Knight's Casque" || item.Quality != "EPIC" || item.SlotType != "HEAD" || item.Level != 311 {
		t.Errorf("Item = %+v, %v", item, err)
	}
	var apiErr *APIError
	if _, err := c.Item(context.Background(), 1); !errors.As(err, &apiErr) || apiErr.Outcome != OutcomeNotFound {
		t.Errorf("unknown item: err = %v", err)
	}
}
