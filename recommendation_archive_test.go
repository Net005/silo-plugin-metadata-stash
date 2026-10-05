package main

import (
	"encoding/json"
	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	rec "github.com/Net005/silo-plugin-metadata-stash/internal/recommendations"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestArchiveRetainsRemovedReleaseAsFeedbackWithoutDuplicateEvents(t *testing.T) {
	at := "2026-01-01T12:00:00Z"
	a := recommendationArchive{Version: 1, Scenes: []recommendationArchiveScene{
		{ID: "old-live", Title: "old title", Code: "LIVE-001", Plays: 5},
		{ID: "gone-1", Release: 9, Path: "/collections/jav/removed.mp4", Plays: 2, O: 1},
		{ID: "gone-2", Release: 9, Path: "/collections/jav/removed-copy.mp4", Plays: 2, O: 1},
		{ID: "no-metadata", Plays: 1},
	}, Events: []recommendationArchiveEvent{{ID: "old-live", Type: "play", At: at}, {ID: "gone-1", Type: "play", At: at}, {ID: "gone-2", Type: "play", At: at}, {ID: "gone-1", Type: "orgasm", At: at}, {ID: "gone-2", Type: "orgasm", At: at}}}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("auth missing")
		}
		if r.URL.Path == "/api/stash/history/export" {
			json.NewEncoder(w).Encode(a)
		} else if r.URL.Path == "/api/releases/9" {
			calls++
			json.NewEncoder(w).Encode(recommendationRelease{ID: 9, Studio: "studio", Actresses: []string{"alias"}})
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	current := []rec.Scene{{ID: "live", Code: "LIVE-001", Title: "current title", MediaID: "available", Plays: 1, PlayHistory: []string{at}, Studio: &rec.Entity{ID: "s", Name: "Studio"}, Performers: []rec.Entity{{ID: "p", Name: "Name", Aliases: []string{"Alias"}}}}}
	scenes, history, stats, e := mergeRecommendationArchive(t.Context(), &artworkClient{base: server.URL, key: "secret"}, current, []provider.MovieLibrary{{ID: "16", Paths: []string{"/collections/jav"}}})
	if e != nil {
		t.Fatal(e)
	}
	if calls != 1 || len(history) != 1 || history[0].MediaID != "" || history[0].LibraryID != "16" || history[0].O != 1 || history[0].Plays != 2 || len(history[0].PlayHistory) != 1 || len(history[0].OHistory) != 1 {
		t.Fatal(calls, history)
	}
	if scenes[0].Plays != 5 || scenes[0].O != 0 || len(scenes[0].PlayHistory) != 1 || stats.AddedEvents != 0 || stats.Unresolved != 1 || stats.HistoryOnly != 1 {
		t.Fatal(scenes, stats)
	}
	if history[0].Performers[0].ID != "p" {
		t.Fatal("alias not resolved")
	}
	o := rec.DefaultOptions()
	o.Kinds = []string{"for-you"}
	r := rec.Build("16", append(scenes, history...), append(scenes, history...), o, nil, nil, nil, nil, time.Now())
	if r.FeedbackScenes != 2 {
		t.Fatal("removed release absent from local learning", r.FeedbackScenes)
	}
	for _, c := range r.Collections {
		for _, p := range c.Candidates {
			if p.ID == history[0].ID {
				t.Fatal("removed release became candidate")
			}
		}
	}
}
func TestArchiveRejectsAmbiguousNamesAndLibraryRoots(t *testing.T) {
	entities := archiveEntities([]rec.Scene{{Studio: &rec.Entity{ID: "a", Name: "Studio"}}, {Studio: &rec.Entity{ID: "b", Name: "studio"}}})
	if archiveReleaseScene(recommendationRelease{ID: 9, Studio: "Studio"}, entities).Studio != nil {
		t.Fatal("ambiguous identity used")
	}
	libs := []provider.MovieLibrary{{ID: "16", Paths: []string{"/collections/jav"}}}
	if archiveLibrary("/collections/jav-other/scene.mp4", libs) != "" {
		t.Fatal("partial prefix matched")
	}
	libs = append(libs, provider.MovieLibrary{ID: "18", Paths: []string{"/collections"}})
	if archiveLibrary("/collections/jav/scene.mp4", libs) != "" {
		t.Fatal("ambiguous library assigned")
	}
}
