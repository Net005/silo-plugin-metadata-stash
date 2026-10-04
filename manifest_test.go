package main

import (
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"testing"
)

func TestManifest(t *testing.T) {
	m, err := publicmanifest.Load(manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	if m.PluginId != "stash.metadata" {
		t.Fatal(m.PluginId)
	}
}
