package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func TestConformedPosterAndScreenshotsBecomeSiloImages(t *testing.T) {
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("ApiKey") != "stash-key" {
			t.Error("Stash API key missing")
		}
		_, _ = w.Write([]byte(`{"data":{"findScene":{"id":"42","title":"Scene","paths":{"screenshot":"/scene/42/screenshot"}}}}`))
	}))
	defer stash.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer artwork-key" {
			t.Error("artwork API key missing")
		}
		_, _ = w.Write([]byte(`{"scene_id":"42","poster_path":"/covers/7/stash-poster","backdrop_paths":["/covers/7/original","/screenshots/7/0","https://untrusted.example/image"]}`))
	}))
	defer backend.Close()
	runtime := &runtimeServer{client: &stashClient{base: stash.URL, key: "stash-key"}, artwork: &artworkClient{base: backend.URL, key: "artwork-key"}}
	server := &metadataServer{runtime: runtime}
	metadata, err := server.GetMetadata(t.Context(), &pluginv1.GetMetadataRequest{ProviderId: "stash:42", ItemType: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.GetItem().GetPosterPath(); got != "stash://backend/covers/7/stash-poster" {
		t.Fatalf("poster=%q", got)
	}
	images, err := server.GetImages(t.Context(), &pluginv1.GetImagesRequest{ProviderId: "stash:42", ItemType: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if len(images.GetImages()) != 4 {
		t.Fatalf("images=%v", images.GetImages())
	}
	if images.GetImages()[0].GetKind() != "poster" || images.GetImages()[0].GetUrl() != "stash://backend/covers/7/stash-poster" {
		t.Fatalf("first image=%v", images.GetImages()[0])
	}
	for _, image := range images.GetImages()[1:] {
		if image.GetKind() != "backdrop" {
			t.Fatalf("non-backdrop=%v", image)
		}
	}
	resolved, err := server.ResolveImageURL(context.Background(), &pluginv1.ResolveImageURLRequest{Path: images.GetImages()[0].GetUrl()})
	if err != nil || !strings.HasPrefix(resolved.GetUrl(), backend.URL+"/covers/7/stash-poster?api_key=artwork-key") {
		t.Fatalf("resolved=%q err=%v", resolved.GetUrl(), err)
	}
}

func TestImageResolverRoutesCanonicalAndSchemeLessArtwork(t *testing.T) {
	runtime := &runtimeServer{
		client:  &stashClient{base: "https://stash.example", key: "stash-key"},
		artwork: &artworkClient{base: "https://jav.example", key: "artwork-key"},
	}
	server := &metadataServer{runtime: runtime}
	cases := []struct{ input, host, path, variant string }{
		{"stash://backend/covers/400320/stash-poster", "jav.example", "/covers/400320/stash-poster", ""},
		{"backend/covers/400320/stash-poster", "jav.example", "/covers/400320/stash-poster", ""},
		{"javbeacon://covers/400320/stash-poster", "jav.example", "/covers/400320/stash-poster", ""},
		{"covers/400320/stash-poster", "jav.example", "/covers/400320/stash-poster", ""},
		{"api/v1/integrations/silo/stash/scenes/41641/cover?variant=poster", "jav.example", "/api/v1/integrations/silo/stash/scenes/41641/cover", "poster"},
		{"stash://scene/43263/screenshot", "stash.example", "/scene/43263/screenshot", ""},
		{"scene/43263/screenshot", "stash.example", "/scene/43263/screenshot", ""},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := server.resolveImageURL(runtime.client, tc.input)
			u, err := url.Parse(got)
			if err != nil || u.Host != tc.host || u.Path != tc.path || u.Query().Get("variant") != tc.variant {
				t.Fatalf("unexpected destination host=%q path=%q variant=%q err=%v", u.Host, u.Path, u.Query().Get("variant"), err)
			}
			if tc.host == "jav.example" && u.Query().Get("api_key") != "artwork-key" {
				t.Fatal("JAVBeacon key missing")
			}
			if tc.host == "stash.example" && u.Query().Get("apikey") != "stash-key" {
				t.Fatal("Stash key missing")
			}
		})
	}
}

func TestArtworkFallbackUsesIndependentStashPosterRoute(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer backend.Close()
	art, err := (&artworkClient{base: backend.URL, key: "key"}).fetch(t.Context(), "42")
	if err != nil || art == nil || art.PosterPath != "/api/v1/integrations/silo/stash/scenes/42/cover?variant=poster" {
		t.Fatalf("artwork=%v err=%v", art, err)
	}
}

func TestUnlinkedStashCoverStillUsesPosterCropAndRawBackdrop(t *testing.T) {
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"findScene":{"id":"42","title":"AKBS-010","paths":{"screenshot":"/scene/42/screenshot"}}}}`))
	}))
	defer stash.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer backend.Close()
	runtime := &runtimeServer{client: &stashClient{base: stash.URL, key: "key"}, artwork: &artworkClient{base: backend.URL, key: "art-key"}}
	server := &metadataServer{runtime: runtime}
	want := "stash://backend/api/v1/integrations/silo/stash/scenes/42/cover?variant=poster"
	metadata, err := server.GetMetadata(t.Context(), &pluginv1.GetMetadataRequest{ProviderId: "stash:42", ItemType: "movie"})
	if err != nil || metadata.GetItem().GetPosterPath() != want {
		t.Fatalf("metadata=%v err=%v", metadata, err)
	}
	images, err := server.GetImages(t.Context(), &pluginv1.GetImagesRequest{ProviderId: "stash:42", ItemType: "movie"})
	if err != nil || len(images.GetImages()) != 2 || images.Images[0].Url != want || images.Images[1].Url != coverPath("42") {
		t.Fatalf("images=%v err=%v", images, err)
	}
	resolved := server.resolveImageURL(runtime.client, want)
	u, err := url.Parse(resolved)
	if err != nil || u.Host != strings.TrimPrefix(backend.URL, "http://") || u.Query().Get("variant") != "poster" || u.Query().Get("api_key") != "art-key" {
		t.Fatalf("resolved image: %v %v", u, err)
	}
}
