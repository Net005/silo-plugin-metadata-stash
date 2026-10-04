package legacyprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDirectStashSavedFiltersPaginationConversionAndCache(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/graphql" || r.Header.Get("ApiKey") == "" {
			t.Error("missing Stash GraphQL connection")
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Variables == nil {
			w.Write([]byte(`{"data":{"findSavedFilters":[{"id":"20","name":"Favorite performers","object_filter":{"performer_favorite":{"value":"true"}}}]}}`))
			return
		}
		if body.Variables["sceneFilter"].(map[string]any)["performer_favorite"] != true {
			t.Error("favorite boolean was not normalized")
		}
		page := int(body.Variables["findFilter"].(map[string]any)["page"].(float64))
		rows := []map[string]any{}
		count := 200
		if page == 2 {
			count = 1
		}
		for i := 0; i < count; i++ {
			id := fmt.Sprint((page-1)*200 + i)
			rows = append(rows, map[string]any{"id": id, "code": "code-" + id, "title": "title-" + id, "files": []map[string]string{{"path": "/scenes/" + id + ".mp4"}}})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"findScenes": map[string]any{"count": 201, "scenes": rows}}})
	}))
	defer server.Close()
	p := NewProvider()
	p.ConfigureStashConnection(server.URL, "stash-key")
	for i := 0; i < 2; i++ {
		rows, err := p.StashSavedFilters(context.Background(), "20")
		if err != nil || len(rows) != 1 || len(rows[0].Items) != 201 {
			t.Fatalf("rows=%v err=%v", rows, err)
		}
	}
	if calls != 3 {
		t.Fatalf("cached export made extra requests: %d", calls)
	}
	p.ConfigureStashConnection(server.URL, "changed-key")
	if _, err := p.StashSavedFilters(context.Background(), "20"); err != nil {
		t.Fatal(err)
	}
	if calls != 6 {
		t.Fatalf("connection change did not invalidate cache: %d", calls)
	}
	if _, err := p.StashSavedFilters(context.Background(), "missing"); err == nil {
		t.Fatal("unknown selection must fail before reconciliation")
	}
}
