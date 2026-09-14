package blizzard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// rosterServer stands in for Blizzard, counting calls and able to start failing
// on command.
func rosterServer(t *testing.T, calls *atomic.Int32, fail *atomic.Bool) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"members":[
			{"character":{"name":"Cwds","level":90,"realm":{"slug":"elune"},
			 "playable_class":{"id":10}},"rank":0}
		]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func rosterClient(t *testing.T, host string, ttl time.Duration) *HTTPClient {
	t.Helper()
	c := NewHTTPClient(host, "profile-us", "us")
	c.RosterTTL = ttl
	return c
}

// TestRosterIsCached is the point of the cache: the guild overview is the page
// every member lands on, and without this each of those loads waits on Blizzard.
func TestRosterIsCached(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	srv := rosterServer(t, &calls, &fail)
	c := rosterClient(t, srv.URL, time.Hour)

	for i := 0; i < 5; i++ {
		members, err := c.GuildRoster(context.Background(), "token", "elune", "TOMB")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if len(members) != 1 || members[0].Name != "Cwds" {
			t.Fatalf("call %d returned %v", i, members)
		}
	}

	if n := calls.Load(); n != 1 {
		t.Errorf("hit Blizzard %d times for 5 page loads, want 1", n)
	}
}

// TestRosterRefetchesWhenStale: cached is not frozen. A guild that gains a
// member must show them without a restart.
func TestRosterRefetchesWhenStale(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	srv := rosterServer(t, &calls, &fail)

	// A TTL already elapsed by the time the second call happens.
	c := rosterClient(t, srv.URL, time.Nanosecond)

	for i := 0; i < 3; i++ {
		if _, err := c.GuildRoster(context.Background(), "token", "elune", "TOMB"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		time.Sleep(time.Millisecond)
	}

	if n := calls.Load(); n != 3 {
		t.Errorf("hit Blizzard %d times with an expired cache, want 3", n)
	}
}

// TestStaleRosterBeatsNoRoster is the failure path worth having.
//
// One bad minute at Blizzard would otherwise empty the front page of the site.
// A roster from an hour ago is a far better answer than "unavailable": almost
// certainly nobody joined or left in the meantime.
func TestStaleRosterBeatsNoRoster(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	srv := rosterServer(t, &calls, &fail)
	c := rosterClient(t, srv.URL, time.Nanosecond)

	// Warm the cache.
	if _, err := c.GuildRoster(context.Background(), "token", "elune", "TOMB"); err != nil {
		t.Fatalf("warming: %v", err)
	}
	time.Sleep(time.Millisecond)

	// Now Blizzard falls over.
	fail.Store(true)

	members, err := c.GuildRoster(context.Background(), "token", "elune", "TOMB")
	if err != nil {
		t.Fatalf("a stale roster should have been served, got %v", err)
	}
	if len(members) != 1 || members[0].Name != "Cwds" {
		t.Errorf("served %v, want the previously cached roster", members)
	}
}

// TestFailureWithNoCacheStillErrors: serving stale data is a fallback, not a
// way of hiding that the roster has never loaded.
func TestFailureWithNoCacheStillErrors(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	srv := rosterServer(t, &calls, &fail)
	c := rosterClient(t, srv.URL, time.Hour)

	if _, err := c.GuildRoster(context.Background(), "token", "elune", "TOMB"); err == nil {
		t.Error("a first fetch that failed reported success")
	}
}

// TestCachedRosterIsACopy: callers sort and group what they are handed, and the
// cached slice outlives the request. Sharing it would let one request reorder
// another one's data.
func TestCachedRosterIsACopy(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	srv := rosterServer(t, &calls, &fail)
	c := rosterClient(t, srv.URL, time.Hour)

	first, err := c.GuildRoster(context.Background(), "token", "elune", "TOMB")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	first[0].Name = "MUTATED"

	second, err := c.GuildRoster(context.Background(), "token", "elune", "TOMB")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second[0].Name != "Cwds" {
		t.Errorf("the cache was mutated by a caller: got %q", second[0].Name)
	}
}
