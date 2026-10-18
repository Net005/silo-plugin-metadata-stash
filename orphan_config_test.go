package main

import (
	"encoding/json"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestOrphanFileRepairRequiresExplicitOptInSchema(t *testing.T) {
	var manifest struct {
		GlobalConfigSchema []struct {
			Key        string `json:"key"`
			JSONSchema string `json:"json_schema"`
			AdminForm  struct {
				Fields []struct {
					Key, Control string
					DefaultValue any `json:"default_value"`
				} `json:"fields"`
			} `json:"admin_form"`
		} `json:"global_config_schema"`
	}
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, entry := range manifest.GlobalConfigSchema {
		if entry.Key != "connection" {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				Type    string
				Default any
			}
		}
		if err := json.Unmarshal([]byte(entry.JSONSchema), &schema); err != nil {
			t.Fatal(err)
		}
		prop, ok := schema.Properties["orphan_file_repair_enabled"]
		if !ok || prop.Type != "boolean" || prop.Default != false {
			t.Fatalf("missing explicit false-by-default repair setting: %#v", prop)
		}
		for _, field := range entry.AdminForm.Fields {
			if field.Key == "orphan_file_repair_enabled" && field.Control == "ADMIN_FORM_CONTROL_SWITCH" && field.DefaultValue == false {
				return
			}
		}
		t.Fatal("missing repair opt-in switch")
	}
	t.Fatal("missing connection schema")
}

func TestInvalidOrphanRepairOptionPreservesWorkingConnection(t *testing.T) {
	original := &stashClient{base: "https://stash.example.com", key: "existing-key"}
	s := &runtimeServer{client: original}
	s.pollOnce.Do(func() {})
	s.watchlistPollOnce.Do(func() {})
	s.playtimePollOnce.Do(func() {})
	value, err := structpb.NewStruct(map[string]any{"base_url": "https://replacement.example.com", "orphan_file_repair_enabled": "true"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Configure(t.Context(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "connection", Value: value}}})
	if err == nil {
		t.Fatal("non-boolean repair option was accepted")
	}
	if s.client != original {
		t.Fatal("invalid repair option partially applied configuration")
	}
}

func TestConfigureOrphanRepairExplicitEnableAndDisable(t *testing.T) {
	s := &runtimeServer{}
	s.pollOnce.Do(func() {})
	s.watchlistPollOnce.Do(func() {})
	s.playtimePollOnce.Do(func() {})
	for _, tc := range []struct {
		name          string
		option        any
		present, want bool
	}{
		{"default", nil, false, false}, {"enabled", true, true, true}, {"disabled", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := map[string]any{"base_url": "https://stash.example.com"}
			if tc.present {
				config["orphan_file_repair_enabled"] = tc.option
			}
			value, err := structpb.NewStruct(config)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Configure(t.Context(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "connection", Value: value}}})
			if err != nil {
				t.Fatal(err)
			}
			if s.orphanRepairEnabled != tc.want {
				t.Fatalf("recovery enabled=%v, want %v", s.orphanRepairEnabled, tc.want)
			}
		})
	}
}
