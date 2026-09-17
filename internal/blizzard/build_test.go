package blizzard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestCharacterLoadouts reads every saved loadout with each talent's
// tooltip where there is one (the captured shape), tells the active loadout
// of the active spec from the rest, and keeps a bare node's id as its name.
func TestCharacterLoadouts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixture(t, "character-loadouts.json"))
	}))
	defer srv.Close()

	los, err := newTestClient(srv).CharacterLoadouts(context.Background(), "tok", CharacterRef{Name: "Nekromoo", RealmSlug: "area-52"})
	if err != nil {
		t.Fatal(err)
	}
	if len(los) != 2 || los[0].Spec != "Blood" || los[0].SpecID != 250 || !los[0].Active || los[1].Active || los[1].ActiveInSpec {
		t.Fatalf("loadouts = %+v", los)
	}
	lo := los[0]
	if lo.HeroTree != "San'layn" || !strings.HasPrefix(lo.Code, "CoPA") || len(lo.Class) != 6 || len(lo.SpecTalents) != 6 || len(lo.Hero) != 3 {
		t.Errorf("loadout = hero %q code %q class %d spec %d hero %d", lo.HeroTree, lo.Code, len(lo.Class), len(lo.SpecTalents), len(lo.Hero))
	}
	// The first node has no tooltip yet: id and rank only.
	if lo.Class[0].ID != 99822 || lo.Class[0].Name != "99822" || lo.Class[0].Description != "" {
		t.Errorf("bare node = %+v", lo.Class[0])
	}
	var icebound *TalentChoice
	for i := range lo.Class {
		if lo.Class[i].Name == "Icebound Fortitude" {
			icebound = &lo.Class[i]
		}
	}
	if icebound == nil || icebound.Cooldown != "2 min cooldown" || icebound.CastTime != "Instant" || icebound.SpellID != 48792 || !strings.Contains(icebound.Description, "blood freezes") {
		t.Errorf("Icebound Fortitude = %+v", icebound)
	}

	// The active loadout, through the older read.
	active, err := newTestClient(srv).CharacterSpecializations(context.Background(), "tok", CharacterRef{Name: "Nekromoo", RealmSlug: "area-52"})
	if err != nil || active.HeroTree != "San'layn" {
		t.Errorf("CharacterSpecializations = %+v, %v", active, err)
	}
}

// TestTalentTree finds the spec's tree through the index -- by the class
// tree's id, since "Frost" alone is a Death Knight's and a Mage's -- reads
// its nodes with every choice and tooltip, and serves the second ask from
// the cache.
func TestTalentTree(t *testing.T) {
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write(fixture(t, "app-token.json"))
		case "/data/wow/talent-tree/index":
			_, _ = w.Write(fixture(t, "talent-tree-index.json"))
		case "/data/wow/talent-tree/750/playable-specialization/250":
			fetches.Add(1)
			if r.URL.Query().Get("namespace") != "static-us" {
				t.Errorf("namespace = %q", r.URL.Query().Get("namespace"))
			}
			_, _ = w.Write(fixture(t, "talent-tree.json"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(srv)
	c.ClientID, c.ClientSecret = "id", "secret"

	tree, err := c.TalentTree(context.Background(), "Death Knight", "Blood")
	if err != nil {
		t.Fatal(err)
	}
	if tree.Class != "Death Knight" || tree.Spec != "Blood" || len(tree.Nodes) != 19 || strings.Join(tree.HeroTrees, ",") != "San'layn,Rider of the Apocalypse,Deathbringer" {
		t.Errorf("tree = %d nodes, heroes %v", len(tree.Nodes), tree.HeroTrees)
	}
	byName := map[string]TalentNode{}
	for _, n := range tree.Nodes {
		for _, e := range n.Entries {
			byName[e.Name] = n
		}
	}
	if n := byName["Death's Reach"]; n.Type != "CHOICE" || len(n.Entries) != 2 || n.Tree != "class" || byName["Asphyxiate"].ID != n.ID {
		t.Errorf("choice node = %+v", n)
	}
	if n := byName["Gloom Ward"]; n.MaxRank != 2 || len(n.Entries) != 1 {
		t.Errorf("two-rank node = %+v", n)
	}
	if n := byName["Icebound Fortitude"]; n.Type != "ACTIVE" || n.Entries[0].Cooldown != "2 min cooldown" || n.Entries[0].SpellID != 48792 {
		t.Errorf("Icebound Fortitude = %+v", n)
	}
	if n := byName["Infliction of Sorrow"]; n.Tree != "San'layn" || n.Type != "PASSIVE" {
		t.Errorf("hero node = %+v", n)
	}

	if _, err := c.TalentTree(context.Background(), "Death Knight", "Blood"); err != nil || fetches.Load() != 1 {
		t.Errorf("second ask: err %v, fetches %d (want the cache)", err, fetches.Load())
	}
	if _, err := c.TalentTree(context.Background(), "Mage", "Frost"); err == nil {
		t.Error("a tree the index has no page for was accepted")
	}
}
