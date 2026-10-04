package legacyprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestArtworkChoiceUsesOnlyMembersAndRotates(t *testing.T) {
	spec := CollectionSpec{Kind: "watchlist", LibraryID: "lib", MediaIDs: []string{"new", "old", "middle"}, Artwork: []CollectionArtwork{
		{MediaID: "new", PosterURL: "new-poster", BackdropURL: "new-backdrop", ReleaseDate: "2026-09-20"},
		{MediaID: "middle", PosterURL: "middle-poster", BackdropURL: "middle-backdrop", ReleaseDate: "2025-01-01"},
		{MediaID: "old", PosterURL: "old-poster", BackdropURL: "old-backdrop", ReleaseDate: "2018-01-01"},
		{MediaID: "outside", PosterURL: "outside-poster", BackdropURL: "outside-backdrop", ReleaseDate: "2026-09-27"},
	}}
	start := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	seen := map[string]bool{}
	for i := 0; i < 24; i++ {
		now := start.Add(time.Duration(i) * artworkRotation)
		poster, backdrop := artworkChoice(spec, now)
		if poster.MediaID == "outside" || backdrop.MediaID == "outside" || poster.PosterURL == "" || backdrop.BackdropURL == "" {
			t.Fatalf("picked nonmember or blank art: %q %q", poster, backdrop)
		}
		again, _ := artworkChoice(spec, now.Add(time.Hour))
		if again.MediaID != poster.MediaID {
			t.Fatalf("art changed inside rotation window: %q -> %q", poster, again)
		}
		seen[poster.MediaID] = true
	}
	if len(seen) < 2 {
		t.Fatalf("art never rotated: %v", seen)
	}
}

func TestSyncCollectionArtworkUploadsOncePerWindow(t *testing.T) {
	uploads := 0
	marker := map[string]json.RawMessage{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/admin/collections/c1" {
			w.Header().Set("ETag", `"art-v1"`)
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(siloCollection{ID: "c1", SourceConfig: marker})
			case http.MethodPatch:
				if r.Header.Get("If-Match") != `"art-v1"` {
					t.Errorf("missing If-Match")
				}
				var update struct {
					SourceConfig map[string]json.RawMessage `json:"source_config"`
				}
				_ = json.NewDecoder(r.Body).Decode(&update)
				marker = update.SourceConfig
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Errorf("unexpected method %s", r.Method)
			}
			return
		}
		if r.URL.Path == "/api/v2/admin/collections/c1/poster" || r.URL.Path == "/api/v2/admin/collections/c1/backdrop" {
			if r.Method != http.MethodPut {
				t.Errorf("unexpected method %s", r.Method)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("source_url") == "" {
				t.Errorf("missing source_url: %v", err)
			}
			uploads++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client := NewSiloClient(server.URL, "test")
	spec := CollectionSpec{Kind: "watchlist", LibraryID: "lib", MediaIDs: []string{"m1"}, Artwork: []CollectionArtwork{{MediaID: "m1", PosterURL: "https://art.test/poster", BackdropURL: "https://art.test/backdrop"}}}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		changed, err := client.syncCollectionArtwork(context.Background(), siloCollection{ID: "c1"}, spec, now)
		if err != nil || changed != (i == 0) {
			t.Fatalf("run %d: changed=%v err=%v", i, changed, err)
		}
	}
	if uploads != 2 {
		t.Fatalf("uploaded %d times, want poster and backdrop once", uploads)
	}
}

func TestCollectionArtworkFallsBackToFileWhenSiloCannotFetchURL(t *testing.T) {
	image := []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 'J', 'F', 'I', 'F', 0}
	fileUploads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/image" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(image)
			return
		}
		if r.URL.Path != "/api/v2/admin/collections/c1/poster" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse upload: %v", err)
		}
		if r.FormValue("source_url") != "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		file, header, err := r.FormFile("image")
		if err != nil {
			t.Errorf("missing file: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		if header.Header.Get("Content-Type") != "image/jpeg" {
			t.Errorf("unexpected file type %q", header.Header.Get("Content-Type"))
		}
		fileUploads++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := NewSiloClient(server.URL, "test")
	if err := client.uploadCollectionArtwork(context.Background(), "c1", "poster", server.URL+"/image"); err != nil {
		t.Fatal(err)
	}
	if fileUploads != 1 {
		t.Fatalf("file uploads = %d, want 1", fileUploads)
	}
}

func TestArtworkRotationUsuallyFavorsRecentButSometimesUsesOlder(t *testing.T) {
	candidates := make([]CollectionArtwork, 20)
	for i := range candidates {
		candidates[i] = CollectionArtwork{MediaID: fmt.Sprintf("m%02d", i), PosterURL: "https://art.test/poster", ReleaseDate: fmt.Sprintf("%04d-01-01", 2026-i)}
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent, older := 0, 0
	for i := 0; i < 100; i++ {
		chosen := weightedArtworkPick(append([]CollectionArtwork(nil), candidates...), "collection", "poster", start.Add(time.Duration(i)*artworkRotation))
		if chosen.MediaID < "m04" {
			recent++
		} else {
			older++
		}
	}
	if recent <= older || older == 0 {
		t.Fatalf("recent=%d older=%d, want recent favored and some older", recent, older)
	}
}
