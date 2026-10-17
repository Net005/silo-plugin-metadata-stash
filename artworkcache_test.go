package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestProviderArtworkStorageSetting(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       bool
		wantErr    bool
	}{
		{"enabled", `{"key":"metadata.cache_images","value":"true"}`, 200, true, false},
		{"disabled", `{"key":"metadata.cache_images","value":"false"}`, 200, false, false},
		{"numeric disabled", `{"key":"metadata.cache_images","value":"0"}`, 200, false, false},
		{"default", `{}`, 404, true, false},
		{"forbidden", `{}`, 403, false, true},
		{"unavailable", `{}`, 503, false, true},
		{"invalid boolean", `{"key":"metadata.cache_images","value":"invalid"}`, 200, false, true},
		{"wrong key", `{"key":"other","value":"true"}`, 200, false, true},
		{"missing value", `{"key":"metadata.cache_images"}`, 200, false, true},
		{"malformed", `{`, 200, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v2/admin/settings/metadata.cache_images" || r.Header.Get("Authorization") != "Bearer key" {
					t.Errorf("unexpected setting request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			got, err := providerArtworkStorageEnabled(t.Context(), server.URL, "key")
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("enabled=%t err=%v", got, err)
			}
		})
	}
}

func TestArtworkWorkersPauseWithoutChangingState(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v2/admin/settings/metadata.cache_images" {
					t.Errorf("paused worker made unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 500)
					return
				}
				w.WriteHeader(status)
				w.Write([]byte(`{"key":"metadata.cache_images","value":"false"}`))
			}))
			defer server.Close()
			task := &scheduledTaskServer{
				runtime:       &runtimeServer{siloBase: server.URL, siloKey: "key", posterRepair: true, client: &stashClient{base: server.URL, key: "key"}, recommendationConfig: recommendationConfig{Profile: "profile"}},
				artworkCursor: "resume-page", artworkRetry: map[string]time.Time{"item": time.Now().Add(time.Hour)},
			}
			for _, run := range []func(context.Context) (map[string]any, error){task.cacheArtwork, task.repairPosters} {
				result, err := run(t.Context())
				if status == http.StatusOK {
					if err != nil || result["status"] != "storage_disabled" {
						t.Fatalf("result=%v err=%v", result, err)
					}
				} else if err == nil {
					t.Fatal("unreadable setting admitted worker")
				}
			}
			if task.artworkCursor != "resume-page" || len(task.artworkRetry) != 1 {
				t.Fatal("paused worker changed progress")
			}
		})
	}
}

func TestArtworkCacheResumesAfterStorageEnabled(t *testing.T) {
	enabled, pages, reads := true, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/admin/settings/metadata.cache_images":
			reads++
			json.NewEncoder(w).Encode(map[string]any{"key": "metadata.cache_images", "value": map[bool]string{true: "true", false: "false"}[enabled]})
		case "/api/v2/profiles":
			w.Write([]byte(`{"items":[{"id":"profile"}]}`))
		case "/api/v2/catalog":
			pages++
			if pages == 1 {
				enabled = false
				w.Write([]byte(`{"items":[],"page":{"has_more":true,"next_cursor":"page2"}}`))
			} else {
				if r.URL.Query().Get("cursor") != "page2" {
					t.Error("worker lost its cursor while paused")
				}
				w.Write([]byte(`{"items":[],"page":{"has_more":false}}`))
			}
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	task := &scheduledTaskServer{runtime: &runtimeServer{siloBase: server.URL, siloKey: "key"}}
	result, err := task.cacheArtwork(t.Context())
	if err != nil || result["status"] != "storage_disabled" || task.artworkCursor != "page2" || pages != 1 || reads != 2 {
		t.Fatalf("pause result=%v pages=%d reads=%d err=%v", result, pages, reads, err)
	}
	enabled = true
	result, err = task.cacheArtwork(t.Context())
	if err != nil || result["status"] != "complete" || task.artworkCursor != "" || pages != 2 {
		t.Fatalf("resume result=%v pages=%d err=%v", result, pages, err)
	}
}

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
