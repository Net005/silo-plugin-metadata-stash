package recommendations

import (
	"crypto/sha256"
	"sort"
	"strings"
)

// Additional rails reuse local feedback; they never add OpenAI requests.
func LocalOnlyKind(k string) bool {
	switch k {
	case "monthly-spotlight", "yearly-spotlight", "cast-spotlight", "general-spotlight", "new-releases":
		return true
	}
	return false
}
func selectSpotlightTheme(keys []string, counts map[string]int, names map[string]string, p profile, period string) (string, string) {
	best, score := "", -1.0
	for _, k := range keys {
		x := p[k]
		if counts[k] < 5 || x == nil || x.Examples < 2 || x.Positive <= x.Negative {
			continue
		}
		h := sha256.Sum256([]byte(period + k))
		value := (x.Positive - x.Negative) / (x.Observed + 3) * (.8 + float64(h[0])/640)
		if value > score {
			best, score = k, value
		}
	}
	return best, names[best]
}
func supportedCastThemes(keys []string, counts map[string]int, p profile) []string {
	out := []string{}
	for _, k := range keys {
		x := p[k]
		if strings.HasPrefix(k, "performer:") && counts[k] >= 2 && x != nil && x.Examples >= 2 && x.Positive > x.Negative {
			out = append(out, k)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := p[out[i]], p[out[j]]
		return (a.Positive-a.Negative)/(a.Observed+3) > (b.Positive-b.Negative)/(b.Observed+3)
	})
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

// Generic tags cannot establish personal relevance for new releases.
func specificPreference(s Scene, p profile) bool {
	for _, key := range features(s) {
		if strings.HasPrefix(key, "tag:") {
			continue
		}
		x := p[key]
		if x != nil && x.Examples >= 2 && x.Positive > x.Negative {
			return true
		}
	}
	return false
}
