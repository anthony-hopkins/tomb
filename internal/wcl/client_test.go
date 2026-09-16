package wcl

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// server fakes the token endpoint and the GraphQL endpoint, answering the
// named fixture, and records what was asked.
func server(t *testing.T, answer func(vars map[string]any) (int, []byte)) (*HTTPClient, *atomic.Int32, *[]map[string]any) {
	t.Helper()
	var mints atomic.Int32
	var asked []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			mints.Add(1)
			if u, p, ok := r.BasicAuth(); !ok || u != "id" || p != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"WCLTOKEN","expires_in":86400}`))
		case "/api/v2/client":
			if r.Header.Get("Authorization") != "Bearer WCLTOKEN" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var req struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &req)
			asked = append(asked, req.Variables)
			status, out := answer(req.Variables)
			w.WriteHeader(status)
			_, _ = w.Write(out)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := New("id", "secret")
	c.TokenURL, c.Endpoint, c.HTTP = srv.URL+"/oauth/token", srv.URL+"/api/v2/client", srv.Client()
	return c, &mints, &asked
}

// TestBestRank: the best of several ranks by rankPercent, with gear and
// talents; the variables carry the name, realm, region and encounter.
func TestBestRank(t *testing.T) {
	c, mints, asked := server(t, func(map[string]any) (int, []byte) { return 200, fixture(t, "encounter-rankings.json") })
	got, err := c.BestRank(context.Background(), CharacterRef{Region: "us", Slug: "area-52", Name: "Toptank"}, 3009, 5, "dps")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Toptank" || got.ClassID != 1 || got.Class != "Death Knight" || got.Spec != "Blood" || got.RankPercent != 97.3 || got.Amount != 1498220.1 {
		t.Errorf("ranking = %+v", got)
	}
	if got.ReportCode != "aBcD1234eFgH" || got.FightID != 7 || got.Duration != 312*time.Second || got.Metric != "dps" {
		t.Errorf("report = %q/%d, duration %v", got.ReportCode, got.FightID, got.Duration)
	}
	if len(got.Gear) != 3 || got.Gear[1].Name != "Pendant of Malefic Fury" || got.Gear[1].ItemLevel != 324 || got.Gear[0].ID != 212345 {
		t.Errorf("gear = %+v", got.Gear)
	}
	if len(got.Talents) != 3 || got.Talents[2].Name != "Bonestorm" {
		t.Errorf("talents = %+v", got.Talents)
	}
	vars := (*asked)[0]
	if vars["name"] != "Toptank" || vars["slug"] != "area-52" || vars["region"] != "us" || vars["enc"].(float64) != 3009 || vars["diff"].(float64) != 5 || vars["metric"] != "dps" {
		t.Errorf("variables = %v", vars)
	}
	if _, ok := vars["id"]; ok {
		t.Error("id was sent for a named character")
	}

	// A second call reuses the token.
	if _, err := c.BestRank(context.Background(), CharacterRef{ID: 1234567}, 3009, 5, "dps"); err != nil {
		t.Fatal(err)
	}
	if mints.Load() != 1 {
		t.Errorf("token minted %d times, want 1", mints.Load())
	}
	if vars := (*asked)[1]; vars["id"].(float64) != 1234567 || vars["name"] != nil {
		t.Errorf("by-id variables = %v", vars)
	}
}

// TestBestRankOutcomes: no ranks, no character, a GraphQL error, a 429, and
// a token refusal.
func TestBestRankOutcomes(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"no ranks", 200, "encounter-rankings-empty.json", ErrNoRank},
		{"no character", 200, "character-null.json", ErrNoCharacter},
		{"busy", 429, "", ErrBusy},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := server(t, func(map[string]any) (int, []byte) {
				if tc.body == "" {
					return tc.status, []byte("{}")
				}
				return tc.status, fixture(t, tc.body)
			})
			_, err := c.BestRank(context.Background(), CharacterRef{Region: "us", Slug: "a", Name: "b"}, 1, 5, "dps")
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}

	c, _, _ := server(t, func(map[string]any) (int, []byte) {
		return 200, []byte(`{"errors":[{"message":"Unknown argument"}],"data":null}`)
	})
	if _, err := c.BestRank(context.Background(), CharacterRef{Region: "us", Slug: "a", Name: "b"}, 1, 5, "dps"); err == nil || err.Error() != "warcraft logs: Unknown argument" {
		t.Errorf("graphql error = %v", err)
	}

	c.ClientSecret = "wrong"
	c.token = ""
	if _, err := c.BestRank(context.Background(), CharacterRef{Region: "us", Slug: "a", Name: "b"}, 1, 5, "dps"); err == nil {
		t.Error("a refused token did not error")
	}
}

// TestTokenRefresh: the token is minted again once it nears expiry.
func TestTokenRefresh(t *testing.T) {
	c, mints, _ := server(t, func(map[string]any) (int, []byte) { return 200, fixture(t, "encounter-rankings.json") })
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	ref := CharacterRef{Region: "us", Slug: "a", Name: "b"}
	if _, err := c.BestRank(context.Background(), ref, 1, 5, "dps"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(86400*time.Second - 30*time.Second)
	if _, err := c.BestRank(context.Background(), ref, 1, 5, "dps"); err != nil {
		t.Fatal(err)
	}
	if mints.Load() != 2 {
		t.Errorf("minted %d times, want 2", mints.Load())
	}
}
