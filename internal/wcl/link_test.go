package wcl

import (
	"errors"
	"testing"
)

// TestParseCharacterLink covers the table in contracts/external-apis.md.
func TestParseCharacterLink(t *testing.T) {
	tests := []struct {
		in   string
		want CharacterRef
		bad  bool
	}{
		{"https://www.warcraftlogs.com/character/us/area-52/nekromoo", CharacterRef{Region: "us", Slug: "area-52", Name: "nekromoo"}, false},
		{"https://www.warcraftlogs.com/character/EU/Twisting-Nether/Some%C3%B1ame?zone=44", CharacterRef{Region: "eu", Slug: "twisting-nether", Name: "Someñame"}, false},
		{"warcraftlogs.com/character/us/area-52/Nekromoo/", CharacterRef{Region: "us", Slug: "area-52", Name: "Nekromoo"}, false},
		{"http://www.warcraftlogs.com/character/us/area-52/nekromoo", CharacterRef{Region: "us", Slug: "area-52", Name: "nekromoo"}, false},
		{"  https://www.warcraftlogs.com/character/id/1234567  ", CharacterRef{ID: 1234567}, false},
		{"https://www.warcraftlogs.com/reports/aBcD1234", CharacterRef{}, true},
		{"https://www.warcraftlogs.com/character/us/area-52", CharacterRef{}, true},
		{"https://www.warcraftlogs.com/character/xx/area-52/name", CharacterRef{}, true},
		{"https://www.warcraftlogs.com/character/id/abc", CharacterRef{}, true},
		{"https://evil.example/character/us/area-52/name", CharacterRef{}, true},
		{"https://notwarcraftlogs.com/character/us/area-52/name", CharacterRef{}, true},
		{"", CharacterRef{}, true},
		{"not a link at all", CharacterRef{}, true},
	}
	for _, tc := range tests {
		got, err := ParseCharacterLink(tc.in)
		if tc.bad {
			if !errors.Is(err, ErrNotCharacterLink) {
				t.Errorf("ParseCharacterLink(%q) = %+v, %v; want ErrNotCharacterLink", tc.in, got, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("ParseCharacterLink(%q) = %+v, %v; want %+v", tc.in, got, err, tc.want)
		}
	}
}

// TestDifficultyAndMetric: the game's ids to Warcraft Logs', and healers by
// healing.
func TestDifficultyAndMetric(t *testing.T) {
	for game, want := range map[int]int{17: 1, 14: 3, 15: 4, 16: 5} {
		if got, err := Difficulty(game); err != nil || got != want {
			t.Errorf("Difficulty(%d) = %d, %v; want %d", game, got, err, want)
		}
	}
	if _, err := Difficulty(23); !errors.Is(err, ErrUnsupportedDifficulty) {
		t.Errorf("Difficulty(23) err = %v", err)
	}
	for spec, want := range map[int]string{250: "dps", 65: "hps", 257: "hps", 1468: "hps", 0: "dps"} {
		if got := MetricFor(spec); got != want {
			t.Errorf("MetricFor(%d) = %q, want %q", spec, got, want)
		}
	}
}
