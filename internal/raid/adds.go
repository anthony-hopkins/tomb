package raid

import (
	"math"
	"sort"

	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// Add management (spec 007, amendment 2): every add that appeared in the
// pull, one instance at a time -- when it appeared, who hit it first,
// which tank picked it up and how late, when it died or how much health
// it had left when the pull ended, who put damage into it, and where it
// walked against the raid. Read from the hits into the adds, which carry
// the add's position and health on every hit.

// AddInstance is one add in one pull.
type AddInstance struct {
	Name     string `json:"name"`
	Instance int    `json:"instance"`
	// AppearedAt is the first hit on it, seconds into the pull: the
	// closest the log comes to a spawn.
	AppearedAt float64 `json:"appeared_at_seconds"`
	FirstHitBy string  `json:"first_hit_by"`
	// PickedUpBy is the first tank to hit it and PickupDelay how long
	// after it appeared; NeverTanked when no tank ever hit it.
	PickedUpBy  string  `json:"picked_up_by,omitempty"`
	PickupDelay float64 `json:"pickup_delay_seconds,omitempty"`
	NeverTanked bool    `json:"never_tanked"`
	// DiedAt is when it died; nil when it outlived the pull, with
	// HealthLeftPct saying how much it had.
	DiedAt        *float64 `json:"died_at_seconds,omitempty"`
	Lifetime      float64  `json:"lifetime_seconds"`
	HealthLeftPct float64  `json:"health_left_pct,omitempty"`
	Damage        int64    `json:"damage_into"`
	// Sources is who hit it, most first; Untouched is how many damage
	// dealers never hit it at all.
	Sources   []wcl.SourceTotal `json:"sources"`
	Untouched []string          `json:"dps_who_never_hit_it,omitempty"`
	// Path, where the log carried positions: how far it walked, and how
	// far from the raid's centre it was when it appeared and when it died
	// or the pull ended.
	Path *AddPath `json:"path,omitempty"`
}

// AddPath is where an add went.
type AddPath struct {
	Travelled     float64 `json:"yards_travelled"`
	StartFromRaid float64 `json:"yards_from_raid_at_appearance"`
	EndFromRaid   float64 `json:"yards_from_raid_at_end"`
	ClosestToRaid float64 `json:"closest_yards_to_raid"`
}

// AddSummary is one kind of add across its instances.
type AddSummary struct {
	Name            string  `json:"name"`
	Instances       int     `json:"instances"`
	Killed          int     `json:"killed"`
	MeanLifetime    float64 `json:"mean_lifetime_seconds"`
	MeanPickupDelay float64 `json:"mean_pickup_delay_seconds"`
	NeverTanked     int     `json:"never_tanked"`
	MeanDamage      int64   `json:"mean_damage_into"`
}

// DetailAdds fills the side's add instances and summaries from the hits
// into the adds, the raid's own hits (for where the raid stood), the
// enemy deaths and the actors.
func (s *Side) DetailAdds(fr wcl.FightReading, addHits []wcl.AddHit, raidHits []wcl.Hit, actorName map[int]string) {
	if len(addHits) == 0 {
		return
	}
	role := map[string]string{}
	for _, p := range fr.Players {
		role[p.Name] = p.Role
	}
	type key struct{ id, inst int }
	byKey := map[key][]wcl.AddHit{}
	var order []key
	for _, h := range addHits {
		k := key{h.TargetID, h.Instance}
		if _, seen := byKey[k]; !seen {
			order = append(order, k)
		}
		byKey[k] = append(byKey[k], h)
	}
	deathAt := map[key]float64{}
	for _, d := range fr.EnemyDeaths {
		deathAt[key{d.ActorID, d.Instance}] = math.Round(float64(d.TimestampMS-s.startMS)/100) / 10
	}
	// The raid's positions over time, for the path.
	var raidPos []wcl.Hit
	for _, h := range raidHits {
		if h.HasPos {
			raidPos = append(raidPos, h)
		}
	}
	sort.Slice(raidPos, func(i, j int) bool { return raidPos[i].TimestampMS < raidPos[j].TimestampMS })
	centreAt := func(t int64) (xy, bool) {
		last := map[int]xy{}
		for _, h := range raidPos {
			if h.TimestampMS > t {
				break
			}
			if t-h.TimestampMS <= 10000 {
				last[h.ActorID] = xy{h.X, h.Y}
			}
		}
		if len(last) == 0 {
			return xy{}, false
		}
		var c xy
		for _, p := range last {
			c.x += p.x
			c.y += p.y
		}
		c.x /= float64(len(last))
		c.y /= float64(len(last))
		return c, true
	}
	dist := func(a, b xy) float64 { return math.Hypot(a.x-b.x, a.y-b.y) / yard }

	for _, k := range order {
		hits := byKey[k]
		sort.Slice(hits, func(i, j int) bool { return hits[i].TimestampMS < hits[j].TimestampMS })
		name := actorName[k.id]
		if name == "" {
			continue
		}
		inst := AddInstance{Name: name, Instance: k.inst, AppearedAt: sec(hits[0].TimestampMS - s.startMS), FirstHitBy: actorName[hits[0].SourceID]}
		bySource := map[string]int64{}
		touched := map[string]bool{}
		var lastHP, lastMax int64
		var lastAt int64
		for _, h := range hits {
			src := actorName[h.SourceID]
			bySource[src] += h.Amount
			touched[src] = true
			inst.Damage += h.Amount
			if role[src] == "tank" && inst.PickedUpBy == "" {
				inst.PickedUpBy = src
				inst.PickupDelay = round1(sec(h.TimestampMS-s.startMS) - inst.AppearedAt)
			}
			if h.MaxHitPoints > 0 {
				lastHP, lastMax = h.HitPoints, h.MaxHitPoints
			}
			lastAt = h.TimestampMS
		}
		inst.NeverTanked = inst.PickedUpBy == ""
		if d, ok := deathAt[k]; ok {
			inst.DiedAt = &d
			inst.Lifetime = round1(d - inst.AppearedAt)
		} else {
			inst.Lifetime = round1(sec(lastAt-s.startMS) - inst.AppearedAt)
			if lastMax > 0 {
				inst.HealthLeftPct = math.Round(float64(lastHP)/float64(lastMax)*1000) / 10
			}
		}
		for src, total := range bySource {
			inst.Sources = append(inst.Sources, wcl.SourceTotal{Name: src, Total: total})
		}
		sort.Slice(inst.Sources, func(i, j int) bool { return inst.Sources[i].Total > inst.Sources[j].Total })
		if len(inst.Sources) > 6 {
			inst.Sources = inst.Sources[:6]
		}
		for _, p := range fr.Players {
			if p.Role == "dps" && !touched[p.Name] {
				inst.Untouched = append(inst.Untouched, p.Name)
			}
		}
		// The path, sampled every two seconds of the add's life.
		var path []xy
		var pathAt []int64
		var lastSample int64 = -1
		for _, h := range hits {
			if !h.HasPos || (lastSample >= 0 && h.TimestampMS-lastSample < 2000) {
				continue
			}
			path = append(path, xy{h.X, h.Y})
			pathAt = append(pathAt, h.TimestampMS)
			lastSample = h.TimestampMS
		}
		if len(path) > 0 {
			ap := &AddPath{ClosestToRaid: math.Inf(1)}
			for i := 1; i < len(path); i++ {
				ap.Travelled += dist(path[i-1], path[i])
			}
			ap.Travelled = round1(ap.Travelled)
			for i, p := range path {
				c, ok := centreAt(pathAt[i])
				if !ok {
					continue
				}
				d := dist(p, c)
				if i == 0 {
					ap.StartFromRaid = round1(d)
				}
				ap.EndFromRaid = round1(d)
				if d < ap.ClosestToRaid {
					ap.ClosestToRaid = round1(d)
				}
			}
			if math.IsInf(ap.ClosestToRaid, 1) {
				ap.ClosestToRaid = 0
			}
			inst.Path = ap
		}
		s.AddDetail = append(s.AddDetail, inst)
	}
	sort.Slice(s.AddDetail, func(i, j int) bool { return s.AddDetail[i].AppearedAt < s.AddDetail[j].AppearedAt })

	// Summaries by name.
	sums := map[string]*AddSummary{}
	var names []string
	for _, a := range s.AddDetail {
		sm := sums[a.Name]
		if sm == nil {
			sm = &AddSummary{Name: a.Name}
			sums[a.Name] = sm
			names = append(names, a.Name)
		}
		sm.Instances++
		if a.DiedAt != nil {
			sm.Killed++
		}
		sm.MeanLifetime += a.Lifetime
		if a.NeverTanked {
			sm.NeverTanked++
		} else {
			sm.MeanPickupDelay += a.PickupDelay
		}
		sm.MeanDamage += a.Damage
	}
	for _, n := range names {
		sm := sums[n]
		sm.MeanLifetime = round1(sm.MeanLifetime / float64(sm.Instances))
		if tanked := sm.Instances - sm.NeverTanked; tanked > 0 {
			sm.MeanPickupDelay = round1(sm.MeanPickupDelay / float64(tanked))
		}
		sm.MeanDamage /= int64(sm.Instances)
		s.AddSummaries = append(s.AddSummaries, *sm)
	}
}

func sec(ms int64) float64 { return math.Round(float64(ms)/100) / 10 }

// addSentences words what the add instances say against the top kill.
func addSentences(ours, theirs Side) []string {
	var out []string
	theirSum := map[string]AddSummary{}
	for _, a := range theirs.AddSummaries {
		theirSum[a.Name] = a
	}
	for _, a := range ours.AddSummaries {
		t, ok := theirSum[a.Name]
		if a.NeverTanked > 0 {
			out = append(out, sentence("%d of %d %s were never hit by a tank.", a.NeverTanked, a.Instances, a.Name))
		}
		if ok && t.MeanLifetime > 0 && a.MeanLifetime > t.MeanLifetime*1.25 {
			out = append(out, sentence("%s lived %.0f s on average against %.0f s in the top kill.", a.Name, a.MeanLifetime, t.MeanLifetime))
		}
		if ok && a.MeanPickupDelay > t.MeanPickupDelay+2 {
			out = append(out, sentence("%s was picked up %.1f s after appearing on average, against %.1f s in the top kill.", a.Name, a.MeanPickupDelay, t.MeanPickupDelay))
		}
		if a.Killed < a.Instances {
			out = append(out, sentence("%d of %d %s outlived the pull.", a.Instances-a.Killed, a.Instances, a.Name))
		}
	}
	return out
}
