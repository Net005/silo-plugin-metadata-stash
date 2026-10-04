package main

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestArtworkFallbackUsesStashScreenshot(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer backend.Close()
	art, err := (&artworkClient{base: backend.URL, key: "key"}).fetch(t.Context(), "42")
	if err != nil || art != nil {
		t.Fatalf("artwork=%v err=%v", art, err)
	}
}
