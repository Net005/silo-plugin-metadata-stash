package main

import (
	"bytes"
	"context"
	"fmt"
	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	"io"
	"net/http"
	"strings"
	"time"
)

// Artwork-only consensus for multipart titles. Never use this result for
// playback writes: each exact file must resolve uniquely, and all scene parts
// must share the same code and original jacket, or exactly one source must
// match the artwork Silo already displays.
func (c *stashClient) sharedJacketForParts(ctx context.Context, paths []string, currentPoster string) (*scene, []byte, error) {
	seen := map[string]bool{}
	var first *scene
	var artwork []byte
	type choice struct {
		row *scene
		raw []byte
	}
	choices := []choice{}
	shared := true
	for _, path := range paths {
		id, err := c.sceneIDForExactPaths(ctx, []string{path})
		if err != nil || id == "" {
			return nil, nil, fmt.Errorf("multipart jacket file identity is not unique")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		row, err := c.findScene(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if row == nil || !javPosterCode.MatchString(strings.TrimSpace(row.Code)) || row.Paths.Screenshot == "" {
			return nil, nil, fmt.Errorf("multipart jacket lacks verified JAV source")
		}
		if first != nil && !strings.EqualFold(strings.TrimSpace(row.Code), strings.TrimSpace(first.Code)) {
			shared = false
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.imageURL(row.Paths.Screenshot), nil)
		if err != nil {
			return nil, nil, err
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return nil, nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || len(raw) > 16<<20 {
			return nil, nil, fmt.Errorf("multipart jacket source unavailable")
		}
		choices = append(choices, choice{row, raw})
		if first == nil {
			first = row
			artwork = raw
		} else if !bytes.Equal(artwork, raw) {
			shared = false
		}
	}
	if len(seen) < 2 {
		return nil, nil, fmt.Errorf("multipart jacket requires multiple verified parts")
	}
	if shared {
		return first, artwork, nil
	}
	// Conflicting sources can only retain the uniquely verified artwork that
	// Silo already displays; never choose a new scene or change playback IDs.
	if currentPoster == "" {
		return nil, nil, fmt.Errorf("multipart jacket sources conflict")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentPoster, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, nil, err
	}
	cached, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || len(cached) > 16<<20 {
		return nil, nil, fmt.Errorf("current jacket unavailable")
	}
	var matched *scene
	var selected []byte
	for _, candidate := range choices {
		if !provider.SameArtworkImage(cached, candidate.raw) {
			continue
		}
		if matched != nil {
			return nil, nil, fmt.Errorf("current jacket matches multiple conflicting sources")
		}
		matched = candidate.row
		selected = candidate.raw
	}
	if matched == nil {
		return nil, nil, fmt.Errorf("multipart jacket has no verified current source")
	}
	return matched, selected, nil
}
