package recommendations

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func WatchlistKind(kind string) bool {
	return kind == "watchlist" || kind == "monthly-watchlist" || kind == "yearly-watchlist"
}

// Existing weekly selections gain the two horizons without enabling Watchlist
// recommendations in libraries where the user excluded that kind entirely.
func (o Options) WithWatchlistPeriods() Options {
	if !o.WatchlistPeriods || !contains(o.Kinds, "watchlist") {
		return o
	}
	for _, k := range []string{"monthly-watchlist", "yearly-watchlist"} {
		if !contains(o.Kinds, k) {
			o.Kinds = append(append([]string(nil), o.Kinds...), k)
		}
	}
	return o
}

func watchFeatures(s Scene) []string {
	out := features(s)
	ids := []string{}
	for _, p := range s.Performers {
		if p.ID != "" {
			ids = append(ids, p.ID)
		}
	}
	ids = unique(ids)
	sort.Strings(ids)
	// Bound ensemble combinations; never equate an ensemble with separate solo preferences.
	if len(ids) <= 8 {
		for i := range ids {
			for j := i + 1; j < len(ids); j++ {
				out = append(out, "pair:"+ids[i]+":"+ids[j])
			}
		}
	}
	duration := 0.0
	for _, f := range s.Files {
		if f.Duration > duration {
			duration = f.Duration
		}
	}
	if duration > 0 {
		bucket := "short"
		if duration >= 1800 {
			bucket = "medium"
		}
		if duration >= 5400 {
			bucket = "long"
		}
		out = append(out, "duration:"+bucket)
	}
	return unique(out)
}

type watchProfile struct {
	long, window profile
	prevalence   map[string]int
	labels       map[string]string
	total, timed int
}

func learnWatchPreferences(rows []Scene, o Options, now time.Time, days int) watchProfile {
	p := watchProfile{long: profile{}, window: profile{}, prevalence: map[string]int{}, labels: map[string]string{}}
	seen := map[string]bool{}
	cutoff := now.AddDate(0, 0, -days)
	for _, s := range rows {
		if seen[s.ID] || excluded(s, o) {
			continue
		}
		seen[s.ID] = true

		if s.Studio != nil {
			p.labels["studio:"+s.Studio.ID] = s.Studio.Name
		}
		for _, e := range s.Performers {
			p.labels["performer:"+e.ID] = e.Name
		}
		for _, g := range s.Groups {
			p.labels["series:"+g.Group.ID] = g.Group.Name
		}
		fs := watchFeatures(s)
		p.total++
		for _, f := range fs {
			p.prevalence[f]++
		}
		pos, neg, observed := evidence(s, o)
		add := func(dst profile, positive, negative float64) {
			for _, f := range fs {
				if dst[f] == nil {
					dst[f] = &featureStats{}
				}
				x := dst[f]
				x.Positive += positive
				x.Negative += negative
				x.Observed++
				x.Examples++
			}
		}
		if observed {
			add(p.long, pos, neg)
		}
		inWindow := func(raw string) bool { t := eventTime(raw); return !t.IsZero() && !t.Before(cutoff) && !t.After(now) }
		plays, orgasms := map[string]bool{}, map[string]bool{}
		for _, e := range s.PlayHistory {
			if inWindow(e) {
				plays[e] = true
			}
		}
		for _, e := range s.OHistory {
			if inWindow(e) {
				orgasms[e] = true
			}
		}
		if inWindow(s.LastPlayed) && len(plays) == 0 {
			plays[s.LastPlayed] = true
		}
		if len(plays)+len(orgasms) > 0 {
			// Undated ratings and aggregate counts inform the long-term prior only.
			timed := Scene{Plays: len(plays), O: len(orgasms)}
			for e := range plays {
				timed.PlayHistory = append(timed.PlayHistory, e)
			}
			positive, _, _ := evidence(timed, o)
			add(p.window, positive, 0)
			p.timed++
		}
	}
	return p
}

func (p watchProfile) affinity(s Scene, window bool) (float64, []string, int) {
	stats := p.long
	if window {
		stats = p.window
	}
	totals, counts := map[string]float64{}, map[string]int{}
	examples := 0
	for _, f := range watchFeatures(s) {
		x := stats[f]
		if x == nil {
			continue
		}
		kind := strings.SplitN(f, ":", 2)[0]
		if kind == "pair" && x.Examples < 2 || kind == "duration" && x.Examples < 3 {
			continue
		}
		support := float64(x.Examples) / (float64(x.Examples) + 3)
		value := (x.Positive - x.Negative) / (x.Observed + 3) * support
		// Common tags carry little identifying information, especially in JAV.
		if kind == "tag" {
			value *= math.Max(0, 1-float64(p.prevalence[f])/float64(max(1, p.total)))
		}
		totals[kind] += value
		counts[kind]++
		examples += x.Examples
	}
	score := 0.0
	reasons := []string{}
	names := map[string]string{"performer": "cast", "pair": "performer combinations", "studio": "studios/labels", "series": "series", "duration": "duration", "tag": "specific content tags"}
	for _, kind := range []string{"performer", "pair", "studio", "series", "duration", "tag"} {
		if counts[kind] == 0 {
			continue
		}
		value := totals[kind] / float64(counts[kind])
		weight := 1.0
		if kind == "pair" {
			weight = 1.4
		}
		if kind == "duration" {
			weight = .25
		}
		if kind == "tag" {
			weight = .25
		}
		score += value * weight
		if value > 0 {
			reasons = append(reasons, "Positive history for "+names[kind])
		}
	}
	return score, reasons, examples
}

type watchPreferences struct {
	local, global watchProfile
	days          int
	recentWeight  float64
	cross         bool
}

func newWatchPreferences(kind string, local, global []Scene, o Options, now time.Time) watchPreferences {
	days, weight := 7, .7
	if kind == "monthly-watchlist" {
		days, weight = 30, .5
	}
	if kind == "yearly-watchlist" {
		days, weight = 365, .3
	}
	localIDs := map[string]bool{}
	for _, row := range local {
		localIDs[row.ID] = true
	}
	other := []Scene{}
	for _, row := range global {
		if !localIDs[row.ID] {
			other = append(other, row)
		}
	}
	return watchPreferences{learnWatchPreferences(local, o, now, days), learnWatchPreferences(other, o, now, days), days, weight, o.CrossLibrary}
}
func (p watchPreferences) rank(s Scene, favourite bool, rating float64) (float64, []string, int) {
	long, reasons, support := p.local.affinity(s, false)
	recent, _, _ := p.local.affinity(s, true)
	if p.cross {
		g, _, n := p.global.affinity(s, false)
		r, _, _ := p.global.affinity(s, true)
		long += .2 * g
		recent += .2 * r
		support += min(3, n)
	}
	weight := p.recentWeight
	if p.local.timed < 3 {
		weight *= float64(p.local.timed) / 3
	}
	score := (1-weight)*long + weight*recent + 2*rating
	if favourite {
		score += 1.5
	}
	reasons = append(reasons, "In current Stash Watchlist", fmt.Sprintf("%d-day activity window with long-term rating and content preferences", p.days))
	if p.local.timed < 3 {
		reasons = append(reasons, "Sparse dated activity; stronger long-term preference fallback")
	}
	return score, reasons, support
}

// Themes name actual source metadata only, with repeated positive evidence.
func (p watchPreferences) themes() []string {
	stats := p.local.long
	if p.local.timed >= 3 && p.days < 365 {
		stats = p.local.window
	}
	keys := []string{}
	for k, x := range stats {
		if p.local.labels[k] != "" && x.Examples >= 2 && x.Positive > x.Negative {
			keys = append(keys, k)
		}
	}
	value := func(k string) float64 { x := stats[k]; return (x.Positive - x.Negative) / (x.Observed + 3) }
	sort.Slice(keys, func(i, j int) bool {
		if value(keys[i]) == value(keys[j]) {
			return keys[i] < keys[j]
		}
		return value(keys[i]) > value(keys[j])
	})
	names := []string{}
	for _, k := range keys {
		name := p.local.labels[k]
		if !contains(names, name) {
			names = append(names, name)
		}
		if len(names) == 3 {
			break
		}
	}
	return names
}
