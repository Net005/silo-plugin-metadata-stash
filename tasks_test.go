package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMatchTaskAppliesOnlyUniqueExactScenes(t *testing.T) {
	applied := []map[string]any{}
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer silo-key" {
			t.Error("missing Silo key")
		}
		if strings.Contains(r.URL.Path, "unmatched-items") {
			_, _ = w.Write([]byte(`{"items":[{"content_id":"movie:one","content_type":"movie","library_id":"16","title":"ATID-705"},{"content_id":"movie:two","content_type":"movie","library_id":"16","title":"PRED-901"}],"page":{"has_more":false}}`))
			return
		}
		if strings.Contains(r.URL.Path, "/match/apply") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			applied = append(applied, body)
			w.WriteHeader(200)
			return
		}
		t.Errorf("unexpected Silo path %s", r.URL.Path)
	}))
	defer silo.Close()
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Filter struct {
					Q string `json:"q"`
				} `json:"filter"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Variables.Filter.Q == "ATID-705" {
			_, _ = w.Write([]byte(`{"data":{"findScenes":{"scenes":[{"id":"42","code":"ATID705"}]}}}`))
		} else {
			_, _ = w.Write([]byte(`{"data":{"findScenes":{"scenes":[{"id":"43","code":"PRED901"},{"id":"44","code":"PRED-901"}]}}}`))
		}
	}))
	defer stash.Close()
	rs := &runtimeServer{client: &stashClient{base: stash.URL, key: "stash-key"}, siloBase: silo.URL, siloKey: "silo-key"}
	task := &scheduledTaskServer{runtime: rs}
	summary, err := task.match(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary["matched"] != 1 || summary["skipped"] != 1 {
		t.Fatalf("summary %#v", summary)
	}
	if len(applied) != 1 || applied[0]["provider_ids"].(map[string]any)["stash"] != "42" {
		t.Fatalf("applied %#v", applied)
	}
}
