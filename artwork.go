package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type artworkClient struct {
	base string
	key  string
	http *http.Client
}

type sceneArtwork struct {
	SceneID       string   `json:"scene_id"`
	PosterPath    string   `json:"poster_path"`
	BackdropPaths []string `json:"backdrop_paths"`
}

func (c *artworkClient) configured() bool { return c != nil && c.base != "" && c.key != "" }

func (c *artworkClient) fetch(ctx context.Context, sceneID string) (*sceneArtwork, error) {
	if !c.configured() || sceneID == "" {
		return nil, nil
	}
	endpoint := c.base + "/api/v1/integrations/stash/enrichment/" + url.PathEscape(sceneID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	client := c.http
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JAVBeacon artwork HTTP %d", resp.StatusCode)
	}
	var art sceneArtwork
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&art); err != nil {
		return nil, err
	}
	if art.SceneID != sceneID {
		return nil, fmt.Errorf("JAVBeacon artwork scene ID mismatch")
	}
	if !validArtworkPath(art.PosterPath) {
		art.PosterPath = ""
	}
	paths := make([]string, 0, len(art.BackdropPaths))
	for _, path := range art.BackdropPaths {
		if validArtworkPath(path) {
			paths = append(paths, path)
		}
	}
	art.BackdropPaths = paths
	return &art, nil
}

func validArtworkPath(path string) bool {
	if !strings.HasPrefix(path, "/covers/") && !strings.HasPrefix(path, "/screenshots/") {
		return false
	}
	return !strings.Contains(path, "..") && !strings.ContainsAny(path, "?#\\")
}

func backendImagePath(path string) string {
	if !validArtworkPath(path) {
		return ""
	}
	return "stash://backend" + path
}

func (c *artworkClient) imageURL(path string) string {
	if !c.configured() || !validArtworkPath(path) {
		return ""
	}
	u, err := url.Parse(c.base + path)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("api_key", c.key)
	u.RawQuery = q.Encode()
	return u.String()
}
