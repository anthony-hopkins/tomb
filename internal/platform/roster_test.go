package platform

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// rosterFake answers GuildRoster and counts the calls. It embeds the package's
// fakeClient so the rest of the interface is satisfied without repeating it.
type rosterFake struct {
	*fakeClient
	roster    []blizzard.GuildMember
	rosterErr error
	gets      atomic.Int32
}

func (f *rosterFake) GuildRoster(context.Context, string, string, string) ([]blizzard.GuildMember, error) {
	f.gets.Add(1)
	return f.roster, f.rosterErr
}

func newRosterCache(f *rosterFake, ttl time.Duration) *RosterCache {
	return &RosterCache{
		Client: f,
		Guild:  GuildConfig{Name: "TOMB", RealmSlug: "elune"},
		Logger: discardLogger(),
		TTL:    ttl,
	}
}

var twoOnRoster = []blizzard.GuildMember{
	{Name: "Cwds", RealmSlug: "elune", Rank: 0},
	{Name: "Maintank", RealmSlug: "area-52", Rank: 1},
}

// TestRosterCacheLoadsOnceWithinTTL: the first request pays; the next inside
// the interval pays nothing and gets the same roster.
func TestRosterCacheLoadsOnceWithinTTL(t *testing.T) {
	f := &rosterFake{fakeClient: &fakeClient{}, roster: twoOnRoster}
	rc := newRosterCache(f, time.Hour)

	first, err := rc.Members(context.Background(), "t")
	if err != nil {
		t.Fatalf("first Members: %v", err)
	}
	second, err := rc.Members(context.Background(), "t")
	if err != nil {
		t.Fatalf("second Members: %v", err)
	}
	if f.gets.Load() != 1 {
		t.Errorf("roster fetched %d times, want 1", f.gets.Load())
	}
	if len(first) != 2 || len(second) != 2 {
		t.Errorf("got %d and %d members, want 2 and 2", len(first), len(second))
	}
	// A copy: sorting what a caller was handed must not reorder the cache.
	first[0], first[1] = first[1], first[0]
	again, _ := rc.Members(context.Background(), "t")
	if again[0].Name != "Cwds" {
		t.Error("a caller's reordering leaked into the cache")
	}
}

// TestRosterCacheServesStaleWhileRefreshing: past the interval a request is
// served what is held, at once, and the refresh lands behind it.
func TestRosterCacheServesStaleWhileRefreshing(t *testing.T) {
	f := &rosterFake{fakeClient: &fakeClient{}, roster: twoOnRoster}
	rc := newRosterCache(f, time.Hour)

	if _, err := rc.Members(context.Background(), "t"); err != nil {
		t.Fatalf("initial: %v", err)
	}
	rc.mu.Lock()
	rc.fetched = time.Now().Add(-2 * time.Hour)
	rc.mu.Unlock()

	f.roster = append(twoOnRoster, blizzard.GuildMember{Name: "Newbie", RealmSlug: "elune", Rank: 8})
	got, err := rc.Members(context.Background(), "t")
	if err != nil || len(got) != 2 {
		t.Fatalf("stale request = %d members, %v; want the held 2 at once", len(got), err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for f.gets.Load() < 2 || func() bool { rc.mu.Lock(); defer rc.mu.Unlock(); return len(rc.members) != 3 }() {
		if time.Now().After(deadline) {
			t.Fatal("the background refresh never landed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestRosterCacheKeepsWhatItHasWhenRefreshFails: a bad minute at Blizzard must
// not empty the roster for everyone.
func TestRosterCacheKeepsWhatItHasWhenRefreshFails(t *testing.T) {
	f := &rosterFake{fakeClient: &fakeClient{}, roster: twoOnRoster}
	rc := newRosterCache(f, time.Hour)

	if _, err := rc.Members(context.Background(), "t"); err != nil {
		t.Fatalf("initial: %v", err)
	}
	f.rosterErr = errors.New("down")
	rc.mu.Lock()
	rc.fetched = time.Now().Add(-2 * time.Hour)
	rc.mu.Unlock()

	got, err := rc.Members(context.Background(), "t")
	if err != nil || len(got) != 2 {
		t.Fatalf("stale request during outage = %d members, %v; want the held 2", len(got), err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for func() bool { rc.mu.Lock(); defer rc.mu.Unlock(); return rc.refreshing }() {
		if time.Now().After(deadline) {
			t.Fatal("refresh never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, _ = rc.Members(context.Background(), "t")
	if len(got) != 2 {
		t.Error("a failed refresh emptied the roster")
	}
}

// TestRosterCacheErrorsOnlyWithNothingHeld: cold and down is an error, not an
// empty guild.
func TestRosterCacheErrorsOnlyWithNothingHeld(t *testing.T) {
	f := &rosterFake{fakeClient: &fakeClient{}, rosterErr: errors.New("down")}
	if _, err := newRosterCache(f, time.Hour).Members(context.Background(), "t"); err == nil {
		t.Error("expected an error with nothing held and the roster unreachable")
	}
}

// TestRosterCacheFirstRequestsShareOneLoad: eight cold requests, one fetch.
func TestRosterCacheFirstRequestsShareOneLoad(t *testing.T) {
	f := &rosterFake{fakeClient: &fakeClient{}, roster: twoOnRoster}
	rc := newRosterCache(f, time.Hour)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := rc.Members(context.Background(), "t"); err != nil {
				t.Errorf("Members: %v", err)
			}
		})
	}
	wg.Wait()
	if f.gets.Load() != 1 {
		t.Errorf("eight cold requests fetched %d times, want 1", f.gets.Load())
	}
}
