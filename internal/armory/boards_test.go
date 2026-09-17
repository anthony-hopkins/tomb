package armory

import (
	"testing"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// TestStandingFloorsAndTies: one entry, or a board where everyone is level,
// gets the full row; nobody gets less than the floor.
func TestStandingFloorsAndTies(t *testing.T) {
	for _, tc := range []struct{ score, lo, hi, want int }{
		{300, 300, 300, 100},
		{7, 7, 69, BarFloor},
		{69, 7, 69, 100},
		{38, 7, 69, 63},
	} {
		if got := Standing(tc.score, tc.lo, tc.hi); got != tc.want {
			t.Errorf("Standing(%d, %d, %d) = %d, want %d", tc.score, tc.lo, tc.hi, got, tc.want)
		}
	}
}

// TestBoardsCut: the size is a cut, not a floor -- fewer qualify, fewer are
// shown; zero means everyone; and the bars are scaled to what is shown, so a
// top five's floor is the fifth place, not the tenth.
func TestBoardsCut(t *testing.T) {
	var members []blizzard.GuildMember
	details := map[string]MemberDetail{}
	for i, name := range []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf"} {
		m := blizzard.GuildMember{Name: name, RealmSlug: "elune", Rank: 3}
		members = append(members, m)
		details[MemberKey(m)] = MemberDetail{Character: blizzard.Character{Name: name, RealmSlug: "elune", Class: "Mage", AverageItemLevel: 300 - i}}
	}
	for _, tc := range []struct{ size, want, lastPct int }{
		{5, 5, BarFloor},
		{10, 7, BarFloor},
		{0, 7, BarFloor},
	} {
		boards := Boards(members, details, tc.size)
		if len(boards) != 1 || boards[0].Title != "Top item level" {
			t.Fatalf("size %d: boards = %+v", tc.size, boards)
		}
		entries := boards[0].Entries
		if len(entries) != tc.want || entries[0].Name != "Alpha" || entries[0].Pct != 100 || entries[len(entries)-1].Pct != tc.lastPct {
			t.Errorf("size %d: %d entries, first %s at %d%%, last at %d%%", tc.size, len(entries), entries[0].Name, entries[0].Pct, entries[len(entries)-1].Pct)
		}
		if entries[0].Class != "mage" || entries[0].Key != "elune/alpha" {
			t.Errorf("entry = %+v", entries[0])
		}
	}
	if got := Boards(members, map[string]MemberDetail{}, 5); got != nil {
		t.Errorf("no detail gave boards: %+v", got)
	}
}
