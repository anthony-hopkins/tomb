package combatlogs

import (
	"context"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// TestPickTopAcrossTheRaid: the player best across the raid wins over the
// player first on one boss (sixth amendment); the pick's parse on the main
// boss comes from that boss's page; the pages are cached, so a second pick
// asks Warcraft Logs nothing.
func TestPickTopAcrossTheRaid(t *testing.T) {
	store := fights.NewMemStore()
	w := healthyWCL()
	entry := func(name string, amount float64) wcl.Entry {
		return wcl.Entry{Ref: wcl.CharacterRef{Region: "us", Slug: "area-52", Name: name},
			Rank: wcl.Ranking{Name: name, Class: "Death Knight", Spec: "Blood", Metric: "dps", RankPercent: 100, Amount: amount, Duration: 300 * time.Second, ReportCode: "r" + name, FightID: 1}}
	}
	w.boardOf = map[int][]wcl.Entry{
		3009: {entry("Alpha", 100000), entry("Steady", 90000)},
		3010: {entry("Gamma", 100000), entry("Steady", 95000)},
	}
	a, _ := seeded(t, store, &memAudit{}, w)

	row, err := a.pickTop(context.Background(), []int{3009, 3010}, 3009, 5, "Death Knight", "Blood", "dps")
	if err != nil {
		t.Fatal(err)
	}
	// Steady: 120*0.9 + 120*0.95 = 222 against Alpha's and Gamma's 120.
	if row.Name != "steady" || row.Encounter != 3009 || row.Amount != 90000 {
		t.Errorf("pick = %+v", row)
	}
	if w.boards != 2 || w.ranks != 1 {
		t.Errorf("boards %d ranks %d; want 2 pages read and 1 own-ranking read", w.boards, w.ranks)
	}
	if _, err := a.pickTop(context.Background(), []int{3009, 3010}, 3009, 5, "Death Knight", "Blood", "dps"); err != nil || w.boards != 2 {
		t.Errorf("second pick read %d pages, want the cache to answer", w.boards)
	}

	// A boss whose page cannot be read is skipped; the main boss's cannot be.
	w.boardOf[3011] = nil
	w2 := healthyWCL()
	w2.topErr = wcl.ErrNoRank
	a2, _ := seeded(t, fights.NewMemStore(), &memAudit{}, w2)
	if _, err := a2.pickTop(context.Background(), []int{3009}, 3009, 5, "Death Knight", "Blood", "dps"); err == nil {
		t.Error("an unreadable main boss was accepted")
	}
}
