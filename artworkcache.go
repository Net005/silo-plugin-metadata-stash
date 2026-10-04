package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const artworkLockField = 10 // Silo metadata.FieldImages

type cacheArtworkItem struct {
	ContentID    string `json:"content_id"`
	Type         string `json:"type"`
	PosterURL    string `json:"poster_url"`
	BackdropURL  string `json:"backdrop_url"`
	LockedFields []int  `json:"locked_fields"`
}

// Convert only known artwork on the exact configured provider origins back to
// credential-free plugin references. S3/local URLs and other providers are skipped.
func artworkCacheSource(raw, stashBase, backendBase string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if wrapped := u.Query().Get("url"); strings.HasPrefix(wrapped, "stash://") || strings.HasPrefix(wrapped, "javbeacon://") {
		return artworkCacheSource(wrapped, stashBase, backendBase)
	}
	if u.Scheme == "stash" || u.Scheme == "javbeacon" {
		path := strings.TrimPrefix(strings.TrimPrefix(raw, u.Scheme+"://"), "backend/")
		if validArtworkReference("/" + strings.TrimPrefix(path, "/")) {
			return "stash://backend/" + strings.TrimPrefix(path, "/")
		}
		if u.Scheme == "stash" && stashScreenshotPath(u.Host+u.Path) {
			return "stash://" + u.Host + u.Path
		}
		return ""
	}
	for _, endpoint := range []struct {
		base    string
		backend bool
	}{{backendBase, true}, {stashBase, false}} {
		b, e := url.Parse(endpoint.base)
		if e != nil || b.Host == "" || u.Scheme != b.Scheme || u.Host != b.Host {
			continue
		}
		basePath := strings.TrimRight(b.Path, "/")
		if !strings.HasPrefix(u.Path, basePath+"/") {
			continue
		}
		path := strings.TrimPrefix(u.Path, basePath)
		if endpoint.backend {
			q := url.Values{}
			if variant := u.Query().Get("variant"); variant != "" {
				q.Set("variant", variant)
			}
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			if validArtworkReference(path) {
				return "stash://backend/" + strings.TrimPrefix(path, "/")
			}
		} else if stashScreenshotPath(strings.TrimPrefix(path, "/")) {
			return "stash://" + strings.TrimPrefix(path, "/")
		}
	}
	return ""
}

func stashScreenshotPath(path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 3 || parts[0] != "scene" || parts[2] != "screenshot" || parts[1] == "" {
		return false
	}
	for _, ch := range parts[1] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func (s *scheduledTaskServer) runArtworkRepair() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := s.cacheArtwork(ctx)
	if err != nil {
		s.log.Warn("Stash artwork cache repair failed", "error", err)
	} else if result["scanned"] != nil {
		s.log.Info("Stash artwork cache repair", "summary", result)
	}
}

func (s *scheduledTaskServer) pollArtwork() {
	for {
		s.runArtworkRepair()
		time.Sleep(time.Minute)
	}
}

func (s *scheduledTaskServer) cacheArtwork(ctx context.Context) (map[string]any, error) {
	if !s.artworkMu.TryLock() {
		return map[string]any{"status": "already_running"}, nil
	}
	defer s.artworkMu.Unlock()
	s.runtime.mu.RLock()
	base, key := s.runtime.siloBase, s.runtime.siloKey
	stashBase, backendBase := "", ""
	if s.runtime.client != nil {
		stashBase = s.runtime.client.base
	}
	if s.runtime.artwork != nil {
		backendBase = s.runtime.artwork.base
	}
	s.runtime.mu.RUnlock()
	if base == "" || key == "" {
		return map[string]any{"status": "not_configured"}, nil
	}
	var profiles struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := siloRequest(ctx, base, key, http.MethodGet, "/api/v2/profiles", nil, &profiles); err != nil {
		return nil, err
	}
	if len(profiles.Items) == 0 {
		return nil, fmt.Errorf("artwork cache: no Silo profile")
	}
	profile := profiles.Items[0].ID
	if s.artworkRetry == nil {
		s.artworkRetry = map[string]time.Time{}
	}
	for id, until := range s.artworkRetry {
		if time.Now().After(until) {
			delete(s.artworkRetry, id)
		}
	}
	cached, failed, scanned := 0, 0, 0
	for page := 0; page < 5; page++ {
		// No library filter: include all accessible libraries, including Hentai.
		path := "/api/v2/catalog?limit=200&skip_total=true&status=matched&sort=-added_at"
		if s.artworkCursor != "" {
			path += "&cursor=" + url.QueryEscape(s.artworkCursor)
		}
		var data struct {
			Items []cacheArtworkItem `json:"items"`
			Page  struct {
				HasMore    bool   `json:"has_more"`
				NextCursor string `json:"next_cursor"`
			} `json:"page"`
		}
		if err := siloProfileRequest(ctx, base, key, profile, http.MethodGet, path, nil, &data); err != nil {
			s.artworkCursor = ""
			return nil, err
		}
		for _, item := range data.Items {
			scanned++
			if item.Type != "movie" || time.Now().Before(s.artworkRetry[item.ContentID]) {
				continue
			}
			if artworkCacheSource(item.PosterURL, stashBase, backendBase) == "" && artworkCacheSource(item.BackdropURL, stashBase, backendBase) == "" {
				continue
			}
			n, err := cacheItemArtwork(ctx, base, key, profile, item.ContentID, stashBase, backendBase)
			cached += n
			if err != nil {
				failed++
				s.artworkRetry[item.ContentID] = time.Now().Add(time.Hour)
				s.log.Warn("Stash artwork cache item failed", "content_id", item.ContentID, "error", err)
			}
			// Keep this page's cursor until its remaining images are stored.
			if cached+failed >= 40 {
				return map[string]any{"status": "partial", "cached": cached, "failed": failed, "scanned": scanned}, nil
			}
		}
		if !data.Page.HasMore {
			s.artworkCursor = ""
			return map[string]any{"status": "complete", "cached": cached, "failed": failed, "scanned": scanned}, nil
		}
		if data.Page.NextCursor == "" || data.Page.NextCursor == s.artworkCursor {
			return nil, fmt.Errorf("artwork cache: catalog cursor did not advance")
		}
		s.artworkCursor = data.Page.NextCursor
	}
	return map[string]any{"status": "partial", "cached": cached, "failed": failed, "scanned": scanned}, nil
}

// Cache the item's existing selections through Silo's supported admin API.
// Silo owns S3/local storage, immutable revisions and generated image sizes.
// No metadata refresh, alternate image choice or additional plugin cache occurs.
func cacheItemArtwork(ctx context.Context, base, key, profile, id, stashBase, backendBase string) (int, error) {
	path := "/api/v2/catalog/items/" + url.PathEscape(id)
	read := func(ctx context.Context) (cacheArtworkItem, error) {
		var item cacheArtworkItem
		err := siloProfileRequest(ctx, base, key, profile, http.MethodGet, path, nil, &item)
		if err == nil && item.ContentID != id {
			err = fmt.Errorf("artwork cache: item ID mismatch")
		}
		return item, err
	}
	item, err := read(ctx)
	if err != nil {
		return 0, err
	}
	wasLocked := slices.Contains(item.LockedFields, artworkLockField)
	cached := 0
	attempted := false
	var applyErr error
	for _, kind := range []string{"poster", "backdrop"} {
		current, e := read(ctx)
		if e != nil {
			applyErr = e
			break
		}
		raw := current.PosterURL
		if kind == "backdrop" {
			raw = current.BackdropURL
		}
		source := artworkCacheSource(raw, stashBase, backendBase)
		if source == "" {
			continue
		}
		var result struct {
			ContentID  string `json:"content_id"`
			StoredPath string `json:"stored_path"`
		}
		attempted = true
		e = siloRequest(ctx, base, key, http.MethodPost, "/api/v2/admin/items/"+url.PathEscape(id)+"/images/apply", map[string]string{"original_url": source, "type": kind, "provider_id": "stash"}, &result)
		if e != nil {
			applyErr = e
			break
		}
		if result.ContentID != id || result.StoredPath == "" {
			applyErr = fmt.Errorf("artwork cache: no stored revision returned")
			break
		}
		cached++
	}
	// Remove only the image lock added by this API, keeping all other current
	// locks. Cleanup must still run if a download is cancelled or fails.
	if attempted && !wasLocked {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		current, e := read(cleanup)
		if e == nil && slices.Contains(current.LockedFields, artworkLockField) {
			locks := make([]int, 0, len(current.LockedFields))
			for _, lock := range current.LockedFields {
				if lock != artworkLockField {
					locks = append(locks, lock)
				}
			}
			e = siloRequest(cleanup, base, key, http.MethodPatch, "/api/v2/admin/items/"+url.PathEscape(id)+"/metadata", map[string]any{"locked_fields": locks}, nil)
		}
		if e != nil {
			return cached, errors.Join(applyErr, fmt.Errorf("artwork cache: restore image lock: %w", e))
		}
	}
	return cached, applyErr
}
