package legacyprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInboundWatchlistDoesNotUndoLocalRemoval(t *testing.T) {
	writes := 0
	config := map[string]json.RawMessage{"stash_watchlist_outbox": json.RawMessage(`{"version":1,"baseline":{"scene":true},"pending":{},"last_actions":{"keep":{"desired":true}}}`)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
		}
		w.Header().Set("ETag", `"1"`)
		json.NewEncoder(w).Encode(siloCollection{ID: "watch", SourceConfig: config})
	}))
	defer server.Close()
	c := NewSiloClient(server.URL, "key")
	err := c.guardedCollectionMembership(t.Context(), siloCollection{ID: "watch", SourceConfig: config}, "scene", true, false, 0)
	if err == nil || writes != 0 {
		t.Fatalf("local removal overwritten: %v writes=%d", err, writes)
	}
}
func TestInboundWatchlistUpdatesOnlyAppliedMemberBaseline(t *testing.T) {
	config := map[string]json.RawMessage{"stash_watchlist_outbox": json.RawMessage(`{"version":1,"baseline":{"other":true},"pending":{},"last_actions":{"keep":{"desired":true}}}`)}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"1"`)
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(siloCollection{ID: "watch", SourceConfig: config})
		case "PUT":
			writes++
		case "PATCH":
			if r.Header.Get("If-Match") != `"1"` {
				t.Error("missing CAS")
			}
			var input struct {
				SourceConfig map[string]json.RawMessage `json:"source_config"`
			}
			json.NewDecoder(r.Body).Decode(&input)
			config = input.SourceConfig
		}
	}))
	defer server.Close()
	c := NewSiloClient(server.URL, "key")
	if err := c.guardedCollectionMembership(t.Context(), siloCollection{ID: "watch", SourceConfig: config}, "scene", true, false, 0); err != nil {
		t.Fatal(err)
	}
	var j struct {
		Baseline map[string]bool `json:"baseline"`
		Actions  map[string]any  `json:"last_actions"`
	}
	json.Unmarshal(config["stash_watchlist_outbox"], &j)
	if writes != 1 || !j.Baseline["scene"] || !j.Baseline["other"] || j.Actions["keep"] == nil {
		t.Fatalf("journal lost: %+v", j)
	}
}
