package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func TestPlaybackPreviewHTTPDoesNotRequireRecommendations(t *testing.T) {
	rs := &runtimeServer{client: &stashClient{}}
	server := &recommendationServer{runtime: rs}
	out, err := server.Handle(t.Context(), &pluginv1.HandleHTTPRequest{Method: "GET", Path: "/recommendations/playback/preview"})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err = json.Unmarshal(out.Body, &body); err != nil {
		t.Fatal(err)
	}
	if out.StatusCode != 200 || body["status"] != "not_configured" || body["read_only"] != true {
		t.Fatalf("HTTP %d: %s", out.StatusCode, out.Body)
	}
	var manifest struct {
		Routes []struct{ Path, Access string } `json:"http_routes"`
	}
	if err = json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range manifest.Routes {
		if r.Path == "/recommendations/playback/preview" {
			found = true
			if r.Access != "admin" {
				t.Fatal("preview must be admin-only")
			}
		}
	}
	if !found {
		t.Fatal("preview route missing from manifest")
	}
}

func TestPlaybackPreviewCoverageAndFailureBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                                         string
		count                                        int
		failHistory, ambiguous, excluded, unresolved bool
		wantError                                    bool
	}{
		{name: "bounded", count: 101},
		{name: "excluded", count: 1, excluded: true},
		{name: "unresolved", count: 1, unresolved: true},
		{name: "incomplete pagination", count: 1, failHistory: true, wantError: true},
		{name: "ambiguous exact files", count: 1, ambiguous: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("preview wrote to Silo")
				}
				if strings.HasSuffix(r.URL.Path, "/files") {
					_, _ = w.Write([]byte(`{"items":[{"file_path":"/media/example.mp4"}],"page":{"has_more":false}}`))
					return
				}
				if r.URL.Query().Get("cursor") != "" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				items := []map[string]any{}
				for i := 0; i < tc.count; i++ {
					items = append(items, map[string]any{"session_id": fmt.Sprint(i), "media_item_id": "movie:one", "media_type": "movie", "completed": true, "ended_at": "2026-01-01T12:00:00Z"})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "page": map[string]any{"has_more": tc.failHistory, "next_cursor": "next"}})
			}))
			defer silo.Close()
			lookups := 0
			stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				lookups++
				var b struct {
					Query string `json:"query"`
				}
				_ = json.NewDecoder(r.Body).Decode(&b)
				if strings.Contains(b.Query, "mutation") {
					t.Error("preview wrote to Stash")
				}
				if strings.Contains(b.Query, "findScenes") {
					if tc.unresolved {
						_, _ = w.Write([]byte(`{"data":{"findScenes":{"count":0,"scenes":[]}}}`))
						return
					}
					scenes := []map[string]any{{"id": "42", "files": []map[string]any{{"path": "/media/example.mp4"}}}}
					if tc.ambiguous {
						scenes = append(scenes, map[string]any{"id": "43", "files": []map[string]any{{"path": "/media/example.mp4"}}})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"findScenes": map[string]any{"count": len(scenes), "scenes": scenes}}})
				} else {
					_, _ = w.Write([]byte(`{"data":{"findScene":{"id":"42","play_history":[]}}}`))
				}
			}))
			defer stash.Close()
			task := &scheduledTaskServer{runtime: &runtimeServer{client: &stashClient{base: stash.URL, key: "test", excludedScenes: map[string]bool{"42": tc.excluded}}, siloBase: silo.URL, siloKey: "test", playtimeProfile: "owner"}}
			out, err := task.previewCompletedPlays(t.Context())
			if (err != nil) != tc.wantError {
				t.Fatalf("out=%#v err=%v", out, err)
			}
			if tc.failHistory && lookups != 0 {
				t.Error("used incomplete source history")
			}
			if err != nil {
				return
			}
			if tc.count > 100 && (out["status"] != "partial" || out["remaining"] != 1 || out["checked"] != 100 || out["eligible"] != 101) {
				t.Fatalf("coverage %#v", out)
			}
			if tc.excluded && (out["excluded"] != 1 || out["missing"] != 0) {
				t.Fatalf("exclusions %#v", out)
			}
			if tc.unresolved && (out["unresolved"] != 1 || out["missing"] != 0) {
				t.Fatalf("identity %#v", out)
			}
			results := out["results"].([]any)
			if len(results) != out["checked"].(int) {
				t.Fatalf("diagnostic coverage: %d vs %v", len(results), out["checked"])
			}
			counts := map[string]int{}
			for _, result := range results {
				counts[result.(map[string]any)["outcome"].(string)]++
			}
			for _, outcome := range []string{"missing", "already_present", "unresolved", "excluded", "incomplete_history"} {
				if counts[outcome] != out[outcome].(int) {
					t.Fatalf("diagnostic tally %s: %d vs %v", outcome, counts[outcome], out[outcome])
				}
			}
		})
	}
}

func TestPlaybackPreviewIncompleteHistoryIsNotMissing(t *testing.T) {
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("unexpected write")
		}
		if strings.HasSuffix(r.URL.Path, "/files") {
			_, _ = w.Write([]byte(`{"items":[{"file_path":"/media/example.mp4"}],"page":{"has_more":false}}`))
		} else {
			_, _ = w.Write([]byte(`{"items":[{"session_id":"one","media_item_id":"movie:one","media_type":"movie","completed":true,"ended_at":"2026-01-01T12:00:00Z"}],"page":{"has_more":false}}`))
		}
	}))
	defer silo.Close()
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if strings.Contains(b.Query, "mutation") {
			t.Error("unexpected write")
		}
		if strings.Contains(b.Query, "findScenes") {
			_, _ = w.Write([]byte(`{"data":{"findScenes":{"count":1,"scenes":[{"id":"42","files":[{"path":"/media/example.mp4"}]}]}}}`))
		} else {
			_, _ = w.Write([]byte(`{"data":{"findScene":{"id":"42","play_count":2,"play_history":[]}}}`))
		}
	}))
	defer stash.Close()
	task := &scheduledTaskServer{runtime: &runtimeServer{client: &stashClient{base: stash.URL, key: "test"}, siloBase: silo.URL, siloKey: "test", playtimeProfile: "owner"}}
	out, err := task.previewCompletedPlays(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if out["missing"] != 0 || out["incomplete_history"] != 1 {
		t.Fatalf("summary: %#v", out)
	}
	if out["results"].([]any)[0].(map[string]any)["outcome"] != "incomplete_history" {
		t.Fatalf("diagnostic: %#v", out["results"])
	}
}

func TestPlaybackPreviewTaskReadsOnlyAndDeduplicatesSessions(t *testing.T) {
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("preview mutated Silo: %s", r.Method)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/playback-history"):
			if r.URL.Query().Get("profile_id") != "owner" || r.URL.Query().Get("completed") != "true" {
				t.Error("history must be profile scoped and completed only")
			}
			_, _ = w.Write([]byte(`{"items":[{"session_id":"one","media_item_id":"movie:one","media_type":"movie","completed":true,"ended_at":"2026-01-01T12:00:00Z"},{"session_id":"two","media_item_id":"movie:one","media_type":"movie","completed":true,"ended_at":"2026-01-02T12:00:00Z"},{"session_id":"two","media_item_id":"movie:one","media_type":"movie","completed":true,"ended_at":"2026-01-02T12:00:00Z"}],"page":{"has_more":false}}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			_, _ = w.Write([]byte(`{"items":[{"file_path":"/media/example.mp4"}],"page":{"has_more":false}}`))
		default:
			t.Errorf("unexpected Silo lookup %s", r.URL.Path)
		}
	}))
	defer silo.Close()
	historyReads := 0
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.Contains(body.Query, "mutation") {
			t.Error("preview mutated Stash")
		}
		if strings.Contains(body.Query, "findScenes") {
			_, _ = w.Write([]byte(`{"data":{"findScenes":{"count":1,"scenes":[{"id":"42","files":[{"path":"/media/example.mp4"}]}]}}}`))
		} else {
			historyReads++
			_, _ = w.Write([]byte(`{"data":{"findScene":{"id":"42","play_history":["2026-01-01T12:00:00Z"]}}}`))
		}
	}))
	defer stash.Close()
	task := &scheduledTaskServer{runtime: &runtimeServer{client: &stashClient{base: stash.URL, key: "test"}, siloBase: silo.URL, siloKey: "test", playtimeProfile: "owner"}}
	out, err := task.Run(t.Context(), &pluginv1.RunScheduledTaskRequest{TaskKey: "stash.metadata:play-backfill-preview"})
	if err != nil {
		t.Fatal(err)
	}
	got := out.Output.AsMap()
	if got["status"] != "complete" || got["checked"] != float64(2) || got["missing"] != float64(1) || got["already_present"] != float64(1) || got["duplicates"] != float64(1) || got["read_only"] != true {
		t.Fatalf("preview summary: %#v", got)
	}
	results, ok := got["results"].([]any)
	if !ok || len(results) != 2 {
		t.Fatalf("expected one diagnostic per checked session: %#v", got["results"])
	}
	for i, want := range []struct{ session, ended, outcome string }{
		{"two", "2026-01-02T12:00:00Z", "missing"},
		{"one", "2026-01-01T12:00:00Z", "already_present"},
	} {
		row := results[i].(map[string]any)
		if row["session_id"] != want.session || row["media_item_id"] != "movie:one" || row["ended_at"] != want.ended || row["stash_scene_id"] != "42" || row["outcome"] != want.outcome {
			t.Fatalf("diagnostic %d: %#v", i, row)
		}
		if _, exists := row["file_paths"]; exists {
			t.Fatal("diagnostics should not disclose file paths")
		}
	}
	if historyReads != 1 {
		t.Fatalf("expected one fresh scene history per preview, got %d", historyReads)
	}
	if _, err := task.previewCompletedPlays(t.Context()); err != nil {
		t.Fatal(err)
	}
	if historyReads != 2 {
		t.Fatalf("a new preview must fetch fresh history, got %d total reads", historyReads)
	}
}

func TestPlaybackPreviewReportsMeasuredWatchTime(t *testing.T) {
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("unexpected write")
		}
		if strings.HasSuffix(r.URL.Path, "/files") {
			_, _ = w.Write([]byte(`{"items":[{"file_path":"/media/example.mp4"}],"page":{"has_more":false}}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[` +
			`{"session_id":"a","media_item_id":"movie:one","media_type":"movie","completed":true,"ended_at":"2026-01-03T12:00:00Z","played_seconds":600},` +
			`{"session_id":"a","media_item_id":"movie:one","media_type":"movie","completed":true,"ended_at":"2026-01-03T12:00:00Z","played_seconds":1800.5},` +
			`{"session_id":"b","media_item_id":"movie:one","media_type":"movie","completed":true,"ended_at":"2026-01-02T12:00:00Z","played_seconds":null},` +
			`{"session_id":"c","media_item_id":"movie:one","media_type":"movie","completed":true,"ended_at":"2026-01-01T12:00:00Z","played_seconds":90}` +
			`],"page":{"has_more":false}}`))
	}))
	defer silo.Close()
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if strings.Contains(b.Query, "mutation") {
			t.Error("unexpected write")
		}
		if strings.Contains(b.Query, "findScenes") {
			_, _ = w.Write([]byte(`{"data":{"findScenes":{"count":1,"scenes":[{"id":"42","files":[{"path":"/media/example.mp4"}]}]}}}`))
		} else {
			_, _ = w.Write([]byte(`{"data":{"findScene":{"id":"42","play_count":1,"play_history":["2026-01-01T12:00:00Z"]}}}`))
		}
	}))
	defer stash.Close()
	task := &scheduledTaskServer{runtime: &runtimeServer{client: &stashClient{base: stash.URL, key: "test"}, siloBase: silo.URL, siloKey: "test", playtimeProfile: "owner"}}
	out, err := task.previewCompletedPlays(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if out["missing"] != 2 || out["already_present"] != 1 || out["duplicates"] != 1 || out["missing_watch_seconds"] != 1800.5 || out["missing_unmeasured"] != 1 {
		t.Fatalf("summary: %#v", out)
	}
	rows := out["results"].([]any)
	if rows[0].(map[string]any)["played_seconds"] != 1800.5 {
		t.Fatalf("largest duplicate measurement should win: %#v", rows[0])
	}
	if _, ok := rows[1].(map[string]any)["played_seconds"]; ok {
		t.Fatalf("unmeasured session must not report a watch time: %#v", rows[1])
	}
}
