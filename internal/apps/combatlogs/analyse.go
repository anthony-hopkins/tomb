package combatlogs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
	"github.com/anthony-hopkins/tomb/internal/wcl"
)

// comparisonTTL is how long a fetched player is reused (FR-035).
const comparisonTTL = 24 * time.Hour

// Message codes the card renders; never the text itself, which the card
// owns (contracts/http-routes.md).
const (
	msgNoLogs      = "nologs"
	msgNoPulls     = "nopulls"
	msgNoSpec      = "nospec"
	msgNoRank      = "norank"
	msgUnavailable = "unavailable"
	msgWait        = "wait"
)

// analyse is POST /analyses. The member names a character and a source:
// "wcl" -- the character's latest ranked kills on Warcraft Logs, the
// default, needing no upload -- or "upload:<id>", one of their own nights.
// The site finds the top player of that class and spec on the main boss
// now, so a refusal can be given before anything is created, and leaves
// the rest to the worker.
func (a *App) analyse(w http.ResponseWriter, r *http.Request) {
	if a.deps.CSRF == nil || !a.deps.CSRF.Verify(r) {
		http.Error(w, "The form could not be verified. Go back and try again.", http.StatusForbidden)
		return
	}
	uid, _ := a.who(r)
	profile, _ := platform.ProfileFrom(r.Context())

	realm, name, ok := strings.Cut(strings.TrimSpace(r.PostFormValue("character")), "/")
	if !ok || realm == "" || name == "" {
		http.NotFound(w, r)
		return
	}
	// The character must be one on the member's account; that is also
	// where its display case comes from.
	var ref *fights.CharacterRef
	for _, c := range profile.Characters {
		if strings.EqualFold(c.Name, name) && c.RealmSlug == realm {
			ref = &fights.CharacterRef{Name: c.Name, RealmSlug: c.RealmSlug}
		}
	}
	if ref == nil {
		http.NotFound(w, r)
		return
	}
	back := func(code string, extra url.Values) {
		q := url.Values{"c": {ref.RealmSlug + "/" + strings.ToLower(ref.Name)}}
		if code != "" {
			q.Set("msg", code)
		}
		for k, v := range extra {
			q[k] = v
		}
		http.Redirect(w, r, "/app/dashboard?"+q.Encode(), http.StatusSeeOther)
	}
	if a.deps.WCL == nil {
		back(msgUnavailable, nil)
		return
	}

	source := strings.TrimSpace(r.PostFormValue("source"))
	an := fights.Analysis{UserID: uid, Name: ref.Name, RealmSlug: ref.RealmSlug}
	var mainEncounter, diff int
	var class, spec, metric string

	switch {
	case source == "" || source == fights.SourceWCL:
		// The character's own standing on Warcraft Logs -- or, with none,
		// a showcase of the top parses of their class and spec instead.
		an.Source = fights.SourceWCL
		zone, err := a.deps.WCL.ZoneRankings(r.Context(), a.selfRef(*ref))
		switch {
		case errors.Is(err, wcl.ErrNoCharacter), errors.Is(err, wcl.ErrNoLogs):
			an.Source = fights.SourceShowcase
			class, spec = a.blizzardSpec(profile, *ref)
			if class == "" || spec == "" {
				back(msgNoSpec, nil)
				return
			}
			metric = wcl.MetricForSpec(spec)
			// Either failure below is told as "no logs, and no showcase
			// just now", which also names the upload alternative.
			raid, err := a.deps.WCL.CurrentZone(r.Context())
			if err != nil || len(raid.Encounters) == 0 {
				a.deps.Logger.Warn("current zone", "error", err)
				back(msgNoLogs, nil)
				return
			}
			// The first boss of the raid, at the highest difficulty anyone
			// of the class and spec is ranked at.
			mainEncounter = raid.Encounters[0].ID
			diff = 0
			for _, d := range []int{5, 4} {
				if _, err := a.topPlayer(r.Context(), mainEncounter, d, class, spec, metric); err == nil {
					diff = d
					break
				}
			}
			if diff == 0 {
				back(msgNoLogs, nil)
				return
			}
		case err != nil:
			a.deps.Logger.Warn("zone rankings", "character", ref.Name, "error", err)
			back(msgUnavailable, nil)
			return
		default:
			main := zone.Encounters[0]
			for _, e := range zone.Encounters {
				if e.Kills > main.Kills {
					main = e
				}
			}
			mainEncounter, diff, class, spec, metric = main.ID, zone.Difficulty, zone.Class, zone.Spec, zone.Metric
			if class == "" || spec == "" {
				back(msgNoSpec, nil)
				return
			}
		}

	case strings.HasPrefix(source, "upload:"):
		uploadID, err := strconv.ParseInt(strings.TrimPrefix(source, "upload:"), 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		u, err := a.store.Upload(r.Context(), uploadID, uid)
		if err != nil || u.State != fights.Parsed {
			http.NotFound(w, r)
			return
		}
		an.Source, an.UploadID = fights.SourceUpload, u.ID
		fs, err := a.store.FightsForUpload(r.Context(), u.ID)
		if err != nil {
			a.deps.Logger.Error("fights for upload", "upload", u.ID, "error", err)
			back(msgUnavailable, nil)
			return
		}
		sums := summariesOf(fs, ref.Name, ref.RealmSlug)
		n, ok := nightOf(sums, false)
		if !ok {
			back(msgNoPulls, nil)
			return
		}
		class, spec = wcl.SpecName(n.SpecID)
		if class == "" {
			back(msgNoSpec, nil)
			return
		}
		metric = wcl.MetricFor(n.SpecID)
		n, _ = nightOf(sums, metric == "hps")
		diff, err = wcl.Difficulty(n.Difficulty)
		if err != nil {
			back(msgNoPulls, nil)
			return
		}
		mainEncounter = n.Bosses[0].EncounterID

	default:
		http.NotFound(w, r)
		return
	}

	top, err := a.topPlayer(r.Context(), mainEncounter, diff, class, spec, metric)
	switch {
	case errors.Is(err, wcl.ErrNoRank):
		back(msgNoRank, nil)
		return
	case err != nil:
		a.deps.Logger.Warn("find top player", "error", err)
		back(msgUnavailable, nil)
		return
	}
	an.ComparisonID = top.ID

	_, err = a.store.CreateAnalysis(r.Context(), an, unlimited(profile))
	var held fights.ErrAllowance
	switch {
	case errors.As(err, &held):
		back(msgWait, url.Values{"min": {strconv.Itoa(held.Minutes())}})
		return
	case err != nil:
		a.deps.Logger.Error("create analysis", "error", err)
		back(msgUnavailable, nil)
		return
	}
	back("", nil)
}

// blizzardSpec is the character's class and active specialization as
// Blizzard has them, for a character Warcraft Logs has never seen.
func (a *App) blizzardSpec(p platform.Profile, ref fights.CharacterRef) (class, spec string) {
	for _, c := range p.Characters {
		if strings.EqualFold(c.Name, ref.Name) && c.RealmSlug == ref.RealmSlug {
			return c.Class, c.ActiveSpec
		}
	}
	return "", ""
}

// selfRef is the member's character as Warcraft Logs names it: the
// configured region and Blizzard's realm slug, which Warcraft Logs shares.
func (a *App) selfRef(c fights.CharacterRef) wcl.CharacterRef {
	return wcl.CharacterRef{Region: strings.ToLower(a.deps.Config.BnetRegion), Slug: c.RealmSlug, Name: c.Name}
}

// summariesOf picks one character's pulls out of an upload's fights.
func summariesOf(fs []fights.Fight, name, realm string) []fights.Summary {
	var out []fights.Summary
	for i := range fs {
		f := fs[i]
		for _, sm := range f.Summaries {
			if strings.EqualFold(sm.Name, name) && sm.RealmSlug == realm {
				fc := f
				fc.Summaries = nil
				sm.Fight = &fc
				out = append(out, sm)
			}
		}
	}
	return out
}

// topPlayer is the leaderboard's first player of a class and spec on a
// boss, as a comparison row. The leaderboard itself is cached a day, keyed
// by class and spec rather than by player, so a busy night asks Warcraft
// Logs once per boss.
func (a *App) topPlayer(ctx context.Context, encounterID, diff int, class, spec, metric string) (fights.ComparisonPlayer, error) {
	key := topKey(class, spec, encounterID, diff, metric)
	if have, err := a.store.ComparisonPlayer(ctx, key); err == nil && a.now().Sub(have.FetchedAt) < comparisonTTL {
		return have, nil
	}
	ref, ranking, err := a.deps.WCL.TopPlayer(ctx, encounterID, diff, wcl.ClassSlug(class), spec, metric)
	if err != nil {
		return fights.ComparisonPlayer{}, err
	}
	if ranking.Class == "" {
		ranking.Class = class
	}
	// Two rows: the leaderboard's answer under the class-and-spec key, and
	// the player's parse under their own, so a later run on that player
	// finds it too.
	_, _ = a.putComparison(ctx, ref, encounterID, diff, metric, ranking)
	payload, err := json.Marshal(ranking)
	if err != nil {
		return fights.ComparisonPlayer{}, err
	}
	return a.store.PutComparisonPlayer(ctx, fights.ComparisonPlayer{
		Region: key.Region, RealmSlug: key.RealmSlug, Name: key.Name, Encounter: encounterID, WCLDiff: diff, Metric: metric,
		FetchedAt: a.now(), ClassID: ranking.ClassID, Class: ranking.Class, Spec: ranking.Spec, RankPercent: ranking.RankPercent, Amount: ranking.Amount,
		Payload: payload,
	})
}

// topKey is the cache key of a leaderboard answer: region "top", the class
// slug where a realm would be, and the spec where a name would be.
func topKey(class, spec string, encounterID, diff int, metric string) fights.ComparisonKey {
	return fights.ComparisonKey{Region: "top", RealmSlug: wcl.ClassSlug(class), Name: strings.ToLower(spec), Encounter: encounterID, WCLDiff: diff, Metric: metric}
}

// comparison is a named player's best parse on a boss: from the day's
// cache, or fetched and cached.
func (a *App) comparison(ctx context.Context, ref wcl.CharacterRef, encounterID, diff int, metric string) (fights.ComparisonPlayer, error) {
	key := comparisonKey(ref, encounterID, diff, metric)
	if have, err := a.store.ComparisonPlayer(ctx, key); err == nil && a.now().Sub(have.FetchedAt) < comparisonTTL {
		return have, nil
	}
	if a.deps.WCL == nil {
		return fights.ComparisonPlayer{}, errors.New("no warcraft logs client")
	}
	ranking, err := a.deps.WCL.BestRank(ctx, ref, encounterID, diff, metric)
	if err != nil {
		return fights.ComparisonPlayer{}, err
	}
	return a.putComparison(ctx, ref, encounterID, diff, metric, ranking)
}

func comparisonKey(ref wcl.CharacterRef, encounterID, diff int, metric string) fights.ComparisonKey {
	key := fights.ComparisonKey{Region: ref.Region, RealmSlug: ref.Slug, Name: strings.ToLower(ref.Name), Encounter: encounterID, WCLDiff: diff, Metric: metric}
	if ref.ID != 0 {
		key.Region, key.RealmSlug, key.Name = "id", "", strconv.FormatInt(ref.ID, 10)
	}
	return key
}

func (a *App) putComparison(ctx context.Context, ref wcl.CharacterRef, encounterID, diff int, metric string, ranking wcl.Ranking) (fights.ComparisonPlayer, error) {
	key := comparisonKey(ref, encounterID, diff, metric)
	payload, err := json.Marshal(ranking)
	if err != nil {
		return fights.ComparisonPlayer{}, err
	}
	return a.store.PutComparisonPlayer(ctx, fights.ComparisonPlayer{
		Region: key.Region, RealmSlug: key.RealmSlug, Name: key.Name, Encounter: encounterID, WCLDiff: diff, Metric: metric,
		FetchedAt: a.now(), ClassID: ranking.ClassID, Class: ranking.Class, Spec: ranking.Spec, RankPercent: ranking.RankPercent, Amount: ranking.Amount,
		Payload: payload,
	})
}
