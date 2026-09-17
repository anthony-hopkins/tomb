//go:build live

package blizzard

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// TestLiveBuild is the seventh amendment's live check: with the site's own
// credentials, read a character's loadouts and the spec's tree from the
// real API and print what the build sheet would be made of. It asserts
// nothing about values; the point is to see them.
//
//	BNET_CLIENT_ID=… BNET_CLIENT_SECRET=… BNET_LIVE_NAME=Nekromoo BNET_LIVE_REALM=area-52 go test -tags live -run TestLiveBuild -v ./internal/blizzard
func TestLiveBuild(t *testing.T) {
	id, secret := os.Getenv("BNET_CLIENT_ID"), os.Getenv("BNET_CLIENT_SECRET")
	if id == "" || secret == "" {
		t.Skip("BNET_CLIENT_ID and BNET_CLIENT_SECRET are not set")
	}
	c := NewHTTPClient("https://us.api.blizzard.com", "profile-us", "us")
	c.ClientID, c.ClientSecret = id, secret
	ctx := context.Background()
	token, err := c.AppToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name, realm := os.Getenv("BNET_LIVE_NAME"), os.Getenv("BNET_LIVE_REALM")
	if name == "" {
		name, realm = "Nekromoo", "area-52"
	}
	los, err := c.CharacterLoadouts(ctx, token, CharacterRef{Name: name, RealmSlug: realm})
	if err != nil {
		t.Fatal(err)
	}
	for _, lo := range los {
		bare, withTip := 0, 0
		for _, g := range [][]TalentChoice{lo.Class, lo.SpecTalents, lo.Hero} {
			for _, ch := range g {
				if ch.Description == "" {
					bare++
				} else {
					withTip++
				}
			}
		}
		fmt.Printf("loadout %-8s active=%-5v inSpec=%-5v hero=%-22q code=%s… class=%d spec=%d hero=%d tooltips=%d bare=%d\n",
			lo.Spec, lo.Active, lo.ActiveInSpec, lo.HeroTree, lo.Code[:12], len(lo.Class), len(lo.SpecTalents), len(lo.Hero), withTip, bare)
	}
	class, spec := os.Getenv("BNET_LIVE_CLASS"), os.Getenv("BNET_LIVE_SPEC")
	if class == "" {
		class, spec = "Death Knight", "Blood"
	}
	tree, err := c.TalentTree(ctx, class, spec)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]int{}
	trees := map[string]int{}
	cds := 0
	for _, n := range tree.Nodes {
		types[n.Type]++
		trees[n.Tree]++
		for _, e := range n.Entries {
			if e.Cooldown != "" {
				cds++
			}
		}
	}
	fmt.Printf("tree %s/%s: %d nodes, types %v, trees %v, entries with a cooldown %d, hero trees %v\n", tree.Class, tree.Spec, len(tree.Nodes), types, trees, cds, tree.HeroTrees)
	// How many of the loadout's nodes the tree knows: the join the sheet relies on.
	known := map[int]bool{}
	for _, n := range tree.Nodes {
		known[n.ID] = true
	}
	for _, lo := range los {
		if !lo.ActiveInSpec || lo.Spec != spec {
			continue
		}
		hit, miss := 0, 0
		for _, g := range [][]TalentChoice{lo.Class, lo.SpecTalents, lo.Hero} {
			for _, ch := range g {
				if known[ch.ID] {
					hit++
				} else {
					miss++
				}
			}
		}
		fmt.Printf("join for %s loadout: %d nodes known to the tree, %d not\n", lo.Spec, hit, miss)
	}
}
