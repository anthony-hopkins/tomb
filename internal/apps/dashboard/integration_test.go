// Package dashboard_test drives the real dashboard app through the real
// platform core, with only Blizzard faked. It covers acceptance scenarios 3, 4,
// 7 and 8, plus FR-010 and FR-016 behaviour that only shows up once the core's
// fetch, gate and rendering are all in play.
//
// It is an external test package so it can import the core without creating an
// import cycle (the core cannot import an app).
package dashboard_test

import (
	"context"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/apps/dashboard"
	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// fakeBlizzard implements the narrow client interface with no network access.
type fakeBlizzard struct {
	refs       []blizzard.CharacterRef
	profileFor func(ref blizzard.CharacterRef) (blizzard.Character, error)
	calls      atomic.Int32
}

func (f *fakeBlizzard) UserInfo(context.Context, string) (blizzard.Identity, error) {
	return blizzard.Identity{Sub: "sub", BattleTag: "Tester#1234"}, nil
}

func (f *fakeBlizzard) AccountCharacters(context.Context, string) ([]blizzard.CharacterRef, error) {
	return f.refs, nil
}

func (f *fakeBlizzard) CharacterProfile(_ context.Context, _ string, ref blizzard.CharacterRef) (blizzard.Character, error) {
	f.calls.Add(1)
	return f.profileFor(ref)
}

var _ blizzard.Client = (*fakeBlizzard)(nil)

func refs(names ...string) []blizzard.CharacterRef {
	out := make([]blizzard.CharacterRef, 0, len(names))
	for _, n := range names {
		out = append(out, blizzard.CharacterRef{Name: n, RealmSlug: "area-52"})
	}
	return out
}

func character(name string, when time.Time, level, ilvl int, guild string) blizzard.Character {
	c := blizzard.Character{
		Name:             name,
		RealmSlug:        "area-52",
		RealmName:        "Area 52",
		Class:            "Warrior",
		ActiveSpec:       "Protection",
		Level:            level,
		AverageItemLevel: ilvl,
		LastLogin:        when,
	}
	if guild != "" {
		c.Guild = &blizzard.Guild{Name: guild, RealmSlug: "area-52"}
	}
	return c
}

// stack wires the real dashboard behind the real core middleware.
func stack(t *testing.T, client blizzard.Client, signedIn bool) http.Handler {
	t.Helper()

	templates, err := platform.LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	core := &platform.Core{
		Deps:     platform.Deps{Logger: logger, Blizzard: client},
		Sessions: &auth.SessionManager{Store: &auth.Store{}},
		Profiles: &platform.ProfileFetcher{
			Client: client,
			Guild:  platform.GuildConfig{Name: "TOMB", RealmSlug: "area-52"},
			Logger: logger,
		},
		CSRF:      &platform.CSRF{},
		Templates: templates,
	}
	core.Deps.RenderInLayout = core.RenderInLayout

	app, err := dashboard.New(core.Deps)
	if err != nil {
		t.Fatalf("dashboard.New() error = %v", err)
	}

	handler, err := platform.Mount(core, &auth.Handlers{Logger: logger}, []platform.App{app})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	if !signedIn {
		return handler
	}

	// Stand in for the session middleware, which needs a database.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := platform.ContextWithSession(r.Context(), auth.Session{
			User:        auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"},
			AccessToken: "token",
			ExpiresAt:   time.Now().Add(time.Hour),
		})
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestShowsMostRecentCharacter is acceptance scenario 3: the card carries name,
// realm, class, level and average item level (FR-007).
func TestShowsMostRecentCharacter(t *testing.T) {
	now := time.Now()
	chars := map[string]blizzard.Character{
		// Higher level and better geared, but played two days ago.
		"Older": character("Older", now.Add(-48*time.Hour), 80, 700, "TOMB"),
		// The one actually played most recently.
		"Newest": character("Newest", now.Add(-1*time.Hour), 62, 410, "TOMB"),
	}

	fake := &fakeBlizzard{
		refs: refs("Older", "Newest"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return chars[ref.Name], nil
		},
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /app/dashboard = %d, want 200", rec.Code)
	}

	body := rec.Body.String()

	// The roster shows every character now, so presence is no longer the
	// question -- ORDER and the current marking are.
	if !strings.Contains(body, "Newest") || !strings.Contains(body, "Older") {
		t.Error("the roster should list every character on the account")
	}
	if strings.Index(body, "Newest") > strings.Index(body, "Older") {
		t.Error("the most recently played character should lead the roster (FR-006)")
	}

	// The first ROW must be the current one and must say so. is-current sits on
	// the row rather than the card so the list still shows which character is
	// the most recent while every card is closed. Splitting on the row class is
	// what lets this assert about one row rather than the page as a whole,
	// where both names appear either way.
	rows := strings.Split(body, `class="character-row`)
	if len(rows) < 3 {
		t.Fatalf("expected two character rows, found %d", len(rows)-1)
	}
	if !strings.Contains(rows[1], "Newest") {
		t.Error("the first row is not the most recently played character")
	}
	if !strings.Contains(rows[1], "is-current") {
		t.Error("the most recently played row is not marked as current")
	}
	if strings.Contains(rows[2], "is-current") {
		t.Error("a stale character is marked as the current one")
	}

	// Every row must be reachable without a mouse. The card is disclosed by
	// :hover and :focus-within, and focus-within needs something focusable.
	for i, row := range rows[1:] {
		if !strings.Contains(row, "tabindex") {
			t.Errorf("row %d is not focusable, so its card is keyboard-unreachable", i+1)
		}
	}

	for _, want := range []string{"Area 52", "Warrior", "62", "410"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard is missing required field value %q (FR-007)", want)
		}
	}
}

// TestRoadmapIsListed covers the placeholder section at the foot of the
// dashboard. It is copy rather than behaviour, but it is copy that makes
// promises, so a silently empty list is worth catching.
func TestRoadmapIsListed(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Main"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return character(ref.Name, time.Now(), 80, 600, "TOMB"), nil
		},
	}

	raw := get(t, stack(t, fake, true), "/app/dashboard").Body.String()

	// Unescaped, so this asserts what a member reads rather than how
	// html/template chose to encode it -- the apostrophe in "Guildmates'"
	// arrives as &#39;, which is correct and not what the test is about.
	body := html.UnescapeString(raw)

	for _, want := range []string{
		"Guildmates' characters",
		"Guild calendar",
		"Ask TOMB Bot",
		"Combat log analysis",
		"Gear analysis",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the roadmap is missing %q", want)
		}
	}

	// The AI entries are tagged; the other two are not. Getting this backwards
	// would advertise a calendar as AI-driven, which is a claim rather than a
	// styling detail.
	if n := strings.Count(raw, `class="tag-ai"`); n != 3 {
		t.Errorf("found %d AI tags, want 3 (Ask TOMB Bot, combat log, gear)", n)
	}

	if !strings.Contains(body, "None of this is built yet") {
		t.Error("the roadmap does not say it is unbuilt; that is the one thing it must say")
	}
}

// TestNonMemberDenied is acceptance scenario 7 and FR-013a.
func TestNonMemberDenied(t *testing.T) {
	tests := []struct {
		name  string
		guild string
	}{
		{"different guild", "Not Tomb"},
		{"no guild at all", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeBlizzard{
				refs: refs("Stranger"),
				profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
					return character(ref.Name, time.Now(), 80, 600, tc.guild), nil
				},
			}

			rec := get(t, stack(t, fake, true), "/app/dashboard")

			body := rec.Body.String()
			if !strings.Contains(body, "TOMB members only") {
				t.Error("a non-member did not get the members-only page")
			}
			if strings.Contains(body, "Stranger") {
				t.Error("a non-member's character data was rendered; no app should be reachable")
			}
		})
	}
}

// TestAnonymousRedirected is scenario 8's mechanism: an absent or expired
// session is unauthenticated, and no character data is rendered or fetched.
func TestAnonymousRedirected(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Maintank"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			t.Error("Blizzard was called for an unauthenticated request")
			return blizzard.Character{}, nil
		},
	}

	rec := get(t, stack(t, fake, false), "/app/dashboard")
	if rec.Code != http.StatusFound {
		t.Errorf("GET /app/dashboard as anonymous = %d, want 302", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "Maintank") {
		t.Error("character data leaked to an unauthenticated request")
	}
}

// TestPartialDataNotice is research.md D9's user-visible half: a character that
// Blizzard could not serve right now is worth telling the member about, because
// it will probably be there next time.
func TestPartialDataNotice(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Good", "Broken"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			if ref.Name == "Broken" {
				return blizzard.Character{}, &blizzard.APIError{
					Endpoint:   "character-profile-summary",
					StatusCode: http.StatusInternalServerError,
					Outcome:    blizzard.OutcomeUnavailable,
				}
			}
			return character(ref.Name, time.Now(), 80, 600, "TOMB"), nil
		},
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /app/dashboard = %d, want 200 despite partial failure", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Good") {
		t.Error("the character that did load is not shown")
	}
	if !strings.Contains(body, "could not be loaded") {
		t.Error("the page does not tell the member the data is incomplete")
	}
}

// TestGoneCharactersAreIgnoredSilently is the other half of that judgement.
//
// Blizzard's account summary keeps listing characters that have been deleted,
// renamed or transferred off the account, and their profiles answer 404 forever
// after. Three of them turn up on a real account here. Treating that as
// "incomplete data" puts a warning on the page on every single visit, for
// characters that are never coming back -- which is how a notice becomes
// wallpaper and stops being read when it matters.
func TestGoneCharactersAreIgnoredSilently(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Live", "Deleted"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			if ref.Name == "Deleted" {
				return blizzard.Character{}, &blizzard.APIError{
					Endpoint:   "character-profile-summary",
					StatusCode: http.StatusNotFound,
					Outcome:    blizzard.OutcomeNotFound,
				}
			}
			return character(ref.Name, time.Now(), 80, 600, "TOMB"), nil
		},
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /app/dashboard = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Live") {
		t.Error("the character that does exist is not shown")
	}
	if strings.Contains(body, "Deleted") {
		t.Error("a character Blizzard reports as gone was rendered anyway")
	}
	if strings.Contains(body, "could not be loaded") {
		t.Error("a character that no longer exists produced an incomplete-data notice")
	}
}

// TestBlizzardUnavailable is FR-010: a clear retry-able error, never a raw
// failure, with Blizzard's own Retry-After honoured.
func TestBlizzardUnavailable(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Maintank"),
		profileFor: func(blizzard.CharacterRef) (blizzard.Character, error) {
			return blizzard.Character{}, &blizzard.APIError{
				Endpoint:   "character-profile-summary",
				StatusCode: http.StatusTooManyRequests,
				Outcome:    blizzard.OutcomeUnavailable,
				RetryAfter: 30 * time.Second,
			}
		},
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")

	body := rec.Body.String()
	if !strings.Contains(body, "not responding") {
		t.Error("expected a retry-able error message")
	}
	if !strings.Contains(body, "Try again") {
		t.Error("the error page offers no way to retry (FR-010)")
	}
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q, want 30", got)
	}
}

// TestRevokedTokenPromptsReLogin is FR-012.
func TestRevokedTokenPromptsReLogin(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Maintank"),
		profileFor: func(blizzard.CharacterRef) (blizzard.Character, error) {
			return blizzard.Character{}, &blizzard.APIError{
				Endpoint:   "character-profile-summary",
				StatusCode: http.StatusUnauthorized,
				Outcome:    blizzard.OutcomeRevoked,
			}
		},
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")

	if rec.Code != http.StatusFound {
		t.Errorf("status = %d, want a 302 to re-login", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "reauth") {
		t.Errorf("Location = %q, want a re-authentication prompt", loc)
	}
	if strings.Contains(rec.Body.String(), "Maintank") {
		t.Error("stale character data was rendered after the token was revoked")
	}
}

// TestNoStoreOnAuthenticatedPages is scenario 4's mechanism.
func TestNoStoreOnAuthenticatedPages(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Maintank"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return character(ref.Name, time.Now(), 80, 600, "TOMB"), nil
		},
	}

	rec := get(t, stack(t, fake, true), "/app/dashboard")
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control = %q, want it to contain no-store", got)
	}
}

// TestFetchesLiveOnEveryView is FR-016: no caching between views.
func TestFetchesLiveOnEveryView(t *testing.T) {
	fake := &fakeBlizzard{
		refs: refs("Maintank"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return character(ref.Name, time.Now(), 80, 600, "TOMB"), nil
		},
	}
	h := stack(t, fake, true)

	const views = 3
	for i := 0; i < views; i++ {
		if rec := get(t, h, "/app/dashboard"); rec.Code != http.StatusOK {
			t.Fatalf("view %d = %d, want 200", i+1, rec.Code)
		}
	}

	// One character call per view. A cache would make this 1.
	if got := fake.calls.Load(); got != views {
		t.Errorf("character fetches = %d, want %d (one per view; FR-016 forbids caching)",
			got, views)
	}
}

// TestDashboardRequiresGuild pins the declaration the core gates on. If this
// ever flips to false the site silently opens to any Battle.net user.
func TestDashboardRequiresGuild(t *testing.T) {
	app, err := dashboard.New(platform.Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("dashboard.New() error = %v", err)
	}

	meta := app.Meta()
	if !meta.RequiresGuild {
		t.Error("RequiresGuild = false; FR-013a requires the dashboard to be guild-gated")
	}
	if meta.RoutePrefix != "/app/"+meta.Slug {
		t.Errorf("RoutePrefix = %q, want /app/%s", meta.RoutePrefix, meta.Slug)
	}
}
