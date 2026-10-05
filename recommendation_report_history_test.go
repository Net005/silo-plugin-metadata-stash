package main

import (
	"encoding/json"
	"fmt"
	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	rec "github.com/Net005/silo-plugin-metadata-stash/internal/recommendations"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReportArchivesPreservePicksAndPruneSixCalendarMonths(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	owner := "owner"
	prefix := provider.RecommendationSlug(owner, "reports", "")
	rows := map[string]provider.RecommendationRecord{}
	for _, entry := range []struct {
		id   string
		date time.Time
	}{{"expired", now.AddDate(0, -6, -1)}, {"boundary", now.AddDate(0, -6, 0)}} {
		meta, _ := json.Marshal(recommendationReportIndex{Started: entry.date})
		rows[entry.id] = provider.RecommendationRecord{ID: entry.id, LibraryID: "16", Slug: prefix + entry.id, Description: provider.RecommendationOwner, SourceConfig: map[string]json.RawMessage{reportArchiveKey: meta}}
	}
	rows["unrelated"] = provider.RecommendationRecord{ID: "unrelated", Slug: provider.RecommendationSlug(owner, "16", "for-you"), Description: provider.RecommendationOwner}
	created := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"1"`)
		if r.URL.Path == "/api/v2/admin/collections" {
			if r.Method == "GET" {
				out := []provider.RecommendationRecord{}
				for _, v := range rows {
					out = append(out, v)
				}
				json.NewEncoder(w).Encode(map[string]any{"items": out})
				return
			}
			var v provider.RecommendationRecord
			json.NewDecoder(r.Body).Decode(&v)
			created++
			v.ID = fmt.Sprint(created)
			rows[v.ID] = v
			json.NewEncoder(w).Encode(v)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/v2/admin/collections/")
		v, ok := rows[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(v)
		case "DELETE":
			delete(rows, id)
		case "PATCH":
			var payload struct {
				SourceConfig map[string]json.RawMessage `json:"source_config"`
			}
			json.NewDecoder(r.Body).Decode(&payload)
			v.SourceConfig = payload.SourceConfig
			rows[id] = v
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()
	c := provider.NewSiloClient(server.URL, "key")
	report := recommendationReport{Started: now.Add(-time.Hour), Finished: now, Status: "complete", Week: "2026-40", Libraries: []rec.LibraryReport{{LibraryID: "16", Collections: []rec.Collection{{Picks: []rec.Pick{{ID: "42", MediaID: "local-42", Title: "Saved scene", Reasons: []string{"High rating"}}}}}}}}
	for i := 0; i < 2; i++ {
		if err := archiveRecommendationReport(t.Context(), c, owner, "16", report, now); err != nil {
			t.Fatal(err)
		}
	}
	if created != 1 {
		t.Fatalf("duplicate archive created: %d", created)
	}
	if _, ok := rows["expired"]; ok {
		t.Fatal("expired archive remains")
	}
	if _, ok := rows["boundary"]; !ok {
		t.Fatal("retention boundary removed")
	}
	if _, ok := rows["unrelated"]; !ok {
		t.Fatal("live recommendation collection removed")
	}
	got, err := readRecommendationArchivedReport(t.Context(), c, owner, "1", now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Libraries[0].Collections[0].Picks[0].Reasons[0] != "High rating" {
		t.Fatal("saved selections lost")
	}
	if _, err := readRecommendationArchivedReport(t.Context(), c, "different-owner", "1", now); err == nil {
		t.Fatal("another owner's archive accessible")
	}
	history, err := recommendationReportHistory(t.Context(), c, owner, now)
	if err != nil || len(history) != 2 || history[0].ID != "1" {
		t.Fatalf("history: %+v %v", history, err)
	}
}
