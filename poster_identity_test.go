package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSharedJacketConsensusDoesNotRelaxPlaybackIdentity(t *testing.T) {
	for _, conflict := range []string{"", "code", "image"} {
		t.Run(conflict, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/poster/") {
					if conflict == "image" && strings.HasSuffix(r.URL.Path, "2") {
						w.Write([]byte("different jacket"))
					} else {
						w.Write([]byte("shared jacket"))
					}
					return
				}
				var request struct {
					Query     string                     `json:"query"`
					Variables map[string]json.RawMessage `json:"variables"`
				}
				json.NewDecoder(r.Body).Decode(&request)
				var data any
				if strings.Contains(request.Query, "findScenes") {
					var path struct {
						Value string `json:"value"`
					}
					json.Unmarshal(request.Variables["path"], &path)
					id := "1"
					if path.Value == "/part2.mp4" {
						id = "2"
					}
					data = map[string]any{"findScenes": map[string]any{"count": 1, "scenes": []any{map[string]any{"id": id, "files": []any{map[string]any{"path": path.Value}}}}}}
				} else {
					var id string
					json.Unmarshal(request.Variables["id"], &id)
					code := "ABC-01"
					if conflict == "code" && id == "2" {
						code = "XYZ-99"
					}
					data = map[string]any{"findScene": map[string]any{"id": id, "code": code, "paths": map[string]any{"screenshot": "/poster/" + id}}}
				}
				json.NewEncoder(w).Encode(map[string]any{"data": data})
			}))
			defer server.Close()
			c := &stashClient{base: server.URL, key: "test"}
			paths := []string{"/part1.mp4", "/part2.mp4"}
			if _, err := c.sceneIDForExactPaths(t.Context(), paths); err == nil {
				t.Fatal("multipart playback identity should remain ambiguous")
			}
			row, raw, err := c.sharedJacketForParts(t.Context(), paths)
			if conflict != "" {
				if err == nil || row != nil || raw != nil {
					t.Fatal("conflicting source accepted")
				}
				return
			}
			if err != nil || row == nil || !bytes.Equal(raw, []byte("shared jacket")) {
				t.Fatal("verified shared jacket rejected", err)
			}
		})
	}
}
