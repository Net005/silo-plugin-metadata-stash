package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"regexp"
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
	ReleaseID     int64    `json:"release_id"`
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
		// Artwork cropping does not require a JAVBeacon release. Its scene
		// endpoint can transform the original Stash cover independently.
		return &sceneArtwork{SceneID: sceneID, PosterPath: "/api/v1/integrations/silo/stash/scenes/" + url.PathEscape(sceneID) + "/cover?variant=poster"}, nil
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
	if !validArtworkReference(art.PosterPath) {
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

var performerImageRoute = regexp.MustCompile(`^/api/v1/integrations/performers/[0-9]+/image$`)
var legacySceneCoverRoute = regexp.MustCompile(`^/api/v1/integrations/silo/stash/scenes/[0-9]+/(?:cover|poster)$`)

func validArtworkPath(path string) bool {
	if performerImageRoute.MatchString(path) {
		return true
	}
	if legacySceneCoverRoute.MatchString(path) {
		return true
	}
	if !strings.HasPrefix(path, "/covers/") && !strings.HasPrefix(path, "/screenshots/") {
		return false
	}
	return !strings.Contains(path, "..") && !strings.ContainsAny(path, "?#\\")
}

func validArtworkReference(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !validArtworkPath(parsed.Path) {
		return false
	}
	if parsed.RawQuery == "" {
		return true
	}
	query := parsed.Query()
	return legacySceneCoverRoute.MatchString(parsed.Path) && len(query) == 1 &&
		(query.Get("variant") == "smart" || query.Get("variant") == "poster" || query.Get("variant") == "backdrop")
}

func backendImagePath(path string) string {
	if !validArtworkReference(path) {
		return ""
	}
	return "stash://backend" + path
}

func (c *artworkClient) imageURL(path string) string {
	if !c.configured() || !validArtworkReference(path) {
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

var errInsufficientPosterContext = errors.New("original artwork is already a close-up")

func (c *artworkClient) renderScenePoster(ctx context.Context, raw []byte) ([]byte, error) {
	if !c.configured() {
		return nil, fmt.Errorf("smart poster crop requires configured JAVBeacon URL and API key")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/integrations/silo/poster/render", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/octet-stream")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, errInsufficientPosterContext
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JAVBeacon poster render HTTP %d (requires JAVBeacon v1.0.286+)", resp.StatusCode)
	}
	result, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	if err != nil || len(result) > 16<<20 {
		return nil, fmt.Errorf("invalid rendered poster response")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(result))
	if err != nil || cfg.Width != 1200 || cfg.Height != 1800 {
		return nil, fmt.Errorf("rendered poster is not 1200x1800")
	}
	return result, nil
}
