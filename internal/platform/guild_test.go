package platform

import (
	"testing"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

func guilded(name, guildName, guildRealm string) blizzard.Character {
	return blizzard.Character{
		Name:      name,
		RealmSlug: "area-52",
		Guild:     &blizzard.Guild{Name: guildName, RealmSlug: guildRealm},
	}
}

func unguilded(name string) blizzard.Character {
	// Blizzard omits the guild field entirely for an unguilded character, so
	// this is nil rather than an empty Guild (research.md D4).
	return blizzard.Character{Name: name, RealmSlug: "area-52", Guild: nil}
}

// TestGuildConfigDerive covers FR-013 membership verification.
func TestGuildConfigDerive(t *testing.T) {
	cfg := GuildConfig{Name: "TOMB", RealmSlug: "area-52"}

	tests := []struct {
		name        string
		characters  []blizzard.Character
		wantMember  bool
		wantMatched string
	}{
		{
			name:       "no characters is not a member",
			characters: nil,
			wantMember: false,
		},
		{
			name:       "unguilded character is not a member",
			characters: []blizzard.Character{unguilded("Loner")},
			wantMember: false,
		},
		{
			name:        "matching guild on matching realm is a member",
			characters:  []blizzard.Character{guilded("Main", "TOMB", "area-52")},
			wantMember:  true,
			wantMatched: "Main",
		},
		{
			name: "same guild name on a different realm is NOT a member",
			// Guild names are not globally unique, so the realm must match too.
			characters: []blizzard.Character{guilded("Impostor", "TOMB", "stormrage")},
			wantMember: false,
		},
		{
			name:       "different guild on the right realm is not a member",
			characters: []blizzard.Character{guilded("Stranger", "NOT TOMB", "area-52")},
			wantMember: false,
		},
		{
			name: "any one matching character grants membership",
			characters: []blizzard.Character{
				unguilded("Alt1"),
				guilded("Alt2", "Some Other Guild", "area-52"),
				guilded("TheMain", "TOMB", "area-52"),
			},
			wantMember:  true,
			wantMatched: "TheMain",
		},
		{
			name: "guild name comparison is case-insensitive",
			// Blizzard's display casing should not decide access.
			characters:  []blizzard.Character{guilded("Main", "tomb", "AREA-52")},
			wantMember:  true,
			wantMatched: "Main",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cfg.Derive(tc.characters)

			if got.IsMember != tc.wantMember {
				t.Fatalf("IsMember = %v, want %v", got.IsMember, tc.wantMember)
			}
			if !tc.wantMember {
				if got.MatchedCharacter != nil {
					t.Errorf("MatchedCharacter = %q, want nil for a non-member",
						got.MatchedCharacter.Name)
				}
				return
			}
			if got.MatchedCharacter == nil {
				t.Fatal("MatchedCharacter = nil, want the character that matched")
			}
			if got.MatchedCharacter.Name != tc.wantMatched {
				t.Errorf("MatchedCharacter = %q, want %q",
					got.MatchedCharacter.Name, tc.wantMatched)
			}
		})
	}
}

// TestCharacterInGuild covers the comparison primitive directly, including the
// nil-guild case that FR-013a turns into a denial.
func TestCharacterInGuild(t *testing.T) {
	tests := []struct {
		name      string
		character blizzard.Character
		guildName string
		realmSlug string
		want      bool
	}{
		{"nil guild never matches", unguilded("X"), "TOMB", "area-52", false},
		{"exact match", guilded("X", "TOMB", "area-52"), "TOMB", "area-52", true},
		{"name mismatch", guilded("X", "TOMBB", "area-52"), "TOMB", "area-52", false},
		{"realm mismatch", guilded("X", "TOMB", "kil-jaeden"), "TOMB", "area-52", false},
		{"whitespace is trimmed", guilded("X", " TOMB ", " area-52 "), "TOMB", "area-52", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.character.InGuild(tc.guildName, tc.realmSlug); got != tc.want {
				t.Errorf("InGuild(%q, %q) = %v, want %v",
					tc.guildName, tc.realmSlug, got, tc.want)
			}
		})
	}
}
