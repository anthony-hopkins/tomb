package dashboard

import (
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// ts is a readable fixed timestamp helper.
func ts(min int) time.Time {
	return time.Date(2026, 9, 13, 12, min, 0, 0, time.UTC)
}

func ch(name string, lastLogin time.Time, level, ilvl int) blizzard.Character {
	return blizzard.Character{
		Name:             name,
		RealmSlug:        "area-52",
		Level:            level,
		AverageItemLevel: ilvl,
		LastLogin:        lastLogin,
	}
}

// TestSelectCurrent exercises FR-006's total order:
// LastLogin desc -> Level desc -> AverageItemLevel desc -> Name asc.
func TestSelectCurrent(t *testing.T) {
	tests := []struct {
		name       string
		characters []blizzard.Character
		want       string // expected winning character name; "" means nil
	}{
		{
			name:       "no characters selects nothing",
			characters: nil,
			want:       "",
		},
		{
			name:       "single character wins by default",
			characters: []blizzard.Character{ch("Solo", ts(10), 80, 600)},
			want:       "Solo",
		},
		{
			name: "most recent last login wins outright",
			characters: []blizzard.Character{
				ch("Older", ts(10), 80, 700),
				ch("Newest", ts(30), 20, 100),
				ch("Middle", ts(20), 80, 650),
			},
			// Level and item level are irrelevant while timestamps differ.
			want: "Newest",
		},
		{
			name: "timestamp tie broken by higher level",
			characters: []blizzard.Character{
				ch("LowLevel", ts(30), 20, 900),
				ch("HighLevel", ts(30), 80, 100),
			},
			want: "HighLevel",
		},
		{
			name: "timestamp and level tie broken by higher item level",
			characters: []blizzard.Character{
				ch("Poorly", ts(30), 80, 600),
				ch("Geared", ts(30), 80, 640),
			},
			want: "Geared",
		},
		{
			name: "full tie broken by name ascending",
			characters: []blizzard.Character{
				ch("Zeta", ts(30), 80, 640),
				ch("Alpha", ts(30), 80, 640),
				ch("Mid", ts(30), 80, 640),
			},
			want: "Alpha",
		},
		{
			name: "name tiebreaker is case-insensitive",
			characters: []blizzard.Character{
				ch("bravo", ts(30), 80, 640),
				ch("Alpha", ts(30), 80, 640),
			},
			// Naive byte ordering would put "Alpha" after "bravo" because
			// uppercase sorts first; FR-006 says case-insensitive.
			want: "Alpha",
		},
		{
			name: "all four keys exercised at once",
			characters: []blizzard.Character{
				ch("StaleButStrong", ts(10), 80, 700),  // loses on timestamp
				ch("FreshLowLevel", ts(40), 60, 700),   // loses on level
				ch("FreshHighPoor", ts(40), 80, 500),   // loses on item level
				ch("Zzz", ts(40), 80, 700),             // loses on name
				ch("FreshHighGeared", ts(40), 80, 700), // wins
			},
			want: "FreshHighGeared",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectCurrent(tc.characters)

			if tc.want == "" {
				if got != nil {
					t.Fatalf("SelectCurrent() = %q, want nil", got.Name)
				}
				return
			}

			if got == nil {
				t.Fatalf("SelectCurrent() = nil, want %q", tc.want)
			}
			if got.Name != tc.want {
				t.Errorf("SelectCurrent() = %q, want %q", got.Name, tc.want)
			}
			if !got.IsCurrent {
				t.Error("winning character should have IsCurrent set")
			}
		})
	}
}

// TestSelectCurrentIsDeterministic is the point of FR-006's total order: the
// same input must always yield the same winner, or the dashboard would show a
// different character on each page load.
func TestSelectCurrentIsDeterministic(t *testing.T) {
	characters := []blizzard.Character{
		ch("Alpha", ts(30), 80, 640),
		ch("Bravo", ts(30), 80, 640),
		ch("Charlie", ts(30), 80, 640),
		ch("Delta", ts(30), 80, 640),
	}

	first := SelectCurrent(characters)
	if first == nil {
		t.Fatal("expected a winner")
	}

	for i := 0; i < 50; i++ {
		got := SelectCurrent(characters)
		if got.Name != first.Name {
			t.Fatalf("selection is not deterministic: got %q then %q", first.Name, got.Name)
		}
	}
}

// TestSelectCurrentDoesNotMutateInput guards the shared per-request profile:
// the core hands the same slice to every component, so selection must not
// reorder it or set IsCurrent on the caller's data.
func TestSelectCurrentDoesNotMutateInput(t *testing.T) {
	characters := []blizzard.Character{
		ch("Older", ts(10), 80, 600),
		ch("Newest", ts(30), 80, 600),
	}

	if got := SelectCurrent(characters); got.Name != "Newest" {
		t.Fatalf("winner = %q, want Newest", got.Name)
	}

	if characters[0].Name != "Older" || characters[1].Name != "Newest" {
		t.Errorf("input slice was reordered: %q, %q", characters[0].Name, characters[1].Name)
	}
	for i, c := range characters {
		if c.IsCurrent {
			t.Errorf("input character %d was mutated: IsCurrent set", i)
		}
	}
}
