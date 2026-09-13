package dashboard

import (
	"sort"
	"strings"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// SelectCurrent returns the user's "most current" character (FR-006).
//
// The ordering is a TOTAL order, applied over successfully fetched characters
// only:
//
//  1. LastLogin descending          — the primary signal
//  2. Level descending              — tiebreak 1
//  3. AverageItemLevel descending   — tiebreak 2
//  4. Name ascending, case-insensitive — tiebreak 3
//
// Keys 2-4 apply only on an exact tie of the preceding key. Because key 4 is
// unique within one account's character list, the result is deterministic for
// identical input — which is exactly why the spec pinned it (FR-006) and what
// makes this table-testable.
//
// Returns nil when there are no characters at all.
func SelectCurrent(characters []blizzard.Character) *blizzard.Character {
	if len(characters) == 0 {
		return nil
	}

	// Copy so the caller's slice order is untouched; the profile is shared
	// across the request and other components may rely on its order.
	ranked := make([]blizzard.Character, len(characters))
	copy(ranked, characters)

	sort.SliceStable(ranked, func(i, j int) bool {
		return less(ranked[i], ranked[j])
	})

	winner := ranked[0]
	winner.IsCurrent = true
	return &winner
}

// less reports whether a should rank ahead of b under FR-006's total order.
func less(a, b blizzard.Character) bool {
	if !a.LastLogin.Equal(b.LastLogin) {
		return a.LastLogin.After(b.LastLogin)
	}
	if a.Level != b.Level {
		return a.Level > b.Level
	}
	if a.AverageItemLevel != b.AverageItemLevel {
		return a.AverageItemLevel > b.AverageItemLevel
	}
	return strings.ToLower(a.Name) < strings.ToLower(b.Name)
}
