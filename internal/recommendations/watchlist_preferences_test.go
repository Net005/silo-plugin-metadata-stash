package recommendations

import (
	"fmt"
	"testing"
	"time"
)

func TestWatchlistPeriodsUseDatedArchivedHistoryAndCurrentMembership(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	o := DefaultOptions()
	o.Count = 1
	o.Kinds = []string{"watchlist"}
	o.MaxEntityFraction = 1
	rows := []Scene{}
	watch := map[string]bool{}
	for i, age := range []int{2, 20, 200} {
		studio := fmt.Sprint(i)
		for j := 0; j < 3; j++ {
			s := Scene{ID: fmt.Sprintf("archive-%d-%d", i, j), HistoryOnly: true, O: 20, Studio: &Entity{ID: studio}}
			for k := 0; k < []int{1, 3, 10}[i]; k++ {
				s.OHistory = append(s.OHistory, now.AddDate(0, 0, -age).Add(time.Duration(k)*time.Minute).Format(time.RFC3339))
			}
			rows = append(rows, s)
		}
		id := fmt.Sprintf("candidate-%d", i)
		rows = append(rows, Scene{ID: id, MediaID: id, Studio: &Entity{ID: studio}})
		watch[id] = true
	}
	rows = append(rows, Scene{ID: "not-watchlisted", MediaID: "outside", O: 999}, Scene{ID: "missing", Rating: new(int)})
	r := Build("16", rows, rows, o, watch, nil, nil, nil, now)
	if len(r.Collections) != 3 {
		t.Fatalf("collections: %d", len(r.Collections))
	}
	for i, c := range r.Collections {
		if !LocalOnlyKind(c.Kind) {
			t.Fatal("dedicated model request allowed")
		}
		if len(c.Picks) != 1 || c.Picks[0].ID != fmt.Sprintf("candidate-%d", i) {
			t.Fatalf("%s picks %+v", c.Kind, c.Picks)
		}
		for _, p := range c.Candidates {
			if !watch[p.ID] || p.MediaID == "" {
				t.Fatal("history or non-Watchlist item eligible")
			}
		}
	}
	// Removing an item from Stash also removes it from retained old recommendations.
	delete(watch, "candidate-0")
	r = Build("16", rows, nil, o, watch, map[string][]string{"watchlist": {"candidate-0"}}, nil, nil, now)
	for _, c := range r.Collections {
		for _, p := range c.Picks {
			if p.ID == "candidate-0" {
				t.Fatal("removed Watchlist item retained")
			}
		}
	}
}

func TestWatchPreferencesDoNotDateAggregateCountsOrGenericTags(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	o := DefaultOptions()
	high, low := 95, 10
	common := Entity{ID: "common", Name: "Generic"}
	rows := []Scene{{ID: "a", Rating: &high, O: 100, Plays: 100, Tags: []Entity{common}}, {ID: "b", Rating: &low, Tags: []Entity{common}}}
	p := learnWatchPreferences(rows, o, now, 7)
	if p.timed != 0 {
		t.Fatal("undated aggregates counted as recent activity")
	}
	score, _, _ := p.affinity(Scene{Tags: []Entity{common}}, false)
	if score != 0 {
		t.Fatal("ubiquitous tag influenced content ranking")
	}
	if len(newWatchPreferences("watchlist", rows, rows, o, now).global.long) != 0 {
		t.Fatal("same-library evidence counted twice")
	}
}

func TestWatchPreferencesLearnCastCombinationsAndDuration(t *testing.T) {
	now := time.Now()
	o := DefaultOptions()
	rows := []Scene{}
	cast := []Entity{{ID: "a"}, {ID: "b"}}
	for i := 0; i < 5; i++ {
		rows = append(rows, Scene{ID: fmt.Sprint(i), O: 3, Performers: cast, Files: []File{{Duration: 600}}})
	}
	p := learnWatchPreferences(rows, o, now, 30)
	together, _, _ := p.affinity(Scene{Performers: cast, Files: []File{{Duration: 600}}}, false)
	solo, _, _ := p.affinity(Scene{Performers: []Entity{{ID: "a"}}, Files: []File{{Duration: 6000}}}, false)
	if together <= solo {
		t.Fatal("supported cast combination/duration did not improve fit")
	}
}

func TestWatchlistOverlapLimitedWithoutStarvingSparseLibraries(t *testing.T) {
	for _, count := range []int{4, 9} {
		o := DefaultOptions()
		o.Count = 4
		o.MaxEntityFraction = 1
		r := LibraryReport{}
		for _, kind := range []string{"watchlist", "monthly-watchlist", "yearly-watchlist"} {
			c := Collection{Kind: kind}
			for i := 0; i < count; i++ {
				c.Candidates = append(c.Candidates, Pick{ID: fmt.Sprint(i), MediaID: fmt.Sprint(i)})
			}
			r.Collections = append(r.Collections, c)
		}
		Finalize(&r, o, nil)
		seen := map[string]int{}
		for _, c := range r.Collections {
			if len(c.Picks) != 4 {
				t.Fatal("sparse library starved")
			}
			for _, p := range c.Picks {
				seen[p.ID]++
			}
		}
		if count == 9 {
			for _, n := range seen {
				if n > 2 {
					t.Fatal("watchlist overlap cap exceeded")
				}
			}
		}
	}
	o := DefaultOptions()
	o.Kinds = []string{"top-rated"}
	if len(o.WithWatchlistPeriods().Kinds) != 1 {
		t.Fatal("Watchlist enabled in an excluded library")
	}
}
