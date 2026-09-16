package fights

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// The log and Warcraft Logs give talents and items as numbers; the card and
// the model need names. These resolve through the store's cache first and
// Blizzard's Game Data for what is missing, a bounded few at a time, and an
// id nobody can name keeps its number so nothing is ever blank.

// lookupParallel bounds the Game Data fan-out, as the profile fetch does.
const lookupParallel = 8

// ResolveTalents names talent entry ids.
func ResolveTalents(ctx context.Context, store Store, gd blizzard.GameData, ids []int) map[int]string {
	return resolve(ctx, ids, store.TalentNames, store.PutTalentNames, func(ctx context.Context, id int) (string, error) {
		if gd == nil {
			return "", errors.New("no game data")
		}
		t, err := gd.Talent(ctx, id)
		return t.Name, err
	})
}

// ResolveItems names item ids.
func ResolveItems(ctx context.Context, store Store, gd blizzard.GameData, ids []int) map[int]string {
	return resolve(ctx, ids, store.ItemNames, store.PutItemNames, func(ctx context.Context, id int) (string, error) {
		if gd == nil {
			return "", errors.New("no game data")
		}
		it, err := gd.Item(ctx, id)
		return it.Name, err
	})
}

func resolve(
	ctx context.Context, ids []int,
	cached func(context.Context, []int) (map[int]string, []int, error),
	put func(context.Context, map[int]string) error,
	fetch func(context.Context, int) (string, error),
) map[int]string {
	out := map[int]string{}
	unique := dedupe(ids)
	if len(unique) == 0 {
		return out
	}
	found, missing, err := cached(ctx, unique)
	if err != nil {
		missing = unique
	}
	for id, name := range found {
		out[id] = name
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	fetched := map[int]string{}
	sem := make(chan struct{}, lookupParallel)
	for _, id := range missing {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			name, err := fetch(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil || name == "" {
				out[id] = strconv.Itoa(id) // keep the number; nothing is blank
				return
			}
			out[id] = name
			fetched[id] = name
		}(id)
	}
	wg.Wait()
	if len(fetched) > 0 {
		_ = put(ctx, fetched) // a cache miss next time is the only cost
	}
	return out
}

func dedupe(ids []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, id := range ids {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
