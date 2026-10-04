package main

import (
	"context"
	"encoding/json"
	legacytasks "github.com/Net005/silo-plugin-metadata-stash/internal/legacytasks"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func TestPersonalWatchlistEventsAreIgnored(t *testing.T) {
	server := &watchSyncServer{runtime: &runtimeServer{client: &stashClient{base: "http://stash.invalid", key: "key"}}}
	for _, op := range []pluginv1.WatchSyncOperation{
		pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST,
		pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FROM_WATCHLIST,
	} {
		got := server.applyOne(context.Background(), &pluginv1.WatchSyncEvent{EventId: "watchlist", Operation: op, ProviderItemKey: "stash:42"})
		if got.GetStatus() != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_NO_CHANGE {
			t.Fatalf("operation %s: status=%s", op, got.GetStatus())
		}
	}
}

func TestPersonalWatchlistImportIsNotAuthoritative(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		t.Error("unexpected Stash query")
	}))
	defer backend.Close()
	server := &watchSyncServer{runtime: &runtimeServer{client: &stashClient{base: backend.URL, key: "key"}}}
	got, err := server.ListRemoteState(context.Background(), &pluginv1.WatchSyncListRemoteStateRequest{StateKinds: []pluginv1.WatchSyncRemoteStateKind{pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHLIST}})
	if err != nil || got.GetCompleteSnapshot() || len(got.GetItems()) != 0 || calls != 0 {
		t.Fatalf("watchlist import: result=%v err=%v calls=%d", got, err, calls)
	}
}

func TestPlaybackUsesJAVBeaconEngineWhenConfigured(t *testing.T) {
	var got struct {
		Event        string `json:"event"`
		SessionID    string `json:"session_id"`
		StashSceneID string `json:"stash_scene_id"`
		IsPlayed     bool   `json:"is_played"`
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/integrations/silo/playback" || r.Header.Get("Authorization") != "Bearer backend-key" {
			t.Errorf("backend request %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"session_id":"session-1","play_counted":true}`))
	}))
	defer backend.Close()
	stashCalls := 0
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stashCalls++
		t.Error("direct Stash write bypassed playback engine")
	}))
	defer stash.Close()
	manager := legacytasks.New(nil)
	manager.Configure(legacytasks.Config{JAVBeaconURL: backend.URL, JAVBeaconKey: "backend-key"})
	server := &watchSyncServer{runtime: &runtimeServer{client: &stashClient{base: stash.URL, key: "stash-key"}, legacy: manager}}
	result := server.applyOne(context.Background(), &pluginv1.WatchSyncEvent{EventId: "event-1", PlaybackSessionId: "session-1", ProviderItemKey: "stash:42", Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP, Completed: true})
	if result.GetStatus() != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED || got.Event != "stop" || got.SessionID != "session-1" || got.StashSceneID != "42" || !got.IsPlayed || stashCalls != 0 {
		t.Fatalf("result=%v got=%+v stashCalls=%d", result, got, stashCalls)
	}
}

func TestBackendOnlyReleasePlaybackDoesNotInventStashScene(t *testing.T) {
	var got struct {
		ReleaseID    int64  `json:"release_id"`
		StashSceneID string `json:"stash_scene_id"`
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"session_id":"session-legacy"}`))
	}))
	defer backend.Close()
	manager := legacytasks.New(nil)
	manager.Configure(legacytasks.Config{JAVBeaconURL: backend.URL, JAVBeaconKey: "key"})
	server := &watchSyncServer{runtime: &runtimeServer{legacy: manager}}
	result := server.applyOne(context.Background(), &pluginv1.WatchSyncEvent{EventId: "legacy-event", ProviderItemKey: "javbeacon:7", Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED})
	if result.GetStatus() != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED || got.ReleaseID != 7 || got.StashSceneID != "" {
		t.Fatalf("result=%v payload=%+v", result, got)
	}
}

func TestCompletedPlaybackAlreadyInStashSkipsBackend(t *testing.T) {
	at := time.Date(2026, 10, 4, 10, 15, 0, 0, time.UTC)
	backendCalls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		t.Error("duplicate play sent to JAVBeacon")
	}))
	defer backend.Close()
	stashCalls := 0
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stashCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"findScene": map[string]any{"id": "42", "play_history": []string{at.Format(time.RFC3339Nano)}}}})
	}))
	defer stash.Close()
	manager := legacytasks.New(nil)
	manager.Configure(legacytasks.Config{JAVBeaconURL: backend.URL, JAVBeaconKey: "key"})
	server := &watchSyncServer{runtime: &runtimeServer{client: &stashClient{base: stash.URL, key: "key"}, legacy: manager}}
	result := server.applyOne(context.Background(), &pluginv1.WatchSyncEvent{EventId: "event-1", PlaybackSessionId: "session-1", ProviderItemKey: "stash:42", Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP, Completed: true, OccurredAt: timestamppb.New(at)})
	if result.GetStatus() != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_NO_CHANGE || backendCalls != 0 || stashCalls != 1 {
		t.Fatalf("result=%v backendCalls=%d stashCalls=%d", result, backendCalls, stashCalls)
	}
}
