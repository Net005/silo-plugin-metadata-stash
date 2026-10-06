package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Artwork-only consensus for multipart titles. Never use this result for
// playback writes: each exact file must resolve uniquely, and all scene parts
// must have the same JAV code AND byte-identical original jacket artwork.
func (c *stashClient) sharedJacketForParts(ctx context.Context, paths []string) (*scene, []byte, error) {
	seen := map[string]bool{}
	var first *scene
	var artwork []byte
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
			return nil, nil, fmt.Errorf("multipart jacket codes differ")
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
		if first == nil {
			first = row
			artwork = raw
		} else if !bytes.Equal(artwork, raw) {
			return nil, nil, fmt.Errorf("multipart jacket images differ")
		}
	}
	if len(seen) < 2 {
		return nil, nil, fmt.Errorf("multipart jacket requires multiple verified parts")
	}
	return first, artwork, nil
}
