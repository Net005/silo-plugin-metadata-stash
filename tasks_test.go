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
		if strings.HasSuffix(r.URL.Path, "/files") {
			if strings.Contains(r.URL.Path, "movie:one") {
				_, _ = w.Write([]byte(`{"items":[{"file_path":"/library/ATID-705.mp4"}],"page":{"has_more":false}}`))
			} else {
				_, _ = w.Write([]byte(`{"items":[{"file_path":"/library/PRED-901.mp4"}],"page":{"has_more":false}}`))
			}
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

// Stale catalog entries used to abort every pass before valid XXX items.
func TestMatchTaskContinuesPastStaleCatalogItems(t *testing.T) {
	for _, missingAt := range []string{"files", "apply", "rejected"} {
		t.Run(missingAt, func(t *testing.T) {
			applied := []string{}
			silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.Contains(r.URL.Path, "unmatched-items"):
					_, _ = w.Write([]byte(`{"items":[{"content_id":"orphan","content_type":"movie","library_id":"","title":"01"},{"content_id":"removed","content_type":"movie","library_id":"22"},{"content_id":"valid","content_type":"movie","library_id":"22"}],"page":{"has_more":false}}`))
				case strings.Contains(r.URL.Path, "orphan"):
					t.Error("orphan must not be looked up or applied")
				case strings.HasSuffix(r.URL.Path, "/files"):
					if strings.Contains(r.URL.Path, "removed") && missingAt == "files" {
						http.NotFound(w, r)
						return
					}
					_, _ = w.Write([]byte(`{"items":[{"file_path":"/torrent/xxx/file.mp4"}],"page":{"has_more":false}}`))
				case strings.HasSuffix(r.URL.Path, "/match/apply"):
					if strings.Contains(r.URL.Path, "removed") {
						if missingAt == "rejected" {
							w.WriteHeader(http.StatusUnprocessableEntity)
							return
						}
						http.NotFound(w, r)
						return
					}
					var body struct {
						LibraryID   string            `json:"library_id"`
						ProviderIDs map[string]string `json:"provider_ids"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body.LibraryID != "22" || body.ProviderIDs["stash"] != "42" {
						t.Errorf("wrong match: %#v", body)
					}
					applied = append(applied, r.URL.Path)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer silo.Close()
			stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"exactPath":{"scenes":[{"id":"42","title":"Different canonical title","files":[{"path":"/torrent/xxx/file.mp4"}]}]}}}`))
			}))
			defer stash.Close()
			task := &scheduledTaskServer{runtime: &runtimeServer{client: &stashClient{base: stash.URL, key: "stash-key"}, siloBase: silo.URL, siloKey: "silo-key"}}
			summary, err := task.match(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantStale, wantRejected := 2, 0
			if missingAt == "rejected" {
				wantStale, wantRejected = 1, 1
			}
			if summary["matched"] != 1 || summary["stale"] != wantStale || summary["rejected"] != wantRejected || len(applied) != 1 {
				t.Fatalf("summary=%#v applied=%v", summary, applied)
			}
		})
	}
}

func TestMatchTaskDoesNotHideSiloAuthorizationFailure(t *testing.T) {
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "unmatched-items") {
			_, _ = w.Write([]byte(`{"items":[{"content_id":"valid","content_type":"movie","library_id":"22"}],"page":{"has_more":false}}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer silo.Close()
	task := &scheduledTaskServer{runtime: &runtimeServer{client: &stashClient{base: "https://stash.invalid", key: "stash-key"}, siloBase: silo.URL, siloKey: "silo-key"}}
	if _, err := task.match(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("expected auth failure, got %v", err)
	}
}
