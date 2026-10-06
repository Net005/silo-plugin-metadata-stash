package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	rec "github.com/Net005/silo-plugin-metadata-stash/internal/recommendations"
)

func TestWeeklyPeriodAcrossSundayAndDST(t *testing.T) {
	c := defaultRecommendationConfig()
	loc, _ := time.LoadLocation(c.Zone)
	checks := []struct{ at, want string }{{"2026-10-05T12:00:00", "2026-10-04"}, {"2026-10-11T03:29:00", "2026-10-04"}, {"2026-10-11T03:30:00", "2026-10-11"}, {"2026-10-25T03:30:00", "2026-10-25"}}
	for _, x := range checks {
		at, _ := time.ParseInLocation("2006-01-02T15:04:05", x.at, loc)
		if got := recommendationPeriod(at, c); got != x.want {
			t.Fatalf("%s: got %s want %s", x.at, got, x.want)
		}
	}
}

func TestRecommendationSettingsOverridesAndSecrets(t *testing.T) {
	cfg, err := parseRecommendationConfig(map[string]any{"enabled": true, "owner_profile_id": "owner", "defaults_json": `{"count":50,"high_rating":85}`, "libraries_json": `{"18":{"enabled":false},"19":{"collections":["top-rated","revisit"]}}`}, recommendationConfig{APIKey: "saved"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "saved" || cfg.Options.Count != 50 || cfg.Options.High != 85 {
		t.Fatal("settings lost")
	}
	o, err := cfg.libraryOptions("18")
	if err != nil || o.Enabled {
		t.Fatal("library disable ignored")
	}
	o, _ = cfg.libraryOptions("19")
	if len(o.Kinds) != 2 || o.Count != 50 {
		t.Fatal("override did not inherit defaults")
	}
	if _, err = parseRecommendationConfig(map[string]any{"defaults_json": `{"count":0}`}, cfg); err == nil {
		t.Fatal("invalid count silently accepted")
	}
}
func TestRecommendationMappingRequiresExactNativeIdentity(t *testing.T) {
	scenes := []rec.Scene{{ID: "1", Title: "Same", Files: []rec.File{{Path: "/collections/a.mp4"}}}, {ID: "2", Title: "Same", Files: []rec.File{{Path: "/collections/b.mp4"}}}}
	catalog := []provider.CatalogItem{{ContentID: localRecommendationID("/collections/a.mp4"), Type: "movie", Title: "Different title"}, {ContentID: "movie-tmdb-123", Type: "movie", Title: "Same"}}
	rows, _, _ := matchRecommendationScenes(scenes, catalog, "16")
	if len(rows) != 1 || rows[0].ID != "1" {
		t.Fatal(rows)
	}
	scenes = append(scenes, rec.Scene{ID: "3", Files: []rec.File{{Path: "/collections/a.mp4"}}})
	rows, _, _ = matchRecommendationScenes(scenes, catalog, "16")
	if len(rows) != 0 {
		t.Fatal("ambiguous path accepted")
	}
}
func TestBudgetAndExposureSurviveStateRoundTrip(t *testing.T) {
	s := emptyRecommendationState()
	s.Spend["2026-10"] = 1.99
	s.Exposure["16"] = map[string]rec.Exposure{"1": {Count: 3, LastWeek: "2026-W40"}}
	b, _ := json.Marshal(s)
	var decoded recommendationState
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Spend["2026-10"] != 1.99 || decoded.Exposure["16"]["1"].Count != 3 {
		t.Fatal("durable accounting lost")
	}
}

func TestCompressedStateFitsSiloBodyAndReadsLegacy(t *testing.T) {
	s := emptyRecommendationState()
	s.Spend["2026-10"] = 0.25
	for i := 0; i < 5; i++ {
		lib := rec.LibraryReport{LibraryID: fmt.Sprint(i)}
		for j := 0; j < 9; j++ {
			c := rec.Collection{Kind: fmt.Sprint(j)}
			for k := 0; k < 100; k++ {
				c.Candidates = append(c.Candidates, rec.Pick{ID: fmt.Sprint(k), Title: strings.Repeat("scene ", 20), Reasons: []string{"Related metadata has positive activity"}})
			}
			lib.Collections = append(lib.Collections, c)
		}
		s.Report.Libraries = append(s.Report.Libraries, lib)
	}
	b, err := encodeRecommendationState(s)
	if err != nil || len(b) > 1<<20 {
		t.Fatal("state exceeds host limit", len(b), err)
	}
	decoded, err := decodeRecommendationState(b)
	if err != nil || decoded.Spend["2026-10"] != 0.25 || len(decoded.Report.Libraries) != 5 {
		t.Fatal("round trip failed", err)
	}
	b, _ = json.Marshal(s)
	if _, err = decodeRecommendationState(b); err != nil {
		t.Fatal("legacy state failed", err)
	}
}

func TestInterruptedWeeklyRunRecoversOnlyAfterLeaseExpiry(t *testing.T) {
	cfg := defaultRecommendationConfig()
	now := time.Now()
	s := emptyRecommendationState()
	s.Report.Week = recommendationPeriod(now, cfg)
	s.Report.Status = "running"
	s.Report.Fingerprint = recommendationFingerprint(cfg)
	s.LeaseUntil = now.Add(time.Minute)
	if recommendationDue(s, cfg, now) {
		t.Fatal("active reservation was ignored")
	}
	s.LeaseUntil = now.Add(-time.Minute)
	if !recommendationDue(s, cfg, now) {
		t.Fatal("interrupted weekly run never recovered")
	}
	s.LastWeek = s.Report.Week
	s.Report.Status = "complete"
	s.LastFingerprint = recommendationFingerprint(cfg)
	if recommendationDue(s, cfg, now) {
		t.Fatal("published week reran automatically")
	}
}

func TestLegacyCountMigratesAndUpdatedConfigurationRebuildsThisWeek(t *testing.T) {
	cfg, err := parseRecommendationConfig(map[string]any{"defaults_json": `{"count":250}`, "libraries_json": `{"18":{"count":250},"19":{"count":50},"20":{"count":250,"keep_legacy_250_count":true}}`}, recommendationConfig{})
	if err != nil || cfg.Options.Count != 500 {
		t.Fatalf("defaults=%d err=%v", cfg.Options.Count, err)
	}
	for id, want := range map[string]int{"18": 500, "19": 50, "20": 250} {
		o, err := cfg.libraryOptions(id)
		if err != nil || o.Count != want {
			t.Fatalf("library %s count=%d err=%v", id, o.Count, err)
		}
	}
	now := time.Now()
	s := emptyRecommendationState()
	s.LastWeek = recommendationPeriod(now, cfg)
	if recommendationCurrent(s, cfg, now) || !recommendationDue(s, cfg, now) {
		t.Fatal("old completed week prevented upgraded rebuild")
	}
	s.LastFingerprint = recommendationFingerprint(cfg)
	s.Report.Fingerprint = s.LastFingerprint
	s.Report.Status = "complete"
	if !recommendationCurrent(s, cfg, now) || recommendationDue(s, cfg, now) {
		t.Fatal("current build reruns without changes")
	}
	cfg.Options.High++
	if recommendationCurrent(s, cfg, now) || !recommendationDue(s, cfg, now) {
		t.Fatal("changed settings prevented rebuild")
	}
	s.LeaseUntil = now.Add(time.Minute)
	if recommendationDue(s, cfg, now) {
		t.Fatal("upgrade bypassed active worker reservation")
	}
	s.LeaseUntil = now.Add(-time.Minute)
	s.Report.Week = s.LastWeek
	s.Report.Fingerprint = recommendationFingerprint(cfg)
	s.Report.Status = "failed"
	if recommendationDue(s, cfg, now) {
		t.Fatal("failed generation retried each minute")
	}
}

func TestStateStorageKeepsPicksAndPrioritiesWithoutMutatingShortlists(t *testing.T) {
	priority := 91.0
	s := emptyRecommendationState()
	original := rec.Pick{ID: "scene", MediaID: "media", Title: "Full selected title", Reasons: []string{"Positive feedback"}, LunaPriority: &priority}
	s.Report.Libraries = []rec.LibraryReport{{LibraryID: "16", Collections: []rec.Collection{{Kind: "for-you", Candidates: []rec.Pick{original, {ID: "unranked", Title: "Transient"}}, Picks: []rec.Pick{original}}}}}
	raw, err := encodeRecommendationState(s)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeRecommendationState(raw)
	if err != nil {
		t.Fatal(err)
	}
	c := decoded.Report.Libraries[0].Collections[0]
	if len(c.Candidates) != 1 || c.Candidates[0].ID != "scene" || c.Candidates[0].MediaID != "media" || *c.Candidates[0].LunaPriority != 91 {
		t.Fatal("reuse priority lost")
	}
	if c.Picks[0].Title != original.Title || len(c.Picks[0].Reasons) != 1 {
		t.Fatal("selected explanation lost")
	}
	if len(s.Report.Libraries[0].Collections[0].Candidates) != 2 || s.Report.Libraries[0].Collections[0].Candidates[0].Title != original.Title {
		t.Fatal("live shortlist mutated")
	}
}

func TestInternedRecommendationStatePreservesFullLargeReport(t *testing.T) {
	s := emptyRecommendationState()
	s.Spend["2026-10"] = 0.45
	for i := 0; i < 5; i++ {
		lib := rec.LibraryReport{LibraryID: fmt.Sprint(i)}
		for j := 0; j < 16; j++ {
			c := rec.Collection{Kind: fmt.Sprint(j)}
			for k := 0; k < 500; k++ {
				v := float64(k % 100)
				p := rec.Pick{ID: fmt.Sprint(k), MediaID: fmt.Sprintf("local-%032x", k*732743), Title: strings.Repeat(fmt.Sprintf("Scene %d ", k), 10), LunaPriority: &v, Reasons: []string{"Related metadata has positive activity", "Favourited performer"}}
				c.Picks = append(c.Picks, p)
				c.Candidates = append(c.Candidates, p)
			}
			lib.Collections = append(lib.Collections, c)
		}
		s.Report.Libraries = append(s.Report.Libraries, lib)
	}
	b, e := encodeRecommendationState(s)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("Large full report saved in %d bytes", len(b))
	if len(b) > 900000 {
		t.Fatal("report exceeds safe state size", len(b))
	}
	decoded, e := decodeRecommendationState(b)
	if e != nil {
		t.Fatal(e)
	}
	if decoded.Spend["2026-10"] != 0.45 {
		t.Fatal("accounting lost")
	}
	for i, l := range decoded.Report.Libraries {
		for j, c := range l.Collections {
			if len(c.Picks) != 500 || len(c.Candidates) != 500 {
				t.Fatal("picks or priorities lost")
			}
			for k, p := range c.Picks {
				want := s.Report.Libraries[i].Collections[j].Picks[k]
				if p.Title != want.Title || p.MediaID != want.MediaID || *p.LunaPriority != *want.LunaPriority || len(p.Reasons) != 2 {
					t.Fatal("report data changed")
				}
			}
		}
	}
	if _, e := expandRecommendationStrings([]byte(`{"strings":[],"state":{"a":{"$s":0}}}`)); e == nil {
		t.Fatal("invalid reference accepted")
	}
}
