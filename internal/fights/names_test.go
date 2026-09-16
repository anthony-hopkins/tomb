package fights

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

type fakeGameData struct {
	talents map[int]string
	items   map[int]string
	calls   atomic.Int32
}

func (f *fakeGameData) Talent(_ context.Context, id int) (blizzard.TalentInfo, error) {
	f.calls.Add(1)
	if n, ok := f.talents[id]; ok {
		return blizzard.TalentInfo{ID: id, Name: n}, nil
	}
	return blizzard.TalentInfo{}, errors.New("404")
}

func (f *fakeGameData) Item(_ context.Context, id int) (blizzard.ItemInfo, error) {
	f.calls.Add(1)
	if n, ok := f.items[id]; ok {
		return blizzard.ItemInfo{ID: id, Name: n}, nil
	}
	return blizzard.ItemInfo{}, errors.New("404")
}

// TestResolve: the cache first, Game Data for the rest, the number for what
// nobody can name, and what was fetched is cached for next time.
func TestResolve(t *testing.T) {
	store := NewMemStore()
	store.Talents[1] = "Cached Talent"
	gd := &fakeGameData{talents: map[int]string{2: "Fetched Talent"}, items: map[int]string{10: "Fetched Item"}}

	got := ResolveTalents(context.Background(), store, gd, []int{1, 2, 3, 2, 0})
	want := map[int]string{1: "Cached Talent", 2: "Fetched Talent", 3: "3"}
	if len(got) != len(want) {
		t.Fatalf("ResolveTalents = %v, want %v", got, want)
	}
	for id, n := range want {
		if got[id] != n {
			t.Errorf("talent %d = %q, want %q", id, got[id], n)
		}
	}
	if gd.calls.Load() != 2 {
		t.Errorf("Game Data called %d times, want 2 (ids 2 and 3)", gd.calls.Load())
	}
	if store.Talents[2] != "Fetched Talent" {
		t.Error("fetched name was not cached")
	}
	if _, cached := store.Talents[3]; cached {
		t.Error("an unknown id was cached")
	}

	gd.calls.Store(0)
	if got := ResolveItems(context.Background(), store, gd, []int{10}); got[10] != "Fetched Item" || gd.calls.Load() != 1 {
		t.Errorf("ResolveItems = %v after %d calls", got, gd.calls.Load())
	}
	if got := ResolveItems(context.Background(), store, nil, []int{11}); got[11] != "11" {
		t.Errorf("without Game Data = %v", got)
	}
}
