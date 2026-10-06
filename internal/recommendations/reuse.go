package recommendations

import (
	"math"
	"sort"
)

// RankingReuse is report evidence, not an additional API usage charge.
type RankingReuse struct {
	Covered int      `json:"covered_candidates"`
	Total   int      `json:"total_candidates"`
	Saved   int      `json:"saved_candidates"`
	Sources []string `json:"source_collections"`
}

func reuseSources(kind string) []string {
	switch kind {
	case "watchlist":
		return []string{"recent", "for-you", "favourites"}
	case "monthly-watchlist":
		return []string{"for-you", "favourites", "recent", "different"}
	case "yearly-watchlist":
		return []string{"for-you", "favourites", "overlooked"}
	case "monthly-spotlight", "yearly-spotlight":
		return []string{"spotlight", "for-you", "favourites", "overlooked", "recent"}
	case "cast-spotlight":
		return []string{"favourites", "for-you", "spotlight"}
	case "general-spotlight":
		return []string{"for-you", "different", "overlooked", "recent"}
	}
	return nil // New releases retain their release-date ordering.
}

type reusedPriority struct {
	sum, weight float64
	saved       bool
	sources     map[string]bool
}

// ReuseLunaRankings only reorders covered slots in an already eligible shortlist.
// Uncovered candidates keep their local positions. Each source has equal weight,
// using its percentile rather than comparing model scales across collection types.
// Current priorities replace saved priorities for the same collection and scene.
func ReuseLunaRankings(r *LibraryReport, saved *LibraryReport) {
	if saved != nil && saved.LibraryID != r.LibraryID {
		saved = nil
	}
	for ci := range r.Collections {
		c := &r.Collections[ci]
		kinds := reuseSources(c.Kind)
		if len(kinds) == 0 {
			continue
		}
		scores := map[string]*reusedPriority{}
		for _, kind := range kinds {
			rows := map[string]Pick{}
			fromCurrent := map[string]bool{}
			collect := func(cols []Collection, current bool) {
				for _, source := range cols {
					if source.Kind != kind {
						continue
					}
					pool := source.Candidates
					if len(pool) == 0 {
						pool = source.Picks
					}
					for _, p := range pool {
						if p.LunaPriority == nil || math.IsNaN(*p.LunaPriority) || math.IsInf(*p.LunaPriority, 0) || *p.LunaPriority < 0 || *p.LunaPriority > 100 {
							continue
						}
						rows[p.ID] = p
						fromCurrent[p.ID] = current
					}
				}
			}
			if saved != nil {
				collect(saved.Collections, false)
			}
			collect(r.Collections, true)
			priorities := []float64{}
			for _, p := range rows {
				priorities = append(priorities, *p.LunaPriority)
			}
			sort.Float64s(priorities)
			for _, p := range c.Candidates {
				source, ok := rows[p.ID]
				if !ok || source.MediaID != p.MediaID {
					continue
				}
				v := *source.LunaPriority
				low := sort.SearchFloat64s(priorities, v)
				high := sort.Search(len(priorities), func(i int) bool { return priorities[i] > v })
				percentile := .5
				if len(priorities) > 1 {
					percentile = (float64(low+high-1) / 2) / float64(len(priorities)-1)
				}
				x := scores[p.ID]
				if x == nil {
					x = &reusedPriority{sources: map[string]bool{}}
					scores[p.ID] = x
				}
				weight := 1.0
				if !fromCurrent[p.ID] {
					weight = .5
					x.saved = true
				}
				x.sum += percentile * weight
				x.weight += weight
				x.sources[kind] = true
			}
		}
		slots := []int{}
		covered := []Pick{}
		blend := map[string]float64{}
		summary := &RankingReuse{Total: len(c.Candidates)}
		sources := map[string]bool{}
		for i, p := range c.Candidates {
			x := scores[p.ID]
			if x == nil {
				continue
			}
			slots = append(slots, i)
			covered = append(covered, p)
			local := 1 - float64(i)/float64(max(1, len(c.Candidates)-1))
			blend[p.ID] = .75*local + .25*x.sum/x.weight
			if x.saved {
				summary.Saved++
			}
			for k := range x.sources {
				sources[k] = true
			}
		}
		sort.SliceStable(covered, func(i, j int) bool { return blend[covered[i].ID] > blend[covered[j].ID] })
		for i, slot := range slots {
			c.Candidates[slot] = covered[i]
		}
		summary.Covered = len(covered)
		for k := range sources {
			summary.Sources = append(summary.Sources, k)
		}
		sort.Strings(summary.Sources)
		c.RankingReuse = summary
	}
}
