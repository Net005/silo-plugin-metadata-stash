package main

import (
	"encoding/json"
	"testing"

	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
)

func TestManifest(t *testing.T) {
	m, err := publicmanifest.Load(manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	if m.PluginId != "stash.metadata" {
		t.Fatal(m.PluginId)
	}
	var raw struct {
		Capabilities []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"capabilities"`
		GlobalConfigSchema []struct {
			JSONSchema string `json:"json_schema"`
			AdminForm  struct {
				Fields []struct {
					Key      string `json:"key"`
					Required bool   `json:"required"`
				} `json:"fields"`
			} `json:"admin_form"`
		} `json:"global_config_schema"`
	}
	if err := json.Unmarshal(manifestJSON, &raw); err != nil {
		t.Fatal(err)
	}
	tasks := map[string]bool{}
	for _, c := range raw.Capabilities {
		if c.Type == "scheduled_task.v1" {
			tasks[c.ID] = true
		}
	}
	for _, id := range []string{"cache-artwork", "collection-sync", "watchlist-collection-sync", "match-unmatched", "repair-matched", "metadata-refresh", "watched-sync", "play-backfill"} {
		if !tasks[id] {
			t.Errorf("missing scheduled task %s", id)
		}
	}
	if len(tasks) != 8 {
		t.Errorf("scheduled tasks=%v", tasks)
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal([]byte(raw.GlobalConfigSchema[0].JSONSchema), &schema); err != nil {
		t.Fatal(err)
	}
	for _, key := range schema.Required {
		if key == "api_key" {
			t.Fatal("saved API secret cannot be required in the form schema")
		}
	}
	for _, field := range raw.GlobalConfigSchema[0].AdminForm.Fields {
		if field.Key == "api_key" && field.Required {
			t.Fatal("saved API secret cannot be required in the admin form")
		}
	}
}
