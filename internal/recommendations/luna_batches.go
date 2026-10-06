package recommendations

import (
	"fmt"
	"math"
	"sort"
)

const lunaBatchSize = 100

// LunaBatches keeps every candidate, using shared comparison anchors to align
// scores across requests without asking for a fragile 1,000-property response.
func LunaBatches(r LibraryReport) []LibraryReport {
	out := []LibraryReport{}
	for _, col := range r.Collections {
		seen := map[string]bool{}
		pool := []Pick{}
		for _, p := range col.Candidates {
			if !seen[p.ID] {
				seen[p.ID] = true
				pool = append(pool, p)
			}
		}
		anchors := []Pick{}
		anchorIDs := map[string]bool{}
		if len(pool) > lunaBatchSize {
			ranked := append([]Pick(nil), pool...)
			sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
			for i := 0; i < 12; i++ {
				p := ranked[i*(len(ranked)-1)/11]
				anchors = append(anchors, p)
				anchorIDs[p.ID] = true
			}
		}
		remaining := []Pick{}
		for _, p := range pool {
			if !anchorIDs[p.ID] {
				remaining = append(remaining, p)
			}
		}
		for len(remaining) > 0 {
			n := min(lunaBatchSize-len(anchors), len(remaining))
			c := col
			c.Picks = nil
			c.Candidates = append(append([]Pick(nil), anchors...), remaining[:n]...)
			b := r
			b.LunaAnchors = nil
			for _, p := range anchors {
				b.LunaAnchors = append(b.LunaAnchors, p.ID)
			}
			b.Collections = []Collection{c}
			out = append(out, b)
			remaining = remaining[n:]
		}
	}
	return out
}

// MergeLunaBatches is atomic: incomplete or inconsistent results keep the local
// candidate ordering. Shared anchors correct median scale drift between calls.
func MergeLunaBatches(r *LibraryReport, batches []LibraryReport) error {
	updated := append([]Collection(nil), r.Collections...)
	for ci, col := range r.Collections {
		parts := []Collection{}
		for _, b := range batches {
			for _, c := range b.Collections {
				if c.Kind == col.Kind {
					parts = append(parts, c)
				}
			}
		}
		if len(parts) == 0 {
			return fmt.Errorf("Luna omitted collection batches")
		}
		reference := map[string]float64{}
		occurrences := map[string]int{}
		for _, part := range parts {
			seen := map[string]bool{}
			for _, p := range part.Candidates {
				if seen[p.ID] || p.LunaPriority == nil || math.IsNaN(*p.LunaPriority) || math.IsInf(*p.LunaPriority, 0) || *p.LunaPriority < 0 || *p.LunaPriority > 100 {
					return fmt.Errorf("Luna batch candidate invalid")
				}
				seen[p.ID] = true
				occurrences[p.ID]++
			}
		}
		for _, p := range parts[0].Candidates {
			reference[p.ID] = *p.LunaPriority
		}
		scores := map[string]float64{}
		for pi, part := range parts {
			deltas := []float64{}
			if pi > 0 {
				for _, p := range part.Candidates {
					if occurrences[p.ID] == len(parts) {
						deltas = append(deltas, reference[p.ID]-*p.LunaPriority)
					}
				}
			}
			offset := 0.0
			if len(deltas) > 0 {
				sort.Float64s(deltas)
				offset = deltas[len(deltas)/2]
				if len(deltas)%2 == 0 {
					offset = (offset + deltas[len(deltas)/2-1]) / 2
				}
			}
			for _, p := range part.Candidates {
				if _, ok := scores[p.ID]; !ok {
					scores[p.ID] = max(0, min(100, *p.LunaPriority+offset))
				}
			}
		}
		pool := []Pick{}
		seen := map[string]bool{}
		for _, p := range col.Candidates {
			v, ok := scores[p.ID]
			if !ok {
				return fmt.Errorf("Luna batch omitted candidate")
			}
			if seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			p.LunaPriority = &v
			pool = append(pool, p)
		}
		if len(scores) != len(seen) {
			return fmt.Errorf("Luna batch returned unknown candidate")
		}
		sort.SliceStable(pool, func(i, j int) bool { return *pool[i].LunaPriority > *pool[j].LunaPriority })
		updated[ci].Candidates = pool
	}
	r.Collections = updated
	return nil
}
