package main

import (
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestConfigurePreservesCollectionPrefixWhitespace(t *testing.T) {
	s := &runtimeServer{}
	// Config parsing does not need to start the matching worker.
	s.pollOnce.Do(func() {})
	value, err := structpb.NewStruct(map[string]any{
		"base_url":                  " https://stash.example/ ",
		"stash_saved_filter_prefix": "Stash | ",
		"saved_filter_prefix":       " JAV | ",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Configure(t.Context(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "connection", Value: value}}})
	if err != nil {
		t.Fatal(err)
	}
	if s.stashPrefix != "Stash | " || s.releasePrefix != " JAV | " {
		t.Fatalf("prefix whitespace lost: Stash=%q JAVBeacon=%q", s.stashPrefix, s.releasePrefix)
	}
	if s.client.base != "https://stash.example" {
		t.Fatalf("URL normalization changed: %q", s.client.base)
	}
}

func TestInvalidPosterLayoutPreservesConnection(t *testing.T) {
	client := &stashClient{base: "https://stash.example.com", key: "existing-stash-key"}
	artwork := &artworkClient{base: "https://beacon.example.com", key: "existing-artwork-key"}
	s := &runtimeServer{client: client, artwork: artwork, siloBase: "https://silo.example.com", siloKey: "existing-silo-key", posterLayout: "contain", stashPrefix: "Original | "}
	value, err := structpb.NewStruct(map[string]any{
		"base_url": "https://replacement.example.com", "api_key": "replacement-key",
		"javbeacon_url": "https://replacement.example.com", "silo_base_url": "https://replacement.example.com",
		"silo_api_key": "replacement-key", "nonjav_poster_layout": "invalid", "stash_saved_filter_prefix": "Changed | ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Configure(t.Context(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "connection", Value: value}}}); err == nil {
		t.Fatal("invalid layout was accepted")
	}
	if s.client != client || s.artwork != artwork || s.siloBase != "https://silo.example.com" || s.siloKey != "existing-silo-key" || s.posterLayout != "contain" || s.stashPrefix != "Original | " {
		t.Fatal("invalid settings changed the working connection")
	}
}

func TestSearchResultIncludesSceneYear(t *testing.T) {
	for _, tc := range []struct {
		date string
		year int32
	}{
		{"2024-06-01", 2024}, {"2023", 2023}, {"", 0}, {"bad-date", 0}, {"0000-01-01", 0},
	} {
		t.Run(tc.date, func(t *testing.T) {
			s := scene{ID: "123", Title: "Example", Date: tc.date}
			if got := searchResult(s).GetYear(); got != tc.year {
				t.Fatalf("search year = %d, want %d", got, tc.year)
			}
			if got := metadataItem(s).GetYear(); got != tc.year {
				t.Fatalf("metadata year = %d, want %d", got, tc.year)
			}
		})
	}
}

func TestConfigureRejectsWholeRequestBeforeApplyingEntries(t *testing.T) {
	entry := func(key string, value map[string]any) *pluginv1.ConfigEntry {
		t.Helper()
		v, err := structpb.NewStruct(value)
		if err != nil {
			t.Fatal(err)
		}
		return &pluginv1.ConfigEntry{Key: key, Value: v}
	}
	for _, entries := range [][]*pluginv1.ConfigEntry{
		{entry("connection", map[string]any{"base_url": "https://replacement.example.com"}), entry("recommendations", map[string]any{"reasoning_effort": "invalid"})},
		{entry("recommendations", map[string]any{"monthly_cap_usd": 7}), entry("connection", map[string]any{"nonjav_poster_layout": "invalid"})},
	} {
		client := &stashClient{base: "https://stash.example.com", key: "existing-key"}
		s := &runtimeServer{client: client, recommendationConfig: recommendationConfig{MonthlyCap: 3}}
		if _, err := s.Configure(t.Context(), &pluginv1.ConfigureRequest{Config: entries}); err == nil {
			t.Fatal("invalid request accepted")
		}
		if s.client != client || s.recommendationConfig.MonthlyCap != 3 {
			t.Fatal("part of invalid request was applied")
		}
	}
}
