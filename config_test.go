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
