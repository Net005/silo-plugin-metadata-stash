package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInboundWatchlistReaddMovesFirstAndRetriesWithoutExport(t *testing.T) {
	ids := []string{"other", "target"}
	now := time.Now().UTC()
	journal := watchlistJournal{Version: 1, Baseline: map[string]bool{"other": true, "target": true}, Pending: map[string]watchlistIntent{}, Incoming: map[string]watchlistIntent{"target": {Desired: false, Changed: now}}}
	failOrder := false
	unavailable := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"1"`)
		switch r.URL.Path {
		case "/api/v2/admin/collections/wl":
			if r.Method == "PATCH" {
				var row watchlistCollection
				json.NewDecoder(r.Body).Decode(&row)
				journal = watchlistJournal{}
				json.Unmarshal(row.SourceConfig[watchlistJournalKey], &journal)
				w.WriteHeader(204)
				return
			}
			raw, _ := json.Marshal(journal)
			json.NewEncoder(w).Encode(watchlistCollection{ID: "wl", SourceConfig: map[string]json.RawMessage{watchlistJournalKey: raw}})
		case "/api/v2/admin/collections/wl/items":
			items := []map[string]string{}
			for _, id := range ids {
				items = append(items, map[string]string{"media_item_id": id})
			}
			json.NewEncoder(w).Encode(map[string]any{"items": items, "page": map[string]bool{"has_more": false}})
		case "/api/v2/admin/collections/wl/items/target":
			if r.Method == "DELETE" {
				next := []string{}
				for _, id := range ids {
					if id != "target" {
						next = append(next, id)
					}
				}
				ids = next
			} else {
				found := false
				for _, id := range ids {
					found = found || id == "target"
				}
				if !found {
					ids = append(ids, "target")
				}
			}
			w.WriteHeader(204)
		case "/api/v2/admin/collections/wl/items/unavailable":
			if unavailable {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			ids = append(ids, "unavailable")
			w.WriteHeader(204)
		case "/api/v2/admin/collections/wl/items/order":
			if r.Method == "GET" {
				json.NewEncoder(w).Encode(map[string]any{"ordered_ids": ids})
				return
			}
			if failOrder {
				w.WriteHeader(503)
				return
			}
			var input struct {
				IDs []string `json:"ordered_ids"`
			}
			json.NewDecoder(r.Body).Decode(&input)
			ids = input.IDs
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	s := &runtimeServer{siloBase: server.URL, siloKey: "key"}
	if err := s.drainInboundWatchlist(context.Background(), "wl"); err != nil {
		t.Fatal(err)
	}
	if journal.Baseline["target"] || len(ids) != 1 {
		t.Fatal("remove not applied")
	}
	journal.Incoming = map[string]watchlistIntent{"target": {Desired: true, Changed: now.Add(time.Second)}}
	failOrder = true
	if err := s.drainInboundWatchlist(context.Background(), "wl"); err == nil {
		t.Fatal("expected failed reorder")
	}
	if len(journal.Incoming) != 1 {
		t.Fatal("failed event lost")
	}
	failOrder = false
	if err := s.drainInboundWatchlist(context.Background(), "wl"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []string{"target", "other"}) {
		t.Fatalf("readd order %v", ids)
	}
	if !journal.Baseline["target"] || len(journal.Pending) != 0 || len(journal.Incoming) != 0 {
		t.Fatalf("incorrect journal %+v", journal)
	}
	// An older delayed remove cannot undo the confirmed re-add.
	journal.Incoming = map[string]watchlistIntent{"target": {Desired: false, Changed: now}}
	if err := s.drainInboundWatchlist(context.Background(), "wl"); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(ids[0], "target") {
		t.Fatal("stale remove applied")
	}
	// A scanned-but-missing file is still returned by Silo's files endpoint,
	// but admission to a library collection returns 404. Its older queued add
	// must not block a subsequent re-add of an available item.
	journal.Incoming = map[string]watchlistIntent{
		"unavailable": {Desired: true, Changed: now.Add(2 * time.Second)},
		"target":      {Desired: true, Changed: now.Add(3 * time.Second)},
	}
	ids = []string{"other", "target"}
	if err := s.drainInboundWatchlist(context.Background(), "wl"); err != nil {
		t.Fatal(err)
	}
	if ids[0] != "target" || len(journal.Incoming) != 1 || !journal.Incoming["unavailable"].Desired {
		t.Fatalf("unavailable item blocked re-add or lost its event: order=%v incoming=%v", ids, journal.Incoming)
	}
	// Recovery of the old event retains the newer re-add's position.
	unavailable = false
	if err := s.drainInboundWatchlist(context.Background(), "wl"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []string{"target", "unavailable", "other"}) || len(journal.Incoming) != 0 {
		t.Fatalf("recovered older event displaced newer add: order=%v incoming=%v", ids, journal.Incoming)
	}
}
