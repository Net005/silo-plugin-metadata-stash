package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/hashicorp/go-hclog"
	"google.golang.org/protobuf/types/known/structpb"
)

type scheduledTaskServer struct {
	pluginv1.UnimplementedScheduledTaskServer
	runtime       *runtimeServer
	log           hclog.Logger
	mu            sync.Mutex
	running       bool
	cursor        string
	artworkMu     sync.Mutex
	artworkCursor string
	artworkRetry  map[string]time.Time
}

func (s *scheduledTaskServer) Run(ctx context.Context, req *pluginv1.RunScheduledTaskRequest) (*pluginv1.RunScheduledTaskResponse, error) {
	key := req.GetTaskKey()
	for _, name := range []string{"recommendation-sync", "recommendation-preview"} {
		if key == name || strings.HasSuffix(key, ":"+name) {
			if s.runtime.recommendations == nil {
				return taskOutput(map[string]any{"status": "not_configured"})
			}
			if name == "recommendation-sync" {
				cfg, base, key, _, _, _ := s.runtime.recommendations.configuration()
				if !cfg.Enabled {
					return taskOutput(map[string]any{"status": "disabled"})
				}
				state, _, _, err := s.runtime.recommendations.state(ctx, provider.NewSiloClient(base, key), cfg.Profile, "", false)
				if err != nil {
					return nil, err
				}
				if state.LastWeek == recommendationPeriod(time.Now(), cfg) {
					return taskOutput(map[string]any{"status": "already_completed_this_week", "detail": "Use the report page Build collections button for an explicit rebuild"})
				}
			}
			if !s.runtime.recommendations.start(name == "recommendation-preview") {
				return taskOutput(map[string]any{"status": "already_running"})
			}
			return taskOutput(map[string]any{"status": "started", "detail": "Read the durable recommendations report for the final result; this acknowledges worker admission only"})
		}
	}
	if key == "cache-artwork" || strings.HasSuffix(key, ":cache-artwork") {
		// Finish the task RPC before the admin API calls back into ImageResolver.
		go s.runArtworkRepair()
		return taskOutput(map[string]any{"status": "started", "detail": "Artwork caching continues in the background; progress is recorded in plugin logs"})
	}
	for _, task := range []string{"collection-sync", "watchlist-collection-sync", "metadata-refresh", "watched-sync", "play-backfill", "repair-matched"} {
		if key == task || strings.HasSuffix(key, ":"+task) {
			if s.runtime.legacy == nil {
				return taskOutput(map[string]any{"status": "not_configured"})
			}
			return s.runtime.legacy.Run(ctx, req)
		}
	}
	if key != "match-unmatched" && !strings.HasSuffix(key, ":match-unmatched") {
		return taskOutput(map[string]any{"status": "unknown_task"})
	}
	if !s.start() {
		return taskOutput(map[string]any{"status": "already_running"})
	}
	return taskOutput(map[string]any{"status": "started", "detail": "Exact Stash matching continues in the background"})
}
func taskOutput(value map[string]any) (*pluginv1.RunScheduledTaskResponse, error) {
	v, err := structpb.NewStruct(value)
	if err != nil {
		return nil, err
	}
	return &pluginv1.RunScheduledTaskResponse{Output: v}, nil
}
func (s *scheduledTaskServer) start() bool {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return false
	}
	s.running = true
	s.mu.Unlock()
	go func() {
		defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		summary, err := s.match(ctx)
		if err != nil {
			s.log.Warn("Stash exact match failed", "error", err)
		} else {
			s.log.Info("Stash exact match finished", "summary", summary)
		}
	}()
	return true
}
func (s *runtimeServer) pollMatching() {
	task := s.task
	go task.pollArtwork()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		task.start()
		<-ticker.C
	}
}
func (s *scheduledTaskServer) match(ctx context.Context) (map[string]any, error) {
	s.runtime.mu.RLock()
	base, key := s.runtime.siloBase, s.runtime.siloKey
	s.runtime.mu.RUnlock()
	if base == "" || key == "" {
		return map[string]any{"status": "skipped", "reason": "Silo admin URL and key not configured"}, nil
	}
	c := s.runtime.stash()
	if !c.configured() {
		return nil, fmt.Errorf("Stash connection not configured")
	}
	matched, skipped := 0, 0
	s.mu.Lock()
	cursor := s.cursor
	s.mu.Unlock()
	for page := 0; page < 5; page++ {
		path := "/api/v2/libraries/unmatched-items?limit=200"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var data struct {
			Items []struct {
				ContentID   string `json:"content_id"`
				ContentType string `json:"content_type"`
				LibraryID   string `json:"library_id"`
				Title       string `json:"title"`
			} `json:"items"`
			Page struct {
				HasMore    bool   `json:"has_more"`
				NextCursor string `json:"next_cursor"`
			} `json:"page"`
		}
		if err := siloRequest(ctx, base, key, http.MethodGet, path, nil, &data); err != nil {
			return nil, err
		}
		for _, item := range data.Items {
			if item.ContentType != "" && item.ContentType != "movie" {
				skipped++
				continue
			}
			files, err := provider.NewSiloClient(base, key).ItemFilePaths(ctx, item.ContentID)
			if err != nil {
				return nil, err
			}
			candidates := files
			if len(candidates) == 0 {
				candidates = []string{item.Title}
			}
			sceneID := ""
			ambiguous := false
			for _, candidate := range candidates {
				found, err := c.search(ctx, candidate)
				if err != nil {
					return nil, err
				}
				if len(found) > 1 {
					ambiguous = true
					break
				}
				if len(found) == 1 {
					if sceneID != "" && sceneID != found[0].ID {
						ambiguous = true
						break
					}
					sceneID = found[0].ID
				}
			}
			if ambiguous || sceneID == "" {
				skipped++
				continue
			}
			body := map[string]any{"library_id": item.LibraryID, "provider_ids": map[string]string{"stash": sceneID}}
			if err = siloRequest(ctx, base, key, http.MethodPost, "/api/v2/admin/items/"+url.PathEscape(item.ContentID)+"/match/apply", body, nil); err != nil {
				return nil, err
			}
			matched++
		}
		if !data.Page.HasMore || data.Page.NextCursor == "" || data.Page.NextCursor == cursor {
			s.mu.Lock()
			s.cursor = ""
			s.mu.Unlock()
			return map[string]any{"status": "complete", "matched": matched, "skipped": skipped}, nil
		}
		cursor = data.Page.NextCursor
	}
	s.mu.Lock()
	s.cursor = cursor
	s.mu.Unlock()
	return map[string]any{"status": "partial", "matched": matched, "skipped": skipped, "next_cursor": cursor}, nil
}
func siloRequest(ctx context.Context, base, key, method, path string, body any, out any) error {
	return siloProfileRequest(ctx, base, key, "", method, path, body, out)
}
func siloProfileRequest(ctx context.Context, base, key, profile, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	if profile != "" {
		req.Header.Set("X-Profile-Id", profile)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Silo HTTP %d on %s", resp.StatusCode, path)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}
