package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	legacy "github.com/Net005/silo-plugin-metadata-stash/internal/legacytasks"
	"github.com/hashicorp/go-hclog"
)

func TestWatchlistHealthEscalatesOnceAndResets(t *testing.T) {
	var h watchlistHealth
	now := time.Now()
	escalations := 0
	for i := 0; i < watchlistStuckAfter+watchlistRepeatEvery; i++ {
		if h.record(errors.New(`Post "https://silo.example.com/x": HTTP 412`), now.Add(time.Duration(i)*5*time.Second)) {
			escalations++
		}
	}
	if escalations != 2 {
		t.Fatalf("expected escalation when stuck and once more after the repeat interval, got %d", escalations)
	}
	snap := h.snapshot(now.Add(time.Hour))
	if snap["stuck"] != true || snap["consecutive_failures"] != watchlistStuckAfter+watchlistRepeatEvery {
		t.Fatalf("snapshot: %#v", snap)
	}
	if msg := snap["last_error"].(string); strings.Contains(msg, "silo.example.com") || !strings.Contains(msg, "<url>") {
		t.Fatalf("error text must not expose addresses: %q", msg)
	}
	h.record(nil, now)
	snap = h.snapshot(now)
	if snap["stuck"] != false || snap["consecutive_failures"] != 0 || snap["last_error"] != nil {
		t.Fatalf("success must clear the streak: %#v", snap)
	}
}

func TestWatchlistHealthTruncatesLongErrors(t *testing.T) {
	var h watchlistHealth
	h.record(errors.New(strings.Repeat("x", 1000)), time.Now())
	if got := len(h.snapshot(time.Now())["last_error"].(string)); got != watchlistErrorMaxSize {
		t.Fatalf("length %d", got)
	}
}

func TestWatchlistStatusIsReadOnlyAndCountsQueues(t *testing.T) {
	s := newWatchlistStatusFixture(t)
	out := s.watchlistStatus(t.Context())
	libs, _ := out["libraries"].([]any)
	if out["read_only"] != true || len(libs) != 1 {
		t.Fatalf("status: %#v", out)
	}
	lib := libs[0].(map[string]any)
	if lib["pending"] != 1 || lib["incoming_from_stash"] != 1 || lib["parked_unavailable"] != 1 || lib["library_id"] != "16" {
		t.Fatalf("queue counts: %#v", lib)
	}
	if out["status"] != "attention" {
		t.Fatalf("an old queued change must need attention: %#v", out)
	}
}

func newWatchlistStatusFixture(t *testing.T) *runtimeServer {
	t.Helper()
	old := time.Now().Add(-time.Hour).UTC()
	journal := watchlistJournal{Version: 1, Baseline: map[string]bool{"a": true, "b": true},
		Pending:  map[string]watchlistIntent{"a": {Desired: true, Changed: old}},
		Incoming: map[string]watchlistIntent{"b": {Desired: false, Changed: old}},
		Inactive: map[string]watchlistIntent{"c": {Desired: true, Changed: old}}}
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("status route wrote to Silo: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("ETag", `"1"`)
		raw, _ := json.Marshal(journal)
		row := watchlistCollection{ID: "wl", LibraryID: "16", Title: "Stash | Watchlist", Slug: "javbeacon-stash-preset-7-library-16", Description: "Managed by JAVBeacon metadata plugin.", SourceConfig: map[string]json.RawMessage{watchlistJournalKey: raw}}
		switch r.URL.Path {
		case "/api/v2/admin/collections":
			json.NewEncoder(w).Encode(map[string]any{"items": []watchlistCollection{row}})
		case "/api/v2/admin/collections/wl":
			json.NewEncoder(w).Encode(row)
		default:
			t.Errorf("unexpected Silo request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(silo.Close)
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if strings.Contains(req.Query, "mutation") {
			t.Error("status route wrote to Stash")
		}
		fmt.Fprint(w, `{"data":{"findSavedFilters":[{"id":"7","name":"Watchlist","object_filter":{}}]}}`)
	}))
	t.Cleanup(stash.Close)
	manager := legacy.New(hclog.NewNullLogger())
	manager.Provider().ConfigureStashConnection(stash.URL, "key")
	return &runtimeServer{siloBase: silo.URL, siloKey: "key", stashFilters: "Watchlist", stashPrefix: "Stash | ", client: &stashClient{base: stash.URL, key: "key"}, legacy: manager}
}
