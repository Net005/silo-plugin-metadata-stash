package recommendations

import (
	"testing"
	"time"
)

func TestPersonalisedNewReleasesExcludeWatchedAndSortByRelease(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	o := DefaultOptions()
	o.Kinds = []string{"new-releases"}
	o.Retain = 1
	rows := []Scene{{ID: "older", MediaID: "older", Date: "2026-09-01", Performers: []Entity{{ID: "cast", Favorite: true}}}, {ID: "newer", MediaID: "newer", Date: "2026-10-01", Performers: []Entity{{ID: "cast", Favorite: true}}}, {ID: "watched", MediaID: "watched", Date: "2026-10-02", Plays: 1, Performers: []Entity{{ID: "cast", Favorite: true}}}, {ID: "future", MediaID: "future", Date: "2026-10-06", Performers: []Entity{{ID: "cast", Favorite: true}}}, {ID: "unrelated", MediaID: "unrelated", Date: "2026-10-03"}}
	r := Build("1", rows, rows, o, nil, map[string][]string{"new-releases": {"older"}}, nil, nil, now)
	if len(r.Collections) != 1 || len(r.Collections[0].Picks) != 2 || r.Collections[0].Picks[0].ID != "newer" {
		t.Fatalf("bad new release selection: %+v", r.Collections)
	}
}
func TestSpotlightPeriodsAndSupportedCast(t *testing.T) {
	p := profile{"performer:a": {Positive: 10, Observed: 5, Examples: 3}, "performer:b": {Positive: 8, Observed: 4, Examples: 2}, "performer:bad": {Positive: 1, Negative: 3, Observed: 4, Examples: 3}}
	counts := map[string]int{"performer:a": 10, "performer:b": 8, "performer:bad": 8}
	keys := []string{"performer:a", "performer:b", "performer:bad"}
	names := map[string]string{"performer:a": "A", "performer:b": "B"}
	a, _ := selectSpotlightTheme(keys, counts, names, p, "2026-10")
	b, _ := selectSpotlightTheme(keys, counts, names, p, "2026-10")
	if a == "" || a != b {
		t.Fatal("unstable supported theme")
	}
	cast := supportedCastThemes(keys, counts, p)
	if len(cast) != 2 {
		t.Fatal(cast)
	}
	for _, k := range []string{"monthly-spotlight", "yearly-spotlight", "cast-spotlight", "general-spotlight", "new-releases"} {
		if !LocalOnlyKind(k) {
			t.Fatal("new rail adds AI calls", k)
		}
	}
}
