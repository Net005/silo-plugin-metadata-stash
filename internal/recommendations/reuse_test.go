package recommendations

import (
	"encoding/json"
	"testing"
)

func priority(v float64) *float64 { return &v }
func TestReusePreservesEligibilityAndUncoveredSlots(t *testing.T) {
	r := LibraryReport{LibraryID: "16", Collections: []Collection{
		{Kind: "for-you", Candidates: []Pick{{ID: "a", MediaID: "ma", LunaPriority: priority(0)}, {ID: "b", MediaID: "mb", LunaPriority: priority(100)}, {ID: "not-eligible", LunaPriority: priority(100)}}},
		{Kind: "general-spotlight", Candidates: []Pick{{ID: "a", MediaID: "ma"}, {ID: "uncovered"}, {ID: "b", MediaID: "mb"}, {ID: "tail1"}, {ID: "tail2"}, {ID: "tail3"}, {ID: "tail4"}, {ID: "tail5"}, {ID: "tail6"}, {ID: "tail7"}}},
		{Kind: "new-releases", Candidates: []Pick{{ID: "a", MediaID: "ma"}, {ID: "b", MediaID: "mb"}}},
	}}
	ReuseLunaRankings(&r, nil)
	c := r.Collections[1]
	if c.Candidates[0].ID != "b" || c.Candidates[1].ID != "uncovered" || c.Candidates[2].ID != "a" {
		t.Fatalf("unexpected order: %+v", c.Candidates)
	}
	if c.RankingReuse.Covered != 2 || c.RankingReuse.Total != 10 {
		t.Fatal(c.RankingReuse)
	}
	if r.Collections[2].Candidates[0].ID != "a" || r.Collections[2].RankingReuse != nil {
		t.Fatal("release order changed")
	}
}
func TestReuseSavedPrioritiesAndCurrentOverride(t *testing.T) {
	old := LibraryReport{LibraryID: "16", Collections: []Collection{{Kind: "for-you", Picks: []Pick{{ID: "a", MediaID: "ma", LunaPriority: priority(100)}, {ID: "b", MediaID: "mb", LunaPriority: priority(0)}}}}}
	r := LibraryReport{LibraryID: "16", Collections: []Collection{{Kind: "for-you", Candidates: []Pick{{ID: "a", MediaID: "ma", LunaPriority: priority(0)}, {ID: "b", MediaID: "mb", LunaPriority: priority(100)}}}, {Kind: "cast-spotlight", Candidates: []Pick{{ID: "a", MediaID: "ma"}, {ID: "b", MediaID: "mb"}}}}}
	ReuseLunaRankings(&r, &old)
	if r.Collections[1].RankingReuse.Saved != 0 {
		t.Fatal("current priorities did not replace saved")
	}
	old.LibraryID = "18"
	r.Collections = r.Collections[1:]
	ReuseLunaRankings(&r, &old)
	if r.Collections[0].RankingReuse.Covered != 0 {
		t.Fatal("cross-library reuse")
	}
	old.LibraryID = "16"
	ReuseLunaRankings(&r, &old)
	if r.Collections[0].RankingReuse.Saved != 2 {
		t.Fatal("saved priorities missing")
	}
}
func TestValidatedPrioritiesPersistAndInvalidResponseIsAtomic(t *testing.T) {
	r := LibraryReport{Collections: []Collection{{Kind: "for-you", Candidates: []Pick{{ID: "a"}, {ID: "b"}}}}}
	if err := applyPriorities(&r, []byte(`{"collections":{"for-you":{"a":10,"b":90}}}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	var roundtrip LibraryReport
	json.Unmarshal(raw, &roundtrip)
	if roundtrip.Collections[0].Candidates[0].ID != "b" || *roundtrip.Collections[0].Candidates[0].LunaPriority != 90 {
		t.Fatal("priority not persisted")
	}
	before, _ := json.Marshal(r)
	if err := applyPriorities(&r, []byte(`{"collections":{"for-you":{"a":99,"b":101}}}`)); err == nil {
		t.Fatal("invalid priority accepted")
	}
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatal("invalid response mutated ranking")
	}
}
