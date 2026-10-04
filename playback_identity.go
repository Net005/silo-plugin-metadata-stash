package main

import (
	"context"
	"fmt"
)

// Silo's watch-event DTO carries standard movie IDs but omits custom Stash
// IDs. Verify every native file against Stash; never infer identity from art.
func (c *stashClient) sceneIDForExactPaths(ctx context.Context, paths []string) (string, error) {
	id := ""
	seen := map[string]bool{}
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		var data struct {
			Scenes struct {
				Count int `json:"count"`
				Items []struct {
					ID    string `json:"id"`
					Files []struct {
						Path string `json:"path"`
					} `json:"files"`
				} `json:"scenes"`
			} `json:"findScenes"`
		}
		vars := map[string]any{"path": map[string]any{"value": path, "modifier": "EQUALS"}}
		if err := c.graphql(ctx, `query($path:StringCriterionInput!){findScenes(scene_filter:{path:$path},filter:{per_page:200}){count scenes{id files{path}}}}`, vars, &data); err != nil {
			return "", err
		}
		if data.Scenes.Count > 200 {
			return "", fmt.Errorf("Stash file identity exceeds lookup limit")
		}
		matched := false
		for _, row := range data.Scenes.Items {
			for _, file := range row.Files {
				if file.Path != path {
					continue
				}
				if id != "" && id != row.ID {
					return "", fmt.Errorf("multiple Stash scenes match playback files")
				}
				id, matched = row.ID, true
			}
		}
		if !matched {
			return "", nil
		}
	}
	return id, nil
}
