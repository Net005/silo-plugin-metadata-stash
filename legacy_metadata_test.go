package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	legacytasks "github.com/Net005/silo-plugin-metadata-stash/internal/legacytasks"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func TestLegacyReleaseWithoutStashSceneRetainsMetadataAndArtwork(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Error("missing backend key")
		}
		if r.URL.Path != "/api/v1/integrations/silo/releases/7" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"release_id":7,"code":"ATID-7","title":"Original","overview":"Story","release_date":"2026-01-01","premiere_date":"2026-01-01","studio":"Studio","performers":["Actor"],"genres":["Drama","Watchlist","Collection: Demo"],"cover_path":"/covers/7/silo-primary","cover_backdrop_path":"/covers/7/original","backdrop_urls":["/screenshots/7/0"]}`))
	}))
	defer backend.Close()
	manager := legacytasks.New(nil)
	manager.Configure(legacytasks.Config{JAVBeaconURL: backend.URL, JAVBeaconKey: "key"})
	server := &metadataServer{runtime: &runtimeServer{client: &stashClient{}, legacy: manager}}
	meta, err := server.GetMetadata(context.Background(), &pluginv1.GetMetadataRequest{ProviderId: "javbeacon:7", ItemType: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	item := meta.GetItem()
	if item == nil || item.GetTitle() != "ATID-7" || len(item.GetGenres()) != 1 || item.GetGenres()[0] != "Drama" || item.GetPosterPath() != "javbeacon://covers/7/silo-primary" {
		t.Fatalf("metadata=%v", item)
	}
	images, err := server.GetImages(context.Background(), &pluginv1.GetImagesRequest{ProviderId: "javbeacon:7", ItemType: "movie"})
	if err != nil || len(images.GetImages()) != 3 {
		t.Fatalf("images=%v err=%v", images, err)
	}
	resolved, err := server.ResolveImageURL(context.Background(), &pluginv1.ResolveImageURLRequest{Path: item.GetPosterPath()})
	if err != nil || !strings.HasPrefix(resolved.GetUrl(), backend.URL+"/covers/7/silo-primary?api_key=key") {
		t.Fatalf("url=%q err=%v", resolved.GetUrl(), err)
	}
}

func TestLegacyReleaseIDResolvesToLinkedStashScene(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/integrations/silo/releases/7":
			_, _ = w.Write([]byte(`{"release_id":7,"stash_scene_id":"42","code":"ATID-7"}`))
		case "/api/v1/integrations/stash/enrichment/42":
			_, _ = w.Write([]byte(`{"scene_id":"42","release_id":7,"poster_path":"/covers/7/stash-poster"}`))
		default:
			t.Errorf("unexpected backend path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer backend.Close()
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"findScene":{"id":"42","title":"Curated Stash Title","code":"ATID-7","paths":{"screenshot":"/scene/42/screenshot"}}}}`))
	}))
	defer stash.Close()
	manager := legacytasks.New(nil)
	manager.Configure(legacytasks.Config{JAVBeaconURL: backend.URL, JAVBeaconKey: "key"})
	server := &metadataServer{runtime: &runtimeServer{client: &stashClient{base: stash.URL, key: "stash-key"}, artwork: &artworkClient{base: backend.URL, key: "key"}, legacy: manager}}
	meta, err := server.GetMetadata(context.Background(), &pluginv1.GetMetadataRequest{ProviderId: "javbeacon:7", ItemType: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	item := meta.GetItem()
	ids := item.GetProviderIds().AsMap()
	if item.GetTitle() != "Curated Stash Title" || ids["stash"] != "42" || ids["javbeacon"] != "7" {
		t.Fatalf("item=%v ids=%v", item, ids)
	}
}
