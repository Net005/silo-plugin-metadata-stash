package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const watchlistJournalKey = "stash_watchlist_outbox"

var errOutsideStashWatchlist = fmt.Errorf("item is outside the selected Stash Watchlist libraries")

type watchlistIntent struct {
	Desired bool      `json:"desired"`
	SceneID string    `json:"scene_id,omitempty"`
	Changed time.Time `json:"changed_at"`
}
type watchlistActionVersion struct {
	Changed time.Time `json:"changed_at"`
	Desired bool      `json:"desired"`
}
type watchlistJournal struct {
	LastActions map[string]watchlistActionVersion `json:"last_actions,omitempty"`
	Version     int                               `json:"version"`
	Baseline    map[string]bool                   `json:"baseline"`
	Pending     map[string]watchlistIntent        `json:"pending"`
}
type watchlistCollection struct {
	ID           string                     `json:"id"`
	Title        string                     `json:"title"`
	Slug         string                     `json:"slug"`
	LibraryID    string                     `json:"library_id"`
	Description  string                     `json:"description"`
	SourceConfig map[string]json.RawMessage `json:"source_config"`
}

func (s *runtimeServer) siloWatchlistRequest(ctx context.Context, method, path string, payload, target any, etag string) (string, error) {
	s.mu.RLock()
	base, key, profile := s.siloBase, s.siloKey, s.recommendationConfig.Profile
	s.mu.RUnlock()
	if base == "" || key == "" {
		return "", fmt.Errorf("Silo connection not configured")
	}
	var body io.Reader
	if payload != nil {
		raw, e := json.Marshal(payload)
		if e != nil {
			return "", e
		}
		body = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, base+path, body)
	if e != nil {
		return "", e
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("X-Profile-Id", profile)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	resp, e := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Silo Watchlist %s HTTP %d", method, resp.StatusCode)
	}
	if target != nil {
		e = json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(target)
	}
	return resp.Header.Get("ETag"), e
}
func (s *runtimeServer) selectedWatchlistCollections(ctx context.Context) ([]watchlistCollection, error) {
	s.mu.RLock()
	selection, prefix, legacy := s.stashFilters, s.stashPrefix, s.legacy
	s.mu.RUnlock()
	if selection == "" || legacy == nil {
		return nil, nil
	}
	filters, e := legacy.Provider().StashSavedFilters(ctx, selection)
	if e != nil {
		return nil, e
	}
	name, id := "", ""
	for _, f := range filters {
		if strings.EqualFold(f.Name, "Watchlist") {
			if id != "" {
				return nil, fmt.Errorf("ambiguous selected Watchlist")
			}
			name, id = prefix+f.Name, f.ID
		}
	}
	if id == "" {
		return nil, nil
	}
	var response struct {
		Items []watchlistCollection `json:"items"`
	}
	_, e = s.siloWatchlistRequest(ctx, "GET", "/api/v2/admin/collections", nil, &response, "")
	if e != nil {
		return nil, e
	}
	out := []watchlistCollection{}
	libs := map[string]bool{}
	for _, r := range response.Items {
		if r.Title == name && r.Slug == "javbeacon-stash-preset-"+url.PathEscape(id)+"-library-"+strings.ToLower(r.LibraryID) && strings.HasPrefix(r.Description, "Managed by JAVBeacon metadata plugin.") {
			if libs[r.LibraryID] {
				return nil, fmt.Errorf("ambiguous Watchlist library")
			}
			libs[r.LibraryID] = true
			out = append(out, r)
		}
	}
	return out, nil
}
func (s *runtimeServer) watchlistMembers(ctx context.Context, id string) (map[string]bool, error) {
	out := map[string]bool{}
	cursor := ""
	for n := 0; n < 100; n++ {
		path := "/api/v2/admin/collections/" + url.PathEscape(id) + "/items?limit=200"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var data struct {
			Items []struct {
				ID string `json:"media_item_id"`
			} `json:"items"`
			Page struct {
				More bool   `json:"has_more"`
				Next string `json:"next_cursor"`
			} `json:"page"`
		}
		if _, e := s.siloWatchlistRequest(ctx, "GET", path, nil, &data, ""); e != nil {
			return nil, e
		}
		for _, x := range data.Items {
			out[x.ID] = true
		}
		if !data.Page.More {
			return out, nil
		}
		if data.Page.Next == "" || data.Page.Next == cursor {
			return nil, fmt.Errorf("Watchlist pagination stalled")
		}
		cursor = data.Page.Next
	}
	return nil, fmt.Errorf("Watchlist pagination limit")
}
func (s *runtimeServer) watchlistState(ctx context.Context, id string) (watchlistCollection, watchlistJournal, string, error) {
	var r watchlistCollection
	tag, e := s.siloWatchlistRequest(ctx, "GET", "/api/v2/admin/collections/"+url.PathEscape(id), nil, &r, "")
	j := watchlistJournal{Baseline: map[string]bool{}, Pending: map[string]watchlistIntent{}}
	if e == nil {
		if raw := r.SourceConfig[watchlistJournalKey]; len(raw) > 0 {
			e = json.Unmarshal(raw, &j)
			if e == nil && j.Version != 1 {
				e = fmt.Errorf("unknown Watchlist journal version")
			}
		}
	}
	if j.LastActions == nil {
		j.LastActions = map[string]watchlistActionVersion{}
	}
	if j.Baseline == nil {
		j.Baseline = map[string]bool{}
	}
	if j.Pending == nil {
		j.Pending = map[string]watchlistIntent{}
	}
	return r, j, tag, e
}
func (s *runtimeServer) saveWatchlistState(ctx context.Context, r watchlistCollection, j watchlistJournal, tag string) error {
	if tag == "" {
		return fmt.Errorf("Watchlist ETag missing")
	}
	if r.SourceConfig == nil {
		r.SourceConfig = map[string]json.RawMessage{}
	}
	r.SourceConfig[watchlistJournalKey], _ = json.Marshal(j)
	_, e := s.siloWatchlistRequest(ctx, "PATCH", "/api/v2/admin/collections/"+url.PathEscape(r.ID), map[string]any{"source_config": r.SourceConfig}, nil, tag)
	return e
}
func (s *runtimeServer) applyLocalWatchlist(ctx context.Context, id, media string, desired bool) error {
	members, e := s.watchlistMembers(ctx, id)
	if e != nil {
		return e
	}
	if members[media] == desired {
		return nil
	}
	method := "DELETE"
	if desired {
		method = "PUT"
	}
	var payload any
	if desired {
		payload = map[string]int{"position": len(members)}
	}
	_, e = s.siloWatchlistRequest(ctx, method, "/api/v2/admin/collections/"+url.PathEscape(id)+"/items/"+url.PathEscape(media), payload, nil, "")
	return e
}
func (c *stashClient) watchlistTag(ctx context.Context) (string, error) {
	var data struct {
		Configuration struct {
			Plugins map[string]map[string]any `json:"plugins"`
		} `json:"configuration"`
	}
	if e := c.graphql(ctx, `query{configuration{plugins(include:["stash-silo-companion"])}}`, nil, &data); e != nil {
		return "", e
	}
	v := data.Configuration.Plugins["stash-silo-companion"]["watchlist_tag_id"]
	id := strings.TrimSpace(fmt.Sprint(v))
	if v == nil || id == "" {
		return "", fmt.Errorf("configure the Stash Silo companion Watchlist tag ID")
	}
	return id, nil
}
func (c *stashClient) setWatchlistTag(ctx context.Context, id, tag string, desired bool) error {
	var data struct {
		Scene *scene `json:"findScene"`
	}
	if e := c.graphql(ctx, `query($id:ID!){findScene(id:$id){id tags{id name}}}`, map[string]any{"id": id}, &data); e != nil {
		return e
	}
	if data.Scene == nil {
		return fmt.Errorf("Stash Watchlist scene missing")
	}
	has := false
	for _, t := range data.Scene.Tags {
		if t.ID == tag {
			has = true
		}
	}
	if has == desired {
		return nil
	}
	mode := "REMOVE"
	if desired {
		mode = "ADD"
	}
	var out any
	return c.graphql(ctx, `mutation($input:BulkSceneUpdateInput!){bulkSceneUpdate(input:$input){id}}`, map[string]any{"input": map[string]any{"ids": []string{id}, "tag_ids": map[string]any{"ids": []string{tag}, "mode": mode}}}, &out)
}
func (s *runtimeServer) resolveWatchlistScene(ctx context.Context, media string) (string, error) {
	s.mu.RLock()
	base, key := s.siloBase, s.siloKey
	s.mu.RUnlock()
	paths, e := provider.NewSiloClient(base, key).ItemFilePaths(ctx, media)
	if e != nil {
		return "", e
	}
	id, e := s.stash().sceneIDForExactPaths(ctx, paths)
	if e == nil && id == "" {
		e = fmt.Errorf("no exact Stash Watchlist file match")
	}
	return id, e
}

// Captures local edits durably before attempting Stash. An error blocks inbound
// reconciliation so an unavailable remote cannot erase the local user's choice.
func (s *runtimeServer) backfillWatchlist(ctx context.Context) (int, error) {
	s.watchlistMu.Lock()
	defer s.watchlistMu.Unlock()
	return s.backfillWatchlistLocked(ctx)
}
func (s *runtimeServer) backfillWatchlistLocked(ctx context.Context) (int, error) {
	rows, e := s.selectedWatchlistCollections(ctx)
	if e != nil {
		return 0, e
	}
	done := 0
	tagID := ""
	for _, row := range rows {
		r, j, tag, e := s.watchlistState(ctx, row.ID)
		if e != nil {
			return done, e
		}
		members, e := s.watchlistMembers(ctx, row.ID)
		if e != nil {
			return done, e
		}
		if j.Version == 0 {
			j.Version = 1
			j.Baseline = members
			if e = s.saveWatchlistState(ctx, r, j, tag); e != nil {
				return done, e
			}
			continue
		}
		changed := false
		for id := range members {
			if !j.Baseline[id] && j.Pending[id].Changed.IsZero() {
				j.Pending[id] = watchlistIntent{Desired: true, Changed: time.Now()}
				j.LastActions[id] = watchlistActionVersion{j.Pending[id].Changed, true}
				changed = true
			}
		}
		for id := range j.Baseline {
			if !members[id] && j.Pending[id].Changed.IsZero() {
				j.Pending[id] = watchlistIntent{Desired: false, Changed: time.Now()}
				j.LastActions[id] = watchlistActionVersion{j.Pending[id].Changed, false}
				changed = true
			}
		}
		if changed {
			j.Baseline = members
			if e = s.saveWatchlistState(ctx, r, j, tag); e != nil {
				return done, e
			}
		}
		for media, intent := range j.Pending {
			if e = s.applyLocalWatchlist(ctx, row.ID, media, intent.Desired); e != nil {
				return done, e
			}
			if intent.SceneID == "" {
				intent.SceneID, e = s.resolveWatchlistScene(ctx, media)
				if e != nil {
					return done, e
				}
			}
			if tagID == "" {
				tagID, e = s.stash().watchlistTag(ctx)
				if e != nil {
					return done, e
				}
			}
			if e = s.stash().setWatchlistTag(ctx, intent.SceneID, tagID, intent.Desired); e != nil {
				return done, e
			}
			if s.legacy != nil {
				s.legacy.Provider().InvalidateStashSavedFilters()
			}
			// Re-read the CAS validator after membership/companion hook writes.
			r, j, tag, e = s.watchlistState(ctx, row.ID)
			if e != nil {
				return done, e
			}
			delete(j.Pending, media)
			// Acknowledge only this intent, preserving concurrent local edits.
			if intent.Desired {
				j.Baseline[media] = true
			} else {
				delete(j.Baseline, media)
			}
			if e = s.saveWatchlistState(ctx, r, j, tag); e != nil {
				return done, e
			}
			done++
		}
	}
	return done, nil
}
func (s *runtimeServer) applyWatchlistEvent(ctx context.Context, event *pluginv1.WatchSyncEvent) error {
	s.watchlistMu.Lock()
	defer s.watchlistMu.Unlock()
	media := event.GetMedia().GetMediaItemId()
	if media == "" {
		return fmt.Errorf("Watchlist event has no Silo item")
	}
	rows, e := s.selectedWatchlistCollections(ctx)
	if e != nil {
		return e
	}
	if len(rows) == 0 {
		return fmt.Errorf("select the Stash Watchlist saved filter first")
	}
	var files struct {
		Items []struct {
			Library string `json:"library_id"`
		} `json:"items"`
	}
	_, e = s.siloWatchlistRequest(ctx, "GET", "/api/v2/admin/items/"+url.PathEscape(media)+"/files?limit=200", nil, &files, "")
	if e != nil {
		return e
	}
	libs := map[string]bool{}
	for _, f := range files.Items {
		libs[f.Library] = true
	}
	matched := 0
	desired := event.GetOperation() == pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST
	for _, row := range rows {
		if !libs[row.LibraryID] {
			continue
		}
		r, j, tag, e := s.watchlistState(ctx, row.ID)
		if e != nil {
			return e
		}
		if j.Version == 0 {
			j.Version = 1
			j.Baseline, e = s.watchlistMembers(ctx, row.ID)
			if e != nil {
				return e
			}
		}
		intent := watchlistIntent{Desired: desired, SceneID: stashPlaybackID(event.GetMedia().GetExternalIds()[capabilityID]), Changed: eventTime(event)}
		if old, ok := j.LastActions[media]; ok && (old.Changed.After(intent.Changed) || (old.Changed.Equal(intent.Changed) && old.Desired == desired && len(j.Pending) == 0)) {
			matched++
			continue
		}
		j.LastActions[media] = watchlistActionVersion{intent.Changed, desired}
		if old, ok := j.Pending[media]; ok && old.Changed.After(intent.Changed) {
			continue
		}
		j.Pending[media] = intent
		j.Baseline[media] = desired
		if !desired {
			delete(j.Baseline, media)
		}
		if e = s.saveWatchlistState(ctx, r, j, tag); e != nil {
			return e
		}
		if e = s.applyLocalWatchlist(ctx, row.ID, media, desired); e != nil {
			return e
		}
		matched++
	}
	if matched == 0 {
		return errOutsideStashWatchlist
	}
	_, e = s.backfillWatchlistLocked(ctx)
	return e
}
func (s *runtimeServer) pollWatchlistExports() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		_, e := s.backfillWatchlist(ctx)
		cancel()
		if e != nil && s.task != nil {
			s.task.log.Warn("Watchlist export retry pending", "error", e)
		}
		<-ticker.C
	}
}
