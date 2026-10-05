package legacyprovider

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecommendationOwnershipCannotTargetSavedFilters(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", "\"1\"")
		w.Write([]byte(`{"id":"1","slug":"javbeacon-stash-preset-3-library-16","description":"Managed by JAVBeacon metadata plugin."}`))
	}))
	defer server.Close()
	c := NewSiloClient(server.URL, "key")
	if err := c.ReconcileRecommendation(t.Context(), RecommendationRecord{ID: "1"}, []string{"local-a"}); err == nil {
		t.Fatal("saved-filter collection ownership accepted")
	}
	if writes != 0 {
		t.Fatal("write before ownership validation")
	}
}
