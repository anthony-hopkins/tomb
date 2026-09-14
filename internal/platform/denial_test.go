package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// captureFetcher returns a fetcher whose logs are captured as parsed JSON
// records, so assertions are on structured fields rather than substrings.
func captureFetcher(t *testing.T, c blizzard.Client, guild GuildConfig) (*ProfileFetcher, func() []map[string]any) {
	t.Helper()

	var buf bytes.Buffer
	f := &ProfileFetcher{
		Client: c,
		Guild:  guild,
		Logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}

	return f, func() []map[string]any {
		var records []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatalf("log line is not JSON: %v\n%s", err, line)
			}
			records = append(records, record)
		}
		return records
	}
}

func denialRecord(t *testing.T, records []map[string]any) map[string]any {
	t.Helper()

	for _, record := range records {
		if record["msg"] == "guild membership denied" {
			return record
		}
	}
	t.Fatal("no 'guild membership denied' record was logged")
	return nil
}

// TestDenialNamesTheGuildsItSaw is the diagnostic that was missing when a real
// member was turned away and the logs offered nothing to distinguish the
// possible causes.
//
// A member who is refused wants to know which of three things happened: their
// character never arrived, their guild name does not match, or their guild's
// realm does not. All three render the same "TOMB members only" page.
func TestDenialNamesTheGuildsItSaw(t *testing.T) {
	// Two characters returned, neither in TOMB, and a third dropped entirely.
	fake := &fakeClient{
		refs: refsFor("Alpha", "Beta", "Gone"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			switch ref.Name {
			case "Gone":
				return blizzard.Character{}, &blizzard.APIError{
					Endpoint: "character-profile-summary",
					Outcome:  blizzard.OutcomeNotFound,
				}
			case "Alpha":
				return memberOf("Alpha", "WRATH"), nil
			default:
				return memberOf("Beta", "WRATH"), nil
			}
		},
	}

	f, logs := captureFetcher(t, fake, GuildConfig{Name: "TOMB", RealmSlug: "area-52"})

	p, err := f.Fetch(context.Background(), "token")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if p.Membership.IsMember {
		t.Fatal("membership should have been denied")
	}

	record := denialRecord(t, logs())

	if got := record["want"]; got != "TOMB@area-52" {
		t.Errorf("want = %v, expected the configured guild identity TOMB@area-52", got)
	}
	if got := record["characters_considered"]; got != float64(2) {
		t.Errorf("characters_considered = %v, want 2", got)
	}
	// The dropped character is the one worth surfacing: it is the difference
	// between "you are not in the guild" and "we never looked at your main".
	if got := record["characters_dropped"]; got != float64(1) {
		t.Errorf("characters_dropped = %v, want 1", got)
	}

	seen, ok := record["guilds_seen"].([]any)
	if !ok {
		t.Fatalf("guilds_seen = %v, want a list", record["guilds_seen"])
	}
	if len(seen) != 1 || seen[0] != "WRATH@area-52" {
		t.Errorf("guilds_seen = %v, want exactly [WRATH@area-52] (deduplicated)", seen)
	}
}

// TestDenialDistinguishesRealmMismatch is the case that is otherwise
// indistinguishable from not being in the guild at all: the right guild name on
// the wrong realm slug. Seeing "TOMB@some-other-realm" next to a configured
// "TOMB@area-52" answers in one line what would otherwise take an afternoon.
func TestDenialDistinguishesRealmMismatch(t *testing.T) {
	fake := &fakeClient{
		refs: refsFor("Main"),
		profileFor: func(blizzard.CharacterRef) (blizzard.Character, error) {
			return blizzard.Character{
				Name:      "Main",
				RealmSlug: "area-52",
				Guild:     &blizzard.Guild{Name: "TOMB", RealmSlug: "some-other-realm"},
				LastLogin: time.Now(),
			}, nil
		},
	}

	f, logs := captureFetcher(t, fake, GuildConfig{Name: "TOMB", RealmSlug: "area-52"})

	if _, err := f.Fetch(context.Background(), "token"); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	record := denialRecord(t, logs())
	seen, _ := record["guilds_seen"].([]any)
	if len(seen) != 1 || seen[0] != "TOMB@some-other-realm" {
		t.Errorf("guilds_seen = %v, want [TOMB@some-other-realm] so the realm mismatch is visible",
			seen)
	}
}

// TestNoDenialLoggedForMembers keeps the diagnostic quiet on the happy path.
// A warning per successful sign-in would be noise, and noise is what stops
// anyone reading warnings at all.
func TestNoDenialLoggedForMembers(t *testing.T) {
	fake := &fakeClient{
		refs: refsFor("Main"),
		profileFor: func(blizzard.CharacterRef) (blizzard.Character, error) {
			return memberOf("Main", "TOMB"), nil
		},
	}

	f, logs := captureFetcher(t, fake, GuildConfig{Name: "TOMB", RealmSlug: "area-52"})

	p, err := f.Fetch(context.Background(), "token")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !p.Membership.IsMember {
		t.Fatal("membership should have been granted")
	}

	for _, record := range logs() {
		if record["msg"] == "guild membership denied" {
			t.Error("a denial was logged for a member who was admitted")
		}
	}
}

// TestDenialLogsNoCharacterNames holds the line the rest of this file holds:
// guild identities are public, character names are not.
func TestDenialLogsNoCharacterNames(t *testing.T) {
	fake := &fakeClient{
		refs: refsFor("Nekromoo", "Lazzlowe"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return memberOf(ref.Name, "WRATH"), nil
		},
	}

	f, logs := captureFetcher(t, fake, GuildConfig{Name: "TOMB", RealmSlug: "area-52"})

	if _, err := f.Fetch(context.Background(), "token"); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	raw, err := json.Marshal(denialRecord(t, logs()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, name := range []string{"Nekromoo", "Lazzlowe"} {
		if strings.Contains(string(raw), name) {
			t.Errorf("the denial log leaked the character name %q: %s", name, raw)
		}
	}
}
