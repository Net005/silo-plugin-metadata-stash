package recommendations

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestRatingsExplicitActivityAndEligibility(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	high, low := 90, 20
	o := DefaultOptions()
	o.MaxEntityFraction = 1
	rows := []Scene{{ID: "good", MediaID: "local-good", Rating: &high, Studio: &Entity{ID: "s"}}, {ID: "bad", MediaID: "local-bad", Rating: &low, Studio: &Entity{ID: "s"}}, {ID: "played", MediaID: "local-played", Plays: 2, PlayHistory: []string{"2022-01-01T12:00:00Z", "2022-01-02T12:00:00Z"}, Studio: &Entity{ID: "s"}}, {ID: "explicit-o", MediaID: "local-o", Plays: 1, O: 2, LastPlayed: "2022-01-01T12:00:00Z"}, {ID: "missing-file", Rating: &high}}
	r := Build("16", rows, nil, o, nil, nil, nil, nil, now)
	foundRated, foundRevisit := false, false
	for _, c := range r.Collections {
		for _, p := range c.Picks {
			if p.ID == "bad" || p.ID == "missing-file" {
				t.Fatalf("invalid eligibility %+v", p)
			}
			if c.Kind == "top-rated" && p.ID == "good" {
				foundRated = true
			}
			if c.Kind == "revisit" && p.ID == "explicit-o" {
				foundRevisit = true
			}
			if discovery(c.Kind) && (p.ID == "played" || p.ID == "explicit-o") {
				t.Fatalf("watched item entered discovery %s", c.Kind)
			}
		}
	}
	if !foundRated || !foundRevisit {
		t.Fatalf("ratings/revisit absent %+v", r)
	}
	if rows[2].O != 0 {
		t.Fatal("play inferred O feedback")
	}
}
func TestLunaCannotIntroduceUnknownIDsOrPartiallyReorder(t *testing.T) {
	r := LibraryReport{Collections: []Collection{{Kind: "for-you", Candidates: []Pick{{ID: "a"}, {ID: "b"}}}, {Kind: "top-rated", Candidates: []Pick{{ID: "c"}}}}}
	bad := []byte(`{"collections":[{"kind":"for-you","ids":["b","a"]},{"kind":"top-rated","ids":["unknown"]}]}`)
	if ApplySuggestion(&r, bad) == nil {
		t.Fatal("unknown candidate accepted")
	}
	if r.Collections[0].Candidates[0].ID != "a" {
		t.Fatal("partial mutation after rejected response")
	}
	if err := ApplySuggestion(&r, []byte(`{"collections":[{"kind":"for-you","ids":["b","a"]},{"kind":"top-rated","ids":["c"]}]}`)); err != nil {
		t.Fatal(err)
	}
	if r.Collections[0].Candidates[0].ID != "b" {
		t.Fatal("valid ranking not used")
	}
}
func TestFiftyItemsDiversityOverlapAndRetention(t *testing.T) {
	o := DefaultOptions()
	o.Count = 50
	o.Kinds = []string{"for-you", "different", "recent"}
	o.MaxEntityFraction = .3
	report := LibraryReport{}
	for _, k := range o.Kinds {
		c := Collection{Kind: k}
		for i := 0; i < 150; i++ {
			c.Candidates = append(c.Candidates, Pick{ID: fmt.Sprint(i), MediaID: fmt.Sprint(i), Studio: fmt.Sprint(i % 10), PerformerIDs: []string{fmt.Sprint(i % 20)}})
		}
		report.Collections = append(report.Collections, c)
	}
	Finalize(&report, o, map[string][]string{"for-you": {"99", "98"}})
	seen := map[string]int{}
	for _, c := range report.Collections {
		if len(c.Picks) != 50 {
			t.Fatalf("%s: count=%d", c.Kind, len(c.Picks))
		}
		for _, p := range c.Picks {
			seen[p.ID]++
			if seen[p.ID] > 2 {
				t.Fatal("discovery overlap exceeded")
			}
		}
	}
	if report.Collections[0].Picks[0].ID != "99" {
		t.Fatal("retention ignored")
	}
}
func TestSingleStudioDoesNotArtificiallyLimitCollection(t *testing.T) {
	o := DefaultOptions()
	o.Count = 50
	r := LibraryReport{Collections: []Collection{{Kind: "for-you"}}}
	for i := 0; i < 100; i++ {
		r.Collections[0].Candidates = append(r.Collections[0].Candidates, Pick{ID: fmt.Sprint(i), Studio: "dominant"})
	}
	Finalize(&r, o, nil)
	if len(r.Collections[0].Picks) != 50 {
		t.Fatal(len(r.Collections[0].Picks))
	}
}
func TestPrivacyCostAndSourceDrop(t *testing.T) {
	r := LibraryReport{Collections: []Collection{{Kind: "for-you", Candidates: []Pick{{ID: "1", Title: "private title", Reasons: []string{"Scene rating 90/100"}}}}}}
	b := LunaInput(r)
	var d map[string]any
	if json.Unmarshal(b, &d) != nil {
		t.Fatal("invalid model payload")
	}
	if string(b) == "" {
		t.Fatal("empty payload")
	}
	for _, x := range []string{"private title", "api_key", "file_path"} {
		if containsBytes(b, x) {
			t.Fatal("sensitive payload", x)
		}
	}
	if !HistoryDrop(map[string]int{"plays": 100}, map[string]int{"plays": 20}) {
		t.Fatal("reset not detected")
	}
	if HistoryDrop(map[string]int{"plays": 100}, map[string]int{"plays": 99}) {
		t.Fatal("minor correction blocked")
	}
	if math.Abs(Cost(500000, 50000)-.075) > 1e-12 {
		t.Fatal(Cost(500000, 50000))
	}
}
func containsBytes(b []byte, s string) bool {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return true
		}
	}
	return false
}

func TestTwoHundredFiftyVerifiedUniquePicks(t *testing.T) {
	o := DefaultOptions()
	if o.Count != 250 || o.Validate() != nil {
		t.Fatal("250 default must be valid")
	}
	r := LibraryReport{Collections: []Collection{{Kind: "for-you"}}}
	for i := 0; i < 500; i++ {
		r.Collections[0].Candidates = append(r.Collections[0].Candidates, Pick{ID: fmt.Sprint(i), MediaID: fmt.Sprint(i), Studio: fmt.Sprint(i % 10)})
	}
	Finalize(&r, o, nil)
	if len(r.Collections[0].Picks) != 250 {
		t.Fatal(len(r.Collections[0].Picks))
	}
	seen := map[string]bool{}
	for _, p := range r.Collections[0].Picks {
		if seen[p.ID] {
			t.Fatal("duplicate")
		}
		seen[p.ID] = true
	}
	o.Count = 251
	if o.Validate() == nil {
		t.Fatal("unbounded count accepted")
	}
}

func TestRemovedReleaseHistoryInfluencesRichLibraryAndHonoursCrossLibrarySetting(t *testing.T) {
	o := DefaultOptions()
	o.Kinds = []string{"for-you"}
	rating := 90
	local := []Scene{{ID: "candidate", MediaID: "available", Rating: &rating, Studio: &Entity{ID: "target", Name: "Target"}}}
	for i := 0; i < 20; i++ {
		local = append(local, Scene{ID: fmt.Sprint(i), MediaID: fmt.Sprint(i), Plays: 1, Studio: &Entity{ID: "other", Name: "Other"}})
	}
	archived := Scene{ID: "archive-release:9", HistoryOnly: true, O: 5, Studio: &Entity{ID: "target", Name: "Target"}}
	now := time.Now()
	score := func(extra []Scene, cross bool) float64 {
		opt := o
		opt.CrossLibrary = cross
		r := Build("16", local, append(append([]Scene(nil), local...), extra...), opt, nil, nil, nil, nil, now)
		for _, c := range r.Collections {
			for _, p := range c.Candidates {
				if p.ID == "candidate" {
					return p.Score
				}
			}
		}
		t.Fatal("candidate absent")
		return 0
	}
	baseline := score(nil, true)
	if score([]Scene{archived}, true) <= baseline {
		t.Fatal("archive ignored in rich library")
	}
	if score([]Scene{archived}, false) != score(nil, false) {
		t.Fatal("cross-library setting ignored")
	}
	archived.LibraryID = "16"
	if score([]Scene{archived}, true) != baseline {
		t.Fatal("assigned archive was double-counted globally")
	}
}
