package main

import (
	"context"
	"encoding/json"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativePlaybackResolvesExactFileAndRetriesWithoutDuplicate(t *testing.T) {
	history := []string{"2021-03-16T22:09:42Z"}
	writes := 0
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var data any
		switch {
		case strings.Contains(req.Query, "findScenes"):
			data = map[string]any{"findScenes": map[string]any{"count": 1, "scenes": []any{map[string]any{"id": "39872", "files": []any{map[string]any{"path": "/collections/jav/HTMS-080.mp4"}}}}}}
		case strings.Contains(req.Query, "findScene"):
			data = map[string]any{"findScene": map[string]any{"id": "39872", "play_count": len(history), "play_history": history}}
		case strings.Contains(req.Query, "sceneAddPlay"):
			writes++
			history = append(history, "2026-10-04T19:22:49Z")
			data = map[string]any{"sceneAddPlay": map[string]any{"count": len(history)}}
		case strings.Contains(req.Query, "sceneSaveActivity"):
			data = map[string]any{"sceneSaveActivity": true}
		default:
			t.Errorf("unexpected query %s", req.Query)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer stash.Close()
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/admin/items/local-test/files" {
			t.Errorf("unexpected identity path %s", r.URL.Path)
		}
		w.Write([]byte(`{"items":[{"file_path":"/collections/jav/HTMS-080.mp4"}],"page":{"has_more":false}}`))
	}))
	defer silo.Close()
	c := &stashClient{base: stash.URL, key: "key"}
	s := &watchSyncServer{runtime: &runtimeServer{client: c, siloBase: silo.URL, siloKey: "key"}}
	at, _ := time.Parse(time.RFC3339, "2026-10-04T19:22:49Z")
	e := &pluginv1.WatchSyncEvent{EventId: "completed", ProviderItemKey: "tmdb:123", Media: &pluginv1.WatchSyncMedia{MediaItemId: "local-test"}, Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP, Completed: true, OccurredAt: timestamppb.New(at)}
	for i := 0; i < 2; i++ {
		if got := s.applyOne(context.Background(), e); got.GetStatus() == pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_RETRY {
			t.Fatalf("apply: %v", got)
		}
	}
	if writes != 1 || len(history) != 2 {
		t.Fatalf("writes %d history %v", writes, history)
	}
	c.excludedScenes = map[string]bool{"39872": true}
	e.OccurredAt = timestamppb.New(at.Add(time.Hour))
	if got := s.applyOne(context.Background(), e); got.GetStatus() != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_NO_CHANGE || writes != 1 {
		t.Fatalf("excluded: %v writes %d", got, writes)
	}
}
func TestPlaybackPathAmbiguityIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"findScenes":{"count":2,"scenes":[{"id":"1","files":[{"path":"/one.mp4"}]},{"id":"2","files":[{"path":"/one.mp4"}]}]}}}`))
	}))
	defer server.Close()
	c := &stashClient{base: server.URL, key: "key"}
	if id, err := c.sceneIDForExactPaths(context.Background(), []string{"/one.mp4"}); err == nil || id != "" {
		t.Fatalf("ambiguous result %q %v", id, err)
	}
	for _, id := range []string{"tmdb:123", "imdb:tt1", "javbeacon:7"} {
		if stashPlaybackID(id) != "" {
			t.Fatalf("accepted %s", id)
		}
	}
}
