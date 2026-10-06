package recommendations

import (
	"fmt"
	"testing"
)

func TestLunaBatchesRetainWholePoolAndCalibrateSharedAnchors(t *testing.T) {
	r := LibraryReport{Collections: []Collection{{Kind: "for-you"}}}
	for i := 0; i < 1000; i++ {
		r.Collections[0].Candidates = append(r.Collections[0].Candidates, Pick{ID: fmt.Sprint(i), Score: float64(i) / 10, Reasons: []string{"Related metadata has positive feedback"}})
	}
	// Duplicate identities must not make a required-property schema impossible.
	r.Collections[0].Candidates = append(r.Collections[0].Candidates, r.Collections[0].Candidates[0])
	bs := LunaBatches(r)
	seen := map[string]bool{}
	if len(bs) != 5 {
		t.Fatal("unbounded batch count", len(bs))
	}
	for i := range bs {
		if len(bs[i].Collections[0].Candidates) > 250 || len(bs[i].LunaAnchors) != 12 || len(LunaRequest(bs[i], "none", 8000)) > 240000 {
			t.Fatal("batch bound or anchors missing")
		}
		for j := range bs[i].Collections[0].Candidates {
			p := &bs[i].Collections[0].Candidates[j]
			seen[p.ID] = true
			v := p.Score/2 + 10 + float64(i)
			p.LunaPriority = &v
		}
	}
	if len(seen) != 1000 {
		t.Fatal("candidates lost", len(seen))
	}
	if err := MergeLunaBatches(&r, bs); err != nil {
		t.Fatal(err)
	}
	if len(r.Collections[0].Candidates) != 1000 {
		t.Fatal("duplicate candidate survived")
	}
	for _, p := range r.Collections[0].Candidates {
		if d := *p.LunaPriority - (p.Score/2 + 10); d < -0.000001 || d > 0.000001 {
			t.Fatal("batch scale drift", p.ID, d)
		}
	}
	if r.Collections[0].Candidates[0].ID != "999" {
		t.Fatal("ranking not merged")
	}
}

func TestLunaBatchMergeIsAtomicOnMissingOrUnknownCandidate(t *testing.T) {
	for _, bad := range []string{"missing", "unknown"} {
		t.Run(bad, func(t *testing.T) {
			r := LibraryReport{Collections: []Collection{{Kind: "for-you", Candidates: []Pick{{ID: "a"}, {ID: "b"}}}}}
			bs := LunaBatches(r)
			v := 50.0
			for j := range bs[0].Collections[0].Candidates {
				bs[0].Collections[0].Candidates[j].LunaPriority = &v
			}
			if bad == "missing" {
				bs[0].Collections[0].Candidates = bs[0].Collections[0].Candidates[:1]
			} else {
				bs[0].Collections[0].Candidates[1].ID = "unknown"
			}
			if err := MergeLunaBatches(&r, bs); err == nil {
				t.Fatal("invalid response accepted")
			}
			if r.Collections[0].Candidates[0].LunaPriority != nil || r.Collections[0].Candidates[1].ID != "b" {
				t.Fatal("failed merge changed local picks")
			}
		})
	}
}
