package legacyprovider

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// StashSavedFilter is one StashApp SCENES saved filter and its ordered local
// scene files. It is independent of JAVBeacon's Release Library presets.
type StashSavedFilter struct {
	ID    string                 `json:"id"`
	Name  string                 `json:"name"`
	Items []StashSavedFilterItem `json:"items"`
}
type StashSavedFilterItem struct {
	SceneID string `json:"stash_scene_id"`
	Path    string `json:"path"`
	Code    string `json:"code"`
	Title   string `json:"title"`
}

func (p *Provider) StashSavedFilters(ctx context.Context, selection string) ([]StashSavedFilter, error) {
	base, key, directErr := p.stashConnection()
	if directErr == nil {
		p.stashFilterMu.Lock()
		defer p.stashFilterMu.Unlock()
		cacheKey := base + "\x00" + key + "\x00" + selection
		if cacheKey == p.stashFilterCacheKey && time.Since(p.stashFilterCacheAt) < time.Minute {
			return p.stashFilterCache, nil
		}
		items, err := p.stashSavedFiltersDirect(ctx, selection)
		if err != nil {
			return nil, err
		}
		p.stashFilterCache, p.stashFilterCacheKey, p.stashFilterCacheAt = items, cacheKey, time.Now()
		return items, nil
	}
	client, err := p.activeClient()
	if err != nil {
		return nil, err
	}
	var response struct {
		Items []StashSavedFilter `json:"items"`
	}
	if err = client.getJSON(ctx, "/api/v1/integrations/silo/stash-saved-filters?selection="+url.QueryEscape(selection), &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}

func (p *Provider) ConfigureStashConnection(base, key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stashURL, p.stashAPIKey = base, key
}

func (p *Provider) stashConnection() (string, string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.stashURL == "" || p.stashAPIKey == "" {
		return "", "", fmt.Errorf("Stash connection not configured")
	}
	return p.stashURL, p.stashAPIKey, nil
}
