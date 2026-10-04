package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestArtworkCacheSource(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"https://jav.test/covers/42/stash-poster?api_key=secret", "stash://backend/covers/42/stash-poster"},
		{"https://jav.test/api/v1/integrations/silo/stash/scenes/42/cover?apikey=secret&variant=poster", "stash://backend/api/v1/integrations/silo/stash/scenes/42/cover?variant=poster"},
		{"https://stash.test/scene/42/screenshot?apikey=secret", "stash://scene/42/screenshot"},
		{"stash://backend/covers/42/stash-poster", "stash://backend/covers/42/stash-poster"},
		{"javbeacon://covers/42/original", "stash://backend/covers/42/original"},
		{"https://silo.test/image?url=stash%3A%2F%2Fscene%2F42%2Fscreenshot", "stash://scene/42/screenshot"},
		{"https://s3.test/bucket/stash/movies/42/poster/original.webp", ""},
		{"/api/v2/artwork/stash/movies/42/poster/original.webp", ""},
		{"https://other.test/covers/42/original", ""},
		{"https://jav.test.evil/covers/42/original", ""},
		{"https://stash.test/scene/not-an-id/screenshot", ""},
		{"https://jav.test/covers/../../private", ""},
	} {
		if got := artworkCacheSource(tc.raw, "https://stash.test", "https://jav.test"); got != tc.want {
			t.Errorf("%s: %q != %q", tc.raw, got, tc.want)
		}
	}
}

func TestCacheArtworkPreservesSelectionsLocksAndSkipsCached(t *testing.T) {
	for _, locked := range []bool{false, true} {
		t.Run(map[bool]string{false: "unlocked", true: "locked"}[locked], func(t *testing.T) {
			item := cacheArtworkItem{ContentID: "local-42", Type: "movie", PosterURL: "https://jav.test/covers/42/stash-poster?api_key=secret", BackdropURL: "https://stash.test/scene/42/screenshot", LockedFields: []int{1}}
			if locked {
				item.LockedFields = append(item.LockedFields, artworkLockField)
			}
			wantLocks := append([]int{}, item.LockedFields...)
			applies, patches := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer key" {
					t.Error("missing auth")
				}
				switch r.Method {
				case "GET":
					if r.Header.Get("X-Profile-Id") != "profile" {
						t.Error("missing profile")
					}
					json.NewEncoder(w).Encode(item)
				case "POST":
					var in struct {
						Source string `json:"original_url"`
						Type   string `json:"type"`
					}
					json.NewDecoder(r.Body).Decode(&in)
					want := "stash://backend/covers/42/stash-poster"
					if in.Type == "backdrop" {
						want = "stash://scene/42/screenshot"
					}
					if in.Source != want {
						t.Errorf("unexpected selection %s", in.Source)
					}
					if in.Type == "poster" {
						item.PosterURL = "https://s3.test/poster.webp"
					} else {
						item.BackdropURL = "https://s3.test/backdrop.webp"
					}
					if !locked && applies == 0 {
						item.LockedFields = append(item.LockedFields, artworkLockField, 2)
						wantLocks = append(wantLocks, 2)
					}
					applies++
					json.NewEncoder(w).Encode(map[string]string{"content_id": "local-42", "stored_path": "stored/revision.webp"})
				case "PATCH":
					var in struct {
						Locks []int `json:"locked_fields"`
					}
					json.NewDecoder(r.Body).Decode(&in)
					item.LockedFields = in.Locks
					patches++
					w.Write([]byte(`{}`))
				}
			}))
			defer server.Close()
			n, err := cacheItemArtwork(context.Background(), server.URL, "key", "profile", "local-42", "https://stash.test", "https://jav.test")
			if err != nil || n != 2 {
				t.Fatalf("cached=%d err=%v", n, err)
			}
			if !reflect.DeepEqual(item.LockedFields, wantLocks) {
				t.Fatalf("locks %v want %v", item.LockedFields, wantLocks)
			}
			n, err = cacheItemArtwork(context.Background(), server.URL, "key", "profile", "local-42", "https://stash.test", "https://jav.test")
			if err != nil || n != 0 || applies != 2 {
				t.Fatalf("cached image reapplied: count=%d applies=%d err=%v", n, applies, err)
			}
			if (locked && patches != 0) || (!locked && patches != 1) {
				t.Fatalf("patches=%d", patches)
			}
		})
	}
}

func TestCacheArtworkFailureRestoresAddedLock(t *testing.T) {
	item := cacheArtworkItem{ContentID: "local-42", Type: "movie", PosterURL: "stash://scene/42/screenshot", BackdropURL: "stash://scene/42/screenshot"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(item)
		case "POST":
			if item.PosterURL == "stored" {
				w.WriteHeader(500)
				return
			}
			item.PosterURL = "stored"
			item.LockedFields = []int{artworkLockField}
			w.Write([]byte(`{"content_id":"local-42","stored_path":"stored"}`))
		case "PATCH":
			item.LockedFields = []int{}
			w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	n, err := cacheItemArtwork(context.Background(), server.URL, "key", "p", "local-42", "https://stash.test", "")
	if n != 1 || err == nil || len(item.LockedFields) != 0 {
		t.Fatalf("cached=%d locks=%v err=%v", n, item.LockedFields, err)
	}
}
