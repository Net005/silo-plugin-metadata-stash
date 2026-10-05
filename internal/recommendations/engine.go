// Package recommendations ranks verified local media from explicit Stash feedback.
// It has no Stash mutation client and never infers O events from plays.
package recommendations

import (
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var Kinds = []string{"for-you", "top-rated", "revisit", "favourites", "watchlist", "overlooked", "different", "recent", "spotlight"}
var Titles = map[string]string{"for-you": "For You", "top-rated": "Your Top Rated", "revisit": "Worth Revisiting", "favourites": "From Your Favourites", "watchlist": "Watchlist This Week", "overlooked": "Overlooked Picks", "different": "Something Different", "recent": "Your Recent Direction", "spotlight": "Weekly Spotlight"}

type Entity struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Favorite bool     `json:"favorite"`
	Aliases  []string `json:"alias_list,omitempty"`
}
type File struct {
	Path     string  `json:"path"`
	Duration float64 `json:"duration"`
}
type Scene struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Code        string   `json:"code"`
	Date        string   `json:"date"`
	CreatedAt   string   `json:"created_at"`
	Rating      *int     `json:"rating100"`
	Plays       int      `json:"play_count"`
	O           int      `json:"o_counter"`
	LastPlayed  string   `json:"last_played_at"`
	PlayHistory []string `json:"play_history"`
	OHistory    []string `json:"o_history"`
	Studio      *Entity  `json:"studio"`
	Performers  []Entity `json:"performers"`
	Tags        []Entity `json:"tags"`
	Groups      []struct {
		Group Entity `json:"group"`
	} `json:"groups"`
	Files       []File `json:"files"`
	MediaID     string `json:"media_id,omitempty"`
	LibraryID   string `json:"library_id,omitempty"`
	SiloPlayed  bool   `json:"-"`
	HistoryOnly bool   `json:"-"`
}
type Options struct {
	Enabled            bool     `json:"enabled"`
	Count              int      `json:"count"`
	Kinds              []string `json:"collections"`
	High               int      `json:"high_rating"`
	Low                int      `json:"low_rating"`
	Cooldown           int      `json:"cooldown_days"`
	RecentDays         int      `json:"recent_days"`
	Retain             float64  `json:"retain_fraction"`
	MaxEntityFraction  float64  `json:"max_entity_fraction"`
	MaxOverlap         int      `json:"max_discovery_overlap"`
	ExcludedScenes     []string `json:"excluded_scenes"`
	ExcludedPerformers []string `json:"excluded_performers"`
	ExcludedStudios    []string `json:"excluded_studios"`
	ExcludedTags       []string `json:"excluded_tags"`
	CrossLibrary       bool     `json:"cross_library"`
}

func DefaultOptions() Options {
	return Options{Enabled: true, Count: 250, Kinds: append([]string(nil), Kinds...), High: 80, Low: 40, Cooldown: 14, RecentDays: 90, Retain: .7, MaxEntityFraction: .3, MaxOverlap: 2, CrossLibrary: true}
}
func (o Options) Validate() error {
	if o.Count < 1 || o.Count > 250 || o.High < 1 || o.High > 100 || o.Low < 0 || o.Low >= o.High || o.Cooldown < 0 || o.RecentDays < 1 || o.Retain < 0 || o.Retain > 1 || o.MaxEntityFraction <= 0 || o.MaxEntityFraction > 1 || o.MaxOverlap < 1 {
		return fmt.Errorf("invalid recommendation limits or rating thresholds")
	}
	seen := map[string]bool{}
	for _, k := range o.Kinds {
		if Titles[k] == "" || seen[k] {
			return fmt.Errorf("unknown or duplicate collection %q", k)
		}
		seen[k] = true
	}
	return nil
}

type Exposure struct {
	Count    int    `json:"count"`
	LastWeek string `json:"last_week"`
}
type Pick struct {
	ID           string   `json:"id"`
	MediaID      string   `json:"media_id"`
	Title        string   `json:"title"`
	Score        float64  `json:"score"`
	Confidence   string   `json:"confidence"`
	Reasons      []string `json:"reasons"`
	Rating       *int     `json:"rating,omitempty"`
	Studio       string   `json:"studio,omitempty"`
	PerformerIDs []string `json:"performer_ids,omitempty"`
}
type Collection struct {
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Candidates  []Pick `json:"candidates,omitempty"`
	Picks       []Pick `json:"picks"`
}
type LibraryReport struct {
	Matched        int          `json:"matched_scenes"`
	Evaluation     Evaluation   `json:"evaluation"`
	LibraryID      string       `json:"library_id"`
	Eligible       int          `json:"eligible"`
	FeedbackScenes int          `json:"feedback_scenes"`
	Collections    []Collection `json:"collections"`
	Warnings       []string     `json:"warnings"`
}
type featureStats struct {
	Positive, Negative, Observed, Recent float64
	Examples                             int
}
type profile map[string]*featureStats

func features(s Scene) []string {
	out := []string{}
	if s.Studio != nil && s.Studio.ID != "" {
		out = append(out, "studio:"+s.Studio.ID)
	}
	for _, p := range s.Performers {
		out = append(out, "performer:"+p.ID)
	}
	for _, t := range s.Tags {
		out = append(out, "tag:"+t.ID)
	}
	for _, g := range s.Groups {
		out = append(out, "series:"+g.Group.ID)
	}
	return unique(out)
}
func unique(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, x := range in {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
func rated(s Scene) bool             { return s.Rating != nil && *s.Rating > 0 }
func played(s Scene) bool            { return s.Plays > 0 || len(s.PlayHistory) > 0 || s.SiloPlayed }
func eventTime(raw string) time.Time { t, _ := time.Parse(time.RFC3339, raw); return t }
func lastPlay(s Scene) time.Time {
	t := eventTime(s.LastPlayed)
	for _, e := range s.PlayHistory {
		if x := eventTime(e); x.After(t) {
			t = x
		}
	}
	return t
}
func recentEvidence(s Scene, now time.Time, days int) bool {
	cutoff := now.AddDate(0, 0, -days)
	for _, e := range append(append([]string(nil), s.PlayHistory...), s.OHistory...) {
		t := eventTime(e)
		if !t.IsZero() && !t.After(now) && !t.Before(cutoff) {
			return true
		}
	}
	return false
}
func evidence(s Scene, o Options) (positive, negative float64, observed bool) {
	observed = played(s) || s.O > 0 || rated(s)
	if rated(s) {
		r := *s.Rating
		if r >= o.High {
			positive += 3 * float64(r) / 100
		} else if r <= o.Low {
			negative += 2 * float64(o.Low-r+1) / float64(max(1, o.Low))
		} else {
			positive += .25 * float64(r) / 100
		}
	}
	if s.O > 0 {
		positive += 2 * math.Log1p(float64(s.O))
	}
	days := map[string]bool{}
	for _, e := range s.PlayHistory {
		if t := eventTime(e); !t.IsZero() {
			days[t.Format("2006-01-02")] = true
		}
	}
	positive += .25 * math.Log1p(float64(max(s.Plays, len(days))))
	if len(days) > 1 {
		positive += .5 * math.Log1p(float64(len(days)-1))
	}
	return
}
func learn(rows []Scene, o Options, now time.Time) (profile, int) {
	p := profile{}
	n := 0
	seen := map[string]bool{}
	for _, s := range rows {
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		pos, neg, observed := evidence(s, o)
		if !observed {
			continue
		}
		n++
		for _, f := range features(s) {
			x := p[f]
			if x == nil {
				x = &featureStats{}
				p[f] = x
			}
			x.Observed++
			x.Positive += pos
			x.Negative += neg
			x.Examples++
			if recentEvidence(s, now, o.RecentDays) {
				x.Recent += pos - neg
			}
		}
	}
	return p, n
}
func affinities(s Scene, p profile) (long, recent float64, n int) {
	totals := map[string]float64{}
	counts := map[string]int{}
	rTotals := map[string]float64{}
	for _, f := range features(s) {
		x := p[f]
		if x == nil {
			continue
		}
		kind := strings.SplitN(f, ":", 2)[0]
		support := float64(x.Examples) / (float64(x.Examples) + 3)
		v := (x.Positive - x.Negative) / (x.Observed + 3) * support
		totals[kind] += v
		rTotals[kind] += x.Recent / (x.Observed + 3) * support
		counts[kind]++
		n += x.Examples
	}
	for k, c := range counts {
		w := 1.
		if k == "tag" {
			w = .5
		}
		long += w * totals[k] / float64(c)
		recent += w * rTotals[k] / float64(c)
	}
	return
}
func contains(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
func excluded(s Scene, o Options) bool {
	if contains(o.ExcludedScenes, s.ID) {
		return true
	}
	if s.Studio != nil && (contains(o.ExcludedStudios, s.Studio.ID) || contains(o.ExcludedStudios, s.Studio.Name)) {
		return true
	}
	for _, p := range s.Performers {
		if contains(o.ExcludedPerformers, p.ID) || contains(o.ExcludedPerformers, p.Name) {
			return true
		}
	}
	for _, t := range s.Tags {
		if contains(o.ExcludedTags, t.ID) || contains(o.ExcludedTags, t.Name) {
			return true
		}
	}
	return false
}
func discovery(k string) bool { return k != "top-rated" && k != "revisit" && k != "watchlist" }
func Build(library string, local, global []Scene, o Options, watch map[string]bool, previous map[string][]string, exposure map[string]Exposure, dismissed map[string]bool, now time.Time) LibraryReport {
	p, n := learn(local, o, now)
	currentGlobal, archivedGlobal := []Scene{}, []Scene{}
	for _, row := range global {
		if row.HistoryOnly {
			if row.LibraryID != library {
				archivedGlobal = append(archivedGlobal, row)
			}
		} else {
			currentGlobal = append(currentGlobal, row)
		}
	}
	gp, _ := learn(currentGlobal, o, now)
	hp, _ := learn(archivedGlobal, o, now)
	report := LibraryReport{LibraryID: library, FeedbackScenes: n, Collections: []Collection{}}
	if n < 10 {
		report.Warnings = append(report.Warnings, "Sparse local feedback; recommendations have limited confidence")
	}
	week := Week(now)
	rows := map[string]Scene{}
	spotlightKey := ""
	spotlightName := ""
	spotlightScore := -1.
	themeCounts := map[string]int{}
	themeNames := map[string]string{}
	for _, s := range local {
		if s.MediaID == "" || excluded(s, o) || dismissed[s.ID] || rated(s) && *s.Rating <= o.Low {
			continue
		}
		rows[s.ID] = s
		if !played(s) {
			for _, f := range features(s) {
				if strings.HasPrefix(f, "tag:") {
					continue
				}
				themeCounts[f]++
				if s.Studio != nil && f == "studio:"+s.Studio.ID {
					themeNames[f] = s.Studio.Name
				}
				for _, e := range s.Performers {
					if f == "performer:"+e.ID {
						themeNames[f] = e.Name
					}
				}
				for _, g := range s.Groups {
					if f == "series:"+g.Group.ID {
						themeNames[f] = g.Group.Name
					}
				}
			}
		}
	}
	report.Eligible = len(rows)
	keys := []string{}
	for k := range themeCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		x := p[k]
		if themeCounts[k] < 5 || x == nil || x.Examples < 2 || x.Positive <= x.Negative {
			continue
		}
		score := (x.Positive - x.Negative) / (x.Observed + 3)
		h := sha256.Sum256([]byte(week + k))
		score *= .8 + float64(h[0])/640
		if score > spotlightScore {
			spotlightKey = k
			spotlightName = themeNames[k]
			spotlightScore = score
		}
	}
	for _, kind := range o.Kinds {
		c := Collection{Kind: kind, Title: Titles[kind], Description: "Weekly Stash recommendations; evidence from ratings, explicit activity and favourites.", Candidates: []Pick{}, Picks: []Pick{}}
		if kind == "spotlight" {
			if spotlightKey == "" {
				continue
			}
			c.Title += " · " + spotlightName
			c.Description = "This week's spotlight: " + spotlightName + ". Supported by multiple positively observed scenes."
		}
		for _, s := range rows {
			isPlayed := played(s)
			cool := !lastPlay(s).IsZero() && now.Sub(lastPlay(s)) < time.Duration(o.Cooldown)*24*time.Hour
			if kind == "top-rated" {
				if !rated(s) || *s.Rating < o.High {
					continue
				}
			} else if kind == "revisit" {
				if !isPlayed || cool || !(s.O > 0 || s.Plays > 1 || rated(s) && *s.Rating >= o.High) {
					continue
				}
			} else if kind == "watchlist" {
				if !watch[s.ID] {
					continue
				}
			} else if isPlayed || s.O > 0 {
				continue
			}
			fit, rec, support := affinities(s, p)
			if o.CrossLibrary && n < 10 {
				g, r, gn := affinities(s, gp)
				fit += .2 * g
				rec += .2 * r
				support += min(gn, 3)
			}
			if o.CrossLibrary {
				h, recentHistory, hn := affinities(s, hp)
				fit += .2 * h
				rec += .2 * recentHistory
				support += min(hn, 3)
			}
			favourite := false
			for _, e := range s.Performers {
				if e.Favorite {
					favourite = true
				}
			}
			rating := 0.
			if rated(s) {
				rating = float64(*s.Rating) / 100
			}
			base := fit + rating*2
			if favourite {
				base += 1.5
			}
			reasons := []string{}
			if !isPlayed {
				reasons = append(reasons, "No recorded play")
			}
			if rated(s) {
				reasons = append(reasons, fmt.Sprintf("Scene rating %d/100", *s.Rating))
			}
			if favourite {
				reasons = append(reasons, "Favourited performer")
			}
			if fit > 0 {
				reasons = append(reasons, "Related metadata has positive feedback")
			}
			if s.O > 0 {
				reasons = append(reasons, fmt.Sprintf("%d explicit O events recorded", s.O))
			}
			if s.Plays > 1 {
				reasons = append(reasons, fmt.Sprintf("%d recorded plays", s.Plays))
			}
			switch kind {
			case "for-you":
				if base <= 0 {
					continue
				}
			case "top-rated":
				base = rating*100 + math.Min(fit, 1)
				if cool {
					base -= 2
				}
			case "revisit":
				pos, _, _ := evidence(s, o)
				base += pos
			case "favourites":
				supported := false
				for _, f := range features(s) {
					x := p[f]
					if !strings.HasPrefix(f, "tag:") && x != nil && x.Examples >= 3 && x.Positive > x.Negative {
						supported = true
					}
				}
				if !favourite && !supported {
					continue
				}
			case "watchlist":
				reasons = append(reasons, "In Stash Watchlist")
				base += .5
			case "overlooked":
				added := eventTime(s.CreatedAt)
				if added.IsZero() || now.Sub(added) < 180*24*time.Hour || base <= 0 {
					continue
				}
				base += 1 / (1 + float64(exposure[s.ID].Count))
				reasons = append(reasons, "In the library for at least 180 days")
			case "different":
				if fit <= 0 {
					continue
				}
				base = fit/(1+fit) + rating
				reasons = append(reasons, "Some supported similarities; diversity selection")
			case "recent":
				if rec <= 0 {
					continue
				}
				base += 2 * rec
				reasons = append(reasons, "Related metadata has recent positive activity")
			case "spotlight":
				if !contains(features(s), spotlightKey) {
					continue
				}
				reasons = append(reasons, "Matches this week's supported theme")
			}
			if kind != "top-rated" {
				base -= math.Log1p(float64(exposure[s.ID].Count)) * .15
			}
			confidence := "low"
			if support >= 6 || rated(s) || favourite || s.O > 0 || s.Plays > 1 {
				confidence = "medium"
			}
			if support >= 15 {
				confidence = "high"
			}
			pick := Pick{ID: s.ID, MediaID: s.MediaID, Title: s.Title, Score: base, Confidence: confidence, Reasons: reasons, Rating: s.Rating}
			if s.Studio != nil {
				pick.Studio = s.Studio.ID
			}
			for _, e := range s.Performers {
				pick.PerformerIDs = append(pick.PerformerIDs, e.ID)
			}
			c.Candidates = append(c.Candidates, pick)
		}
		sort.Slice(c.Candidates, func(i, j int) bool {
			a, b := c.Candidates[i], c.Candidates[j]
			if a.Score == b.Score {
				return a.ID < b.ID
			}
			return a.Score > b.Score
		})
		if len(c.Candidates) > o.Count*6 {
			c.Candidates = c.Candidates[:o.Count*6]
		}
		report.Collections = append(report.Collections, c)
	}
	Finalize(&report, o, previous)
	// Keep a bounded AI pool that includes every locally feasible selection.
	// A larger local pool avoids exhausting diversity and overlap constraints.
	for i := range report.Collections {
		c := &report.Collections[i]
		if len(c.Candidates) <= o.Count*2 {
			continue
		}
		selected := map[string]bool{}
		for _, p := range c.Picks {
			selected[p.ID] = true
		}
		pool := append([]Pick(nil), c.Picks...)
		for _, p := range c.Candidates {
			if !selected[p.ID] && len(pool) < o.Count*2 {
				pool = append(pool, p)
			}
		}
		c.Candidates = pool
	}
	return report
}

// Finalize treats model order only as a suggestion; eligibility, diversity,
// overlap and count limits remain enforced locally.
func Finalize(report *LibraryReport, o Options, previous map[string][]string) {
	overlap := map[string]int{}
	for i := range report.Collections {
		c := &report.Collections[i]
		eligible := map[string]Pick{}
		for _, p := range c.Candidates {
			eligible[p.ID] = p
		}
		chosen := map[string]bool{}
		studios := map[string]int{}
		performers := map[string]int{}
		c.Picks = []Pick{}
		add := func(p Pick) bool {
			if chosen[p.ID] || len(c.Picks) >= o.Count || discovery(c.Kind) && overlap[p.ID] >= o.MaxOverlap {
				return false
			}
			cap := max(1, int(math.Ceil(float64(o.Count)*o.MaxEntityFraction)))
			studioPopulation := 0
			for _, candidate := range c.Candidates {
				if p.Studio != "" && candidate.Studio == p.Studio {
					studioPopulation++
				}
			}
			studioCap := max(cap, int(math.Ceil(float64(o.Count)*float64(studioPopulation)/float64(max(1, len(c.Candidates))))))
			if c.Kind != "spotlight" {
				if p.Studio != "" && studios[p.Studio] >= studioCap {
					return false
				}
				for _, x := range p.PerformerIDs {
					if performers[x] >= cap {
						return false
					}
				}
			}
			chosen[p.ID] = true
			c.Picks = append(c.Picks, p)
			if discovery(c.Kind) {
				overlap[p.ID]++
			}
			studios[p.Studio]++
			for _, x := range p.PerformerIDs {
				performers[x]++
			}
			return true
		}
		keep := int(float64(o.Count) * o.Retain)
		for _, id := range previous[c.Kind] {
			if len(c.Picks) >= keep {
				break
			}
			if p, ok := eligible[id]; ok {
				add(p)
			}
		}
		for _, p := range c.Candidates {
			add(p)
		}
	}
}
func Week(now time.Time) string { y, w := now.ISOWeek(); return fmt.Sprintf("%04d-W%02d", y, w) }

// HistoryDrop stops automatic publishing when previously observed activity vanishes.
func HistoryDrop(old, new map[string]int) bool {
	for _, k := range []string{"plays", "o", "scenes"} {
		if old[k] >= 10 && new[k] < old[k]*8/10 {
			return true
		}
	}
	return false
}
