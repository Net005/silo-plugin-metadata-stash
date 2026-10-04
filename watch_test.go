package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
