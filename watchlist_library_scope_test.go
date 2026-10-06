package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWatchlistResolveExcludesOrdinaryMovieFilesAndPagesSelectedLibrary(t *testing.T) {
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "second" {
			fmt.Fprint(w, `{"items":[{"library_id":"21","file_path":"/stash/scene.mp4"},{"library_id":"19","file_path":"/another/unrelated.mp4"}],"page":{"has_more":false}}`)
		} else {
			fmt.Fprint(w, `{"items":[{"library_id":"3","file_path":"/movies/unrelated.mkv"}],"page":{"has_more":true,"next_cursor":"second"}}`)
		}
	}))
	defer silo.Close()
	calls := 0
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		path := req.Variables["path"].(map[string]any)["value"]
		if path != "/stash/scene.mp4" {
			t.Errorf("queried an unrelated library path: %v", path)
		}
		calls++
		fmt.Fprint(w, `{"data":{"findScenes":{"count":1,"scenes":[{"id":"42167","files":[{"path":"/stash/scene.mp4"}]}]}}}`)
	}))
	defer stash.Close()
	rt := &runtimeServer{siloBase: silo.URL, siloKey: "key", client: &stashClient{base: stash.URL, key: "key"}}
	id, err := rt.resolveWatchlistScene(t.Context(), "movie-shared", "21")
	if err != nil || id != "42167" || calls != 1 {
		t.Fatalf("id=%s calls=%d err=%v", id, calls, err)
	}
	id, err = rt.resolveWatchlistScene(t.Context(), "movie-shared", "16")
	if err == nil || id != "" || calls != 1 {
		t.Fatal("missing selected-library file guessed a Stash identity")
	}
}
