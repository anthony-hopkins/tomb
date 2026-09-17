//go:build live

package wcl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestLive is T040/T050: the real API, with real credentials, so the
// hand-written fixtures can be checked against what Warcraft Logs actually
// returns. It runs only with -tags live and WCL_CLIENT_ID/WCL_CLIENT_SECRET
// set, and writes the raw answers to the file named by WCL_LIVE_OUT (or
// stdout) for a person to read. It asserts nothing about the shapes: the
// point is to see them.
//
//	WCL_CLIENT_ID=… WCL_CLIENT_SECRET=… WCL_LIVE_OUT=/tmp/wcl.json go test -tags live -run TestLive ./internal/wcl
func TestLive(t *testing.T) {
	id, secret := os.Getenv("WCL_CLIENT_ID"), os.Getenv("WCL_CLIENT_SECRET")
	if id == "" || secret == "" {
		t.Skip("WCL_CLIENT_ID and WCL_CLIENT_SECRET are not set")
	}
	c := New(id, secret)
	ctx := context.Background()
	out := os.Stdout
	if p := os.Getenv("WCL_LIVE_OUT"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		out = f
	}
	dump := func(label string, v any) {
		b, _ := json.MarshalIndent(v, "", "  ")
		s := string(b)
		if len(s) > 60000 {
			s = s[:60000] + "\n…(truncated)"
		}
		fmt.Fprintf(out, "\n===== %s =====\n%s\n", label, s)
	}
	raw := func(label, query string, vars map[string]any) json.RawMessage {
		var payload json.RawMessage
		if err := c.query(ctx, query, vars, &payload); err != nil {
			fmt.Fprintf(out, "\n===== %s: ERROR =====\n%v\n", label, err)
			return nil
		}
		dump(label, payload)
		return payload
	}

	// The zone list, decoded and raw.
	zone, err := c.CurrentZone(ctx)
	fmt.Fprintf(out, "\n===== CurrentZone =====\n%+v\nerr=%v\n", zone, err)
	raw("zones raw (first 6000 bytes)", zonesQuery, map[string]any{})
	if err != nil || len(zone.Encounters) == 0 {
		return
	}
	enc := zone.Encounters[0].ID

	// The leaderboard, raw, one ranking with its gear and talents.
	class, spec := os.Getenv("WCL_LIVE_CLASS"), os.Getenv("WCL_LIVE_SPEC")
	if class == "" {
		class, spec = "Warrior", "Arms"
	}
	raw("characterRankings raw", `query($enc:Int!,$diff:Int!,$class:String!,$spec:String!,$metric:CharacterRankingMetricType!){ worldData { encounter(id:$enc) { name characterRankings(difficulty:$diff, className:$class, specName:$spec, metric:$metric, includeCombatantInfo:true, page:1) } } }`,
		map[string]any{"enc": enc, "diff": 5, "class": class, "spec": spec, "metric": "dps"})
	ref, rk, err := c.TopPlayer(ctx, enc, 5, class, spec, "dps")
	fmt.Fprintf(out, "\n===== TopPlayer decoded =====\nref=%+v\nerr=%v\nname=%s class=%s spec=%s pct=%v amount=%v dur=%v report=%s fight=%d gear=%d talents=%d\n",
		ref, err, rk.Name, rk.Class, rk.Spec, rk.RankPercent, rk.Amount, rk.Duration, rk.ReportCode, rk.FightID, len(rk.Gear), len(rk.Talents))
	if len(rk.Gear) > 0 {
		dump("TopPlayer gear[0..2]", rk.Gear[:min(3, len(rk.Gear))])
	}
	if len(rk.Talents) > 0 {
		dump("TopPlayer talents[0..3]", rk.Talents[:min(4, len(rk.Talents))])
	}

	// A named character: standing, and their best and latest on that boss.
	name, slug, region := os.Getenv("WCL_LIVE_NAME"), os.Getenv("WCL_LIVE_REALM"), os.Getenv("WCL_LIVE_REGION")
	if name == "" {
		name, slug, region = ref.Name, ref.Slug, ref.Region
	}
	if region == "" {
		region = "us"
	}
	cr := CharacterRef{Region: region, Slug: slug, Name: name}
	raw("character zoneRankings raw", `query($name:String!,$slug:String!,$region:String!){ characterData { character(name:$name, serverSlug:$slug, serverRegion:$region) { id name classID zoneRankings } } }`,
		map[string]any{"name": name, "slug": slug, "region": region})
	z, err := c.ZoneRankings(ctx, cr)
	fmt.Fprintf(out, "\n===== ZoneRankings decoded =====\n%+v\nerr=%v\n", z, err)
	// Their own standing decides the difficulty and the boss asked about.
	diff := 5
	if z.Difficulty > 0 {
		diff = z.Difficulty
	}
	if len(z.Encounters) > 0 {
		enc = z.Encounters[0].ID
	}
	raw("character encounterRankings raw", `query($name:String!,$slug:String!,$region:String!,$enc:Int!,$diff:Int!,$metric:CharacterRankingMetricType!){ characterData { character(name:$name, serverSlug:$slug, serverRegion:$region) { id name classID encounterRankings(encounterID:$enc, difficulty:$diff, metric:$metric, includeCombatantInfo:true) } } }`,
		map[string]any{"name": name, "slug": slug, "region": region, "enc": enc, "diff": diff, "metric": "dps"})
	best, err := c.BestRank(ctx, cr, enc, diff, "dps")
	fmt.Fprintf(out, "\n===== BestRank decoded =====\nerr=%v\nname=%s pct=%v amount=%v dur=%v started=%v report=%s fight=%d gear=%d talents=%d\n",
		err, best.Name, best.RankPercent, best.Amount, best.Duration, best.StartedAt, best.ReportCode, best.FightID, len(best.Gear), len(best.Talents))
	latest, err := c.LatestRank(ctx, cr, enc, diff, "dps")
	fmt.Fprintf(out, "\n===== LatestRank decoded =====\nerr=%v\nname=%s pct=%v started=%v\n", err, latest.Name, latest.RankPercent, latest.StartedAt)
	_ = strings.TrimSpace
}
