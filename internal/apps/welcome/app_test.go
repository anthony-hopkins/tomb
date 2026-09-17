package welcome

import (
	"context"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// fakeClient answers the roster and per-character profiles, and can mint the
// site's token or not, which is the difference between a page with numbers
// and one without.
type fakeClient struct {
	blizzard.Client

	roster    []blizzard.GuildMember
	rosterErr error
	byName    map[string]blizzard.Character
	ratings   map[string]int
	tokenErr  error
	tokens    atomic.Int32
	profiles  atomic.Int32
}

func (f *fakeClient) AppToken(context.Context) (string, error) {
	f.tokens.Add(1)
	return "site-token", f.tokenErr
}

func (f *fakeClient) GuildRoster(context.Context, string, string, string) ([]blizzard.GuildMember, error) {
	return f.roster, f.rosterErr
}

func (f *fakeClient) CharacterProfile(_ context.Context, token string, ref blizzard.CharacterRef) (blizzard.Character, error) {
	f.profiles.Add(1)
	if token != "site-token" {
		return blizzard.Character{}, errors.New("not the site's token")
	}
	c, ok := f.byName[strings.ToLower(ref.Name)]
	if !ok {
		return blizzard.Character{}, errors.New("no such character")
	}
	return c, nil
}

func (f *fakeClient) MythicPlusRating(_ context.Context, _ string, ref blizzard.CharacterRef) (int, error) {
	return f.ratings[strings.ToLower(ref.Name)], nil
}

func (f *fakeClient) RaidProgression(context.Context, string, blizzard.CharacterRef) ([]blizzard.RaidProgress, error) {
	return []blizzard.RaidProgress{{Name: "Raid", Modes: []blizzard.RaidMode{{Difficulty: "HEROIC", Completed: 3, Total: 8}}}}, nil
}

// noToken is a client that cannot mint the site's token: the plain fake the
// dashboard tests use, with no AppToken method.
type noToken struct{ blizzard.Client }

var _ blizzard.AppTokenSource = (*fakeClient)(nil)

func guildOf() *fakeClient {
	return &fakeClient{
		roster: []blizzard.GuildMember{
			{Name: "Azelora", RealmSlug: "elune", RealmName: "Elune", Rank: 0, Level: 90, Class: "Monk"},
			{Name: "Nekromoo", RealmSlug: "area-52", RealmName: "Area 52", Rank: 1, Level: 90, Class: "Death Knight"},
			{Name: "Lazzlowe", RealmSlug: "elune", RealmName: "Elune", Rank: 1, Level: 90, Class: "Paladin"},
			{Name: "Quiet", RealmSlug: "elune", Rank: 1, Level: 80}, // no profile served
			{Name: "Member", RealmSlug: "elune", RealmName: "Elune", Rank: 3, Level: 90, Class: "Mage"},
		},
		byName: map[string]blizzard.Character{
			"azelora":  {Name: "Azelora", RealmSlug: "elune", RealmName: "Elune", Class: "Monk", ActiveSpec: "Mistweaver", Level: 90, AverageItemLevel: 305},
			"nekromoo": {Name: "Nekromoo", RealmSlug: "area-52", RealmName: "Area 52", Class: "Death Knight", ActiveSpec: "Blood", Level: 90, AverageItemLevel: 311},
			"lazzlowe": {Name: "Lazzlowe", RealmSlug: "elune", RealmName: "Elune", Class: "Paladin", ActiveSpec: "Retribution", Level: 90, AverageItemLevel: 298},
			"member":   {Name: "Member", RealmSlug: "elune", RealmName: "Elune", Class: "Mage", ActiveSpec: "Frost", Level: 90, AverageItemLevel: 320},
		},
		ratings: map[string]int{"member": 2600, "nekromoo": 2100},
	}
}

func appOver(t *testing.T, client blizzard.Client, refresh time.Duration) *App {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	guild := platform.GuildConfig{Name: "TOMB", RealmSlug: "elune", Ranks: []string{"Guild Master", "Officer", "Veteran", "Member"}, OfficerRank: 1}
	deps := platform.Deps{
		Blizzard: client,
		Logger:   logger,
		Guild:    guild,
		Roster:   &platform.RosterCache{Client: client, Guild: guild, Logger: logger, TTL: time.Hour},
		Config:   platform.Config{Timezone: time.UTC, DiscordInvite: "https://discord.gg/tomb"},
	}
	deps.RenderInLayout = func(w http.ResponseWriter, _ *http.Request, status int, _ string, content template.HTML) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(content))
	}
	a, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshEvery = refresh
	return a
}

func page(t *testing.T, a *App, signedIn bool) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/app/welcome", nil)
	if signedIn {
		r = r.WithContext(platform.ContextWithSession(r.Context(), auth.Session{User: auth.User{ID: 1, BattleTag: "Tester#1234"}, AccessToken: "member-token"}))
	}
	rec := httptest.NewRecorder()
	a.index(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("index = %d", rec.Code)
	}
	return rec.Body.String()
}

// TestWarmThenPage: after Warm the page names the guild master and officers
// in roster order with their profile detail, the top lists cut to five, the
// total, the Discord link and the sign-in action -- and nothing about any
// account (FR-043, FR-045, FR-046, FR-047).
func TestWarmThenPage(t *testing.T) {
	f := guildOf()
	a := appOver(t, f, time.Hour)
	a.Warm(context.Background())
	if f.tokens.Load() != 1 || f.profiles.Load() != 5 {
		t.Errorf("tokens %d profiles %d; want 1 and 5", f.tokens.Load(), f.profiles.Load())
	}

	body := page(t, a, false)
	for _, want := range []string{
		"Welcome to TOMB", "on Elune,", "Sign in with Battle.net", `href="https://discord.gg/tomb"`,
		`rank-mark--gm`, `rank-mark--officer`, "Azelora", "Nekromoo", "Lazzlowe", "Quiet",
		"Mistweaver Monk &middot; 305 item level &middot; Elune", "Blood Death Knight &middot; 311 item level &middot; Area 52",
		"Guild Master", "Officer", "<b>5</b> characters on the roster",
		"Top item level", "Top Mythic&#43; rating", "Most raid bosses down", "2600", "3H",
		"TOMB Cares", "Listen. Support. Connect.", "988",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	// Officers in roster order: the guild master first, then the officers.
	if strings.Index(body, "Azelora") > strings.Index(body, "Nekromoo") || strings.Index(body, "Nekromoo") > strings.Index(body, "Lazzlowe") {
		t.Error("officers are not in roster order")
	}
	// A rank-3 member is on a board but not on the officer list.
	if got := strings.Count(body, "Member"); strings.Contains(body, `officer-rank">Member`) || got == 0 {
		t.Errorf("Member appears %d times, and must not be listed as an officer", got)
	}
	// Names on the boards are not links.
	if strings.Contains(body, `href="?c=`) {
		t.Error("the top lists link to member pages an anonymous visitor cannot open")
	}
	for _, never := range []string{"Tester#1234", "#1234", "battletag", "member-token", "site-token"} {
		if strings.Contains(body, never) {
			t.Errorf("page contains %q", never)
		}
	}
}

// TestPageBeforeAndWithoutNumbers: the first view renders at once, says the
// numbers are on their way and starts one load; a client without the site's
// token gives the same page with no load at all; and the Discord line changes
// with the configuration (FR-043, FR-049).
func TestPageBeforeAndWithoutNumbers(t *testing.T) {
	f := guildOf()
	a := appOver(t, f, time.Hour)
	body := page(t, a, false)
	for _, want := range []string{"Sign in with Battle.net", "on its way", "TOMB Cares", "on elune,"} {
		if !strings.Contains(body, want) {
			t.Errorf("first page is missing %q", want)
		}
	}
	if strings.Contains(body, "Azelora") {
		t.Error("the first page shows a roster it does not have")
	}
	// The view started a load; a second view while it runs does not start
	// another. Wait for it, then the page has the numbers.
	page(t, a, false)
	deadline := time.Now().Add(5 * time.Second)
	for a.current() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.tokens.Load() != 1 {
		t.Errorf("token minted %d times, want once", f.tokens.Load())
	}
	if !strings.Contains(page(t, a, false), "Azelora") {
		t.Error("the page has no roster after the load")
	}

	// No token source: no numbers, ever, and no error.
	plain := appOver(t, noToken{}, time.Hour)
	plain.deps.Config.DiscordInvite = ""
	body = page(t, plain, false)
	if !strings.Contains(body, "on its way") || !strings.Contains(body, "Whisper any officer") || strings.Contains(body, "discord.gg") {
		t.Error("a site without its own token does not render the plain page")
	}
	plain.Warm(context.Background())
	if plain.current() != nil {
		t.Error("a load happened with no token source")
	}
}

// TestFailuresKeepTheSnapshot: a token that cannot be minted, or a roster
// that cannot be fetched, leaves the last snapshot in place (FR-044).
func TestFailuresKeepTheSnapshot(t *testing.T) {
	f := guildOf()
	a := appOver(t, f, 0) // every view finds it stale
	a.Warm(context.Background())
	held := a.snap
	if held == nil {
		t.Fatal("no snapshot after Warm")
	}
	f.tokenErr = errors.New("blizzard is down")
	a.Warm(context.Background())
	if a.snap != held {
		t.Error("a failed token mint replaced the snapshot")
	}
	f.tokenErr = nil
	f.rosterErr = errors.New("no roster")
	// The core's cache holds the last roster and serves it on a failed
	// refresh, so this load succeeds from cache; the point is that it does
	// not empty the page.
	a.Warm(context.Background())
	if !strings.Contains(page(t, a, false), "Azelora") {
		t.Error("the page lost its roster")
	}
}

// TestSignedInVariant: a member who opens the page sees the way to the guild
// rather than the sign-in form, and the notice from the core when there is
// one.
func TestSignedInVariant(t *testing.T) {
	a := appOver(t, guildOf(), time.Hour)
	body := page(t, a, true)
	if strings.Contains(body, "Sign in with Battle.net") || !strings.Contains(body, `href="/">Open the guild`) {
		t.Error("a signed-in viewer is offered the sign-in form")
	}
	r := httptest.NewRequest(http.MethodGet, "/app/welcome", nil)
	r = r.WithContext(platform.ContextWithLandingMessage(r.Context(), "You have been signed out."))
	rec := httptest.NewRecorder()
	a.index(rec, r)
	if !strings.Contains(rec.Body.String(), `role="status">You have been signed out.`) {
		t.Error("the core's notice is not shown")
	}
}

// TestOfficerThreshold: the configured officer rank decides the list; with
// no rank names the labels fall back to the roster's own.
func TestOfficerThreshold(t *testing.T) {
	f := guildOf()
	a := appOver(t, f, time.Hour)
	a.deps.Guild.OfficerRank = 0
	a.deps.Guild.Ranks = nil
	a.Warm(context.Background())
	snap := a.current()
	got := a.officers(snap)
	if len(got) != 1 || got[0].Name != "Azelora" || got[0].Rank != "Guild Master" || got[0].ClassSlug != "monk" {
		t.Errorf("officers = %+v", got)
	}
	a.deps.Guild.OfficerRank = 3
	got = a.officers(snap)
	if len(got) != 5 || got[4].Rank != "Rank 3" || got[3].Name != "Quiet" || got[3].Class != "" || got[3].Realm != "elune" {
		t.Errorf("officers = %+v", got)
	}
}

// TestOfficersFoldByAccount: characters the site knows to be one person's
// (from their sign-in) fold into one officer line -- the best rank, then the
// highest item level, standing for the rest with a count -- while characters
// of members who have never signed in stay one line each.
func TestOfficersFoldByAccount(t *testing.T) {
	f := guildOf()
	f.roster = append(f.roster,
		blizzard.GuildMember{Name: "Nekalt", RealmSlug: "elune", RealmName: "Elune", Rank: 1, Level: 70, Class: "Mage"},
		blizzard.GuildMember{Name: "Azalt", RealmSlug: "elune", RealmName: "Elune", Rank: 1, Level: 90, Class: "Druid"})
	f.byName["nekalt"] = blizzard.Character{Name: "Nekalt", RealmSlug: "elune", Class: "Mage", Level: 70, AverageItemLevel: 120}
	f.byName["azalt"] = blizzard.Character{Name: "Azalt", RealmSlug: "elune", Class: "Druid", Level: 90, AverageItemLevel: 330}
	a := appOver(t, f, time.Hour)
	owners := &platform.MemOwners{}
	// Nekromoo, Lazzlowe and Nekalt are one account; Azelora and Azalt another.
	_ = owners.Record(context.Background(), 1, []blizzard.Character{{Name: "Nekromoo", RealmSlug: "area-52"}, {Name: "Lazzlowe", RealmSlug: "elune"}, {Name: "Nekalt", RealmSlug: "elune"}})
	_ = owners.Record(context.Background(), 2, []blizzard.Character{{Name: "Azelora", RealmSlug: "elune"}, {Name: "Azalt", RealmSlug: "elune"}})
	a.deps.Owners = owners
	a.Warm(context.Background())

	got := a.officers(a.current())
	var names []string
	for _, o := range got {
		names = append(names, o.Name)
	}
	// Azelora (rank 0) stands for her account over the better-geared Azalt;
	// Nekromoo (311) over Lazzlowe (298) and Nekalt; Quiet never signed in.
	if strings.Join(names, ",") != "Azelora,Nekromoo,Quiet" {
		t.Fatalf("officers = %v", names)
	}
	if got[0].Others != 1 || got[1].Others != 2 || got[2].Others != 0 {
		t.Errorf("others = %d %d %d, want 1 2 0", got[0].Others, got[1].Others, got[2].Others)
	}
	body := page(t, a, false)
	// Three officer lines, with the counts; Lazzlowe is still on the top
	// lists, which are by character, but not on the officer list.
	if n := strings.Count(body, `class="officer-rank"`); n != 3 || !strings.Contains(body, "and 2 more characters") || !strings.Contains(body, "and 1 more character<") {
		t.Errorf("the page does not fold the alts: %d officer lines", n)
	}
}
