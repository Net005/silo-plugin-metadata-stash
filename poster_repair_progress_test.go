package main

import (
	"encoding/json"
	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPosterRenewalDrainsPagesAndPersistsItemFailures(t *testing.T) {
	state := posterRepairState{Items: map[string]posterMarker{}}
	rec := provider.RecommendationRecord{ID: "state", Slug: "stash-recommendations-poster-layout-19", LibraryID: "19", Description: provider.RecommendationOwner, SourceConfig: map[string]json.RawMessage{}}
	save := func() { rec.SourceConfig["poster_layout"], _ = json.Marshal(state) }
	save()
	pageReads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"1"`)
		switch r.URL.Path {
		case "/api/v2/libraries":
			json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": "19", "type": "movies", "enabled": true}}})
		case "/api/v2/libraries/19/providers":
			json.NewEncoder(w).Encode(map[string]any{"levels": []map[string]any{{"content_level": "movie", "entries": []map[string]any{{"capability_id": "stash", "provider_slug": "stash", "enabled": true}}}}})
		case "/api/v2/admin/collections":
			json.NewEncoder(w).Encode(map[string]any{"items": []provider.RecommendationRecord{rec}})
		case "/api/v2/admin/collections/state":
			if r.Method == http.MethodPatch {
				var d struct {
					SourceConfig map[string]json.RawMessage `json:"source_config"`
				}
				json.NewDecoder(r.Body).Decode(&d)
				rec.SourceConfig = d.SourceConfig
				json.Unmarshal(d.SourceConfig["poster_layout"], &state)
			}
			json.NewEncoder(w).Encode(rec)
		case "/api/v2/profiles":
			json.NewEncoder(w).Encode(map[string]any{"items": []map[string]string{{"id": "profile"}}})
		case "/api/v2/catalog":
			pageReads++
			if r.URL.Query().Get("cursor") == "" {
				json.NewEncoder(w).Encode(map[string]any{"items": []map[string]string{{"content_id": "bad"}, {"content_id": "safe"}}, "page": map[string]any{"has_more": true, "next_cursor": "page2"}})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"items": []map[string]string{{"content_id": "last"}}, "page": map[string]any{"has_more": false}})
			}
		case "/api/v2/catalog/items/bad":
			http.Error(w, "unavailable", 500)
		case "/api/v2/catalog/items/safe", "/api/v2/catalog/items/last":
			json.NewEncoder(w).Encode(map[string]string{"type": "episode"})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	rs := &runtimeServer{siloBase: srv.URL, siloKey: "key", posterLayout: "contain", posterRepair: true, client: &stashClient{base: srv.URL, key: "key"}, recommendationConfig: recommendationConfig{Profile: "profile"}}
	task := &scheduledTaskServer{runtime: rs}
	result, err := task.repairPosters(t.Context())
	if err != nil || result["status"] != "partial" {
		t.Fatalf("first pass %+v %v", result, err)
	}
	if state.Scanned != 2 || state.Offset != 0 || state.Cursor != "page2" || len(state.Errors) != 1 {
		t.Fatalf("checkpoint %+v", state)
	}
	result, err = task.repairPosters(t.Context())
	if err != nil || result["status"] != "complete" || state.Scanned != 3 || state.Completed.IsZero() {
		t.Fatalf("final pass %+v %+v %v", result, state, err)
	}
	task.repairPosters(t.Context())
	if pageReads != 2 {
		t.Fatal("completed sweep restarted immediately")
	}
}
