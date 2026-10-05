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
