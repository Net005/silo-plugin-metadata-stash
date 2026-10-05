package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	rec "github.com/Net005/silo-plugin-metadata-stash/internal/recommendations"
)

type recommendationArchiveStats struct {
	Scenes          int `json:"archived_scenes"`
	Events          int `json:"archived_events"`
	AddedEvents     int `json:"matched_events_added"`
	HistoryOnly     int `json:"history_only_releases"`
	LibraryAssigned int `json:"history_only_library_assigned"`
	Unresolved      int `json:"unresolved_active_scenes"`
}
type recommendationArchiveScene struct {
	ID      string `json:"stash_scene_id"`
	Release int64  `json:"release_id"`
	Title   string `json:"title"`
	Code    string `json:"video_id"`
	Path    string `json:"file_path"`
	Plays   int    `json:"play_count"`
	O       int    `json:"orgasm_count"`
}
type recommendationArchiveEvent struct {
	ID   string `json:"stash_scene_id"`
	Type string `json:"type"`
	At   string `json:"occurred_at"`
}
type recommendationArchive struct {
	Version int                          `json:"version"`
	Scenes  []recommendationArchiveScene `json:"scenes"`
	Events  []recommendationArchiveEvent `json:"events"`
}
type recommendationRelease struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	Code      string   `json:"video_id"`
	FilePath  string   `json:"stash_file_path"`
	Studio    string   `json:"studio"`
	Actresses []string `json:"actresses"`
	Actress   string   `json:"actress"`
	Genres    []string `json:"genres"`
}

func archiveRead(ctx context.Context, art *artworkClient, endpoint string, target any) error {
	req, e := http.NewRequestWithContext(ctx, "GET", art.base+endpoint, nil)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+art.key)
	resp, e := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if e != nil {
		return fmt.Errorf("archive unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("archive HTTP %d", resp.StatusCode)
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(target); e != nil {
		return fmt.Errorf("archive response invalid")
	}
	return nil
}
func archiveEvent(row *rec.Scene, event recommendationArchiveEvent, now time.Time) bool {
	at, e := time.Parse(time.RFC3339, event.At)
	if e != nil || at.After(now) {
		return false
	}
	dst := &row.PlayHistory
	if event.Type == "orgasm" {
		dst = &row.OHistory
	} else if event.Type != "play" {
		return false
	}
	for _, raw := range *dst {
		t, _ := time.Parse(time.RFC3339, raw)
		if t.Equal(at) {
			return false
		}
	}
	*dst = append(*dst, at.UTC().Format(time.RFC3339))
	return true
}
func archiveCounters(row *rec.Scene) {
	row.Plays = max(row.Plays, len(row.PlayHistory))
	row.O = max(row.O, len(row.OHistory))
	for _, at := range row.PlayHistory {
		if eventTimeForArchive(at).After(eventTimeForArchive(row.LastPlayed)) {
			row.LastPlayed = at
		}
	}
}
func eventTimeForArchive(raw string) time.Time { t, _ := time.Parse(time.RFC3339, raw); return t }
func archiveLibrary(file string, libraries []provider.MovieLibrary) string {
	file = path.Clean(strings.ReplaceAll(file, "\\", "/"))
	if file == "." {
		return ""
	}
	ids := map[string]bool{}
	for _, lib := range libraries {
		for _, root := range lib.Paths {
			root = path.Clean(strings.ReplaceAll(root, "\\", "/"))
			if root != "." && (file == root || strings.HasPrefix(file, strings.TrimSuffix(root, "/")+"/")) {
				ids[lib.ID] = true
			}
		}
	}
	if len(ids) != 1 {
		return ""
	}
	for id := range ids {
		return id
	}
	return ""
}

// Match archived metadata to existing entity identities only when an exact,
// case-insensitive name or performer alias identifies a single Stash entity.
func archiveEntities(scenes []rec.Scene) map[string]map[string]rec.Entity {
	out := map[string]map[string]rec.Entity{"studio": {}, "performer": {}, "tag": {}}
	add := func(kind string, e rec.Entity) {
		for _, name := range append([]string{e.Name}, e.Aliases...) {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			old, ok := out[kind][name]
			if ok && old.ID != e.ID {
				out[kind][name] = rec.Entity{}
			} else if !ok {
				out[kind][name] = e
			}
		}
	}
	for _, s := range scenes {
		if s.Studio != nil {
			add("studio", *s.Studio)
		}
		for _, e := range s.Performers {
			add("performer", e)
		}
		for _, e := range s.Tags {
			add("tag", e)
		}
	}
	return out
}
func archiveReleaseScene(r recommendationRelease, entities map[string]map[string]rec.Entity) rec.Scene {
	row := rec.Scene{ID: "archive-release:" + strconv.FormatInt(r.ID, 10), Title: r.Title, Code: r.Code}
	lookup := func(kind, name string) rec.Entity { return entities[kind][strings.ToLower(strings.TrimSpace(name))] }
	if e := lookup("studio", r.Studio); e.ID != "" {
		row.Studio = &e
	}
	names := r.Actresses
	if len(names) == 0 && r.Actress != "" {
		names = []string{r.Actress}
	}
	seen := map[string]bool{}
	for _, name := range names {
		e := lookup("performer", name)
		if e.ID != "" && !seen[e.ID] {
			row.Performers = append(row.Performers, e)
			seen[e.ID] = true
		}
	}
	for _, name := range r.Genres {
		if e := lookup("tag", name); e.ID != "" {
			row.Tags = append(row.Tags, e)
		}
	}
	return row
}
func mergeRecommendationArchive(ctx context.Context, art *artworkClient, scenes []rec.Scene, libraries []provider.MovieLibrary) ([]rec.Scene, []rec.Scene, recommendationArchiveStats, error) {
	stats := recommendationArchiveStats{}
	if art == nil || art.base == "" || art.key == "" {
		return scenes, nil, stats, fmt.Errorf("JAVBeacon connection not configured")
	}
	var a recommendationArchive
	if e := archiveRead(ctx, art, "/api/stash/history/export", &a); e != nil {
		return scenes, nil, stats, e
	}
	if a.Version != 1 {
		return scenes, nil, stats, fmt.Errorf("unsupported archive version")
	}
	stats.Scenes = len(a.Scenes)
	stats.Events = len(a.Events)
	events := map[string][]recommendationArchiveEvent{}
	for _, e := range a.Events {
		events[e.ID] = append(events[e.ID], e)
	}
	byID := map[string]int{}
	byCode := map[string][]int{}
	byPath := map[string]map[int]bool{}
	for i, s := range scenes {
		byID[s.ID] = i
		if code := strings.ToUpper(strings.TrimSpace(s.Code)); code != "" {
			byCode[code] = append(byCode[code], i)
		}
		for _, f := range s.Files {
			if byPath[f.Path] == nil {
				byPath[f.Path] = map[int]bool{}
			}
			byPath[f.Path][i] = true
		}
	}
	missing := map[int64][]recommendationArchiveScene{}
	matchedReleases := map[int64]map[int]bool{}
	now := time.Now()
	merge := func(i int, old recommendationArchiveScene) {
		scenes[i].Plays = max(scenes[i].Plays, old.Plays)
		scenes[i].O = max(scenes[i].O, old.O)
		for _, e := range events[old.ID] {
			if archiveEvent(&scenes[i], e, now) {
				stats.AddedEvents++
			}
		}
		archiveCounters(&scenes[i])
	}
	for _, old := range a.Scenes {
		if old.Plays == 0 && old.O == 0 && len(events[old.ID]) == 0 {
			continue
		}
		i, ok := byID[old.ID]
		ok = ok && old.Title != "" && strings.EqualFold(old.Title, scenes[i].Title) && (old.Code == "" || scenes[i].Code == "" || strings.EqualFold(old.Code, scenes[i].Code))
		if !ok && old.Path != "" && len(byPath[old.Path]) == 1 {
			for n := range byPath[old.Path] {
				i = n
				ok = true
			}
		}
		if !ok {
			if ids := byCode[strings.ToUpper(strings.TrimSpace(old.Code))]; old.Code != "" && len(ids) == 1 {
				i = ids[0]
				ok = true
			}
		}
		if ok {
			merge(i, old)
			if old.Release > 0 {
				if matchedReleases[old.Release] == nil {
					matchedReleases[old.Release] = map[int]bool{}
				}
				matchedReleases[old.Release][i] = true
			}
			continue
		}
		if old.Release > 0 {
			missing[old.Release] = append(missing[old.Release], old)
		} else {
			stats.Unresolved++
		}
	}
	// A reimport may have a different scene ID/path. A release already matched
	// uniquely above is the same feedback source, never a second history row.
	for id, olds := range missing {
		if len(matchedReleases[id]) == 1 {
			for i := range matchedReleases[id] {
				for _, old := range olds {
					merge(i, old)
				}
			}
			delete(missing, id)
		}
	}
	for id, olds := range missing {
		if len(matchedReleases[id]) > 1 {
			stats.Unresolved += len(olds)
			delete(missing, id)
		}
	}
	entities := archiveEntities(scenes)
	results := map[int64]recommendationRelease{}
	var mu sync.Mutex
	var firstErr error
	jobs := make(chan int64)
	var wg sync.WaitGroup
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				var r recommendationRelease
				e := archiveRead(ctx, art, "/api/releases/"+strconv.FormatInt(id, 10), &r)
				mu.Lock()
				if e != nil {
					if e.Error() != "archive HTTP 404" && firstErr == nil {
						firstErr = e
					}
				} else if r.ID == id {
					results[id] = r
				}
				mu.Unlock()
			}
		}()
	}
	ids := []int64{}
	for id := range missing {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return scenes, nil, stats, firstErr
	}
	history := []rec.Scene{}
	for _, id := range ids {
		olds := missing[id]
		r, ok := results[id]
		if !ok {
			stats.Unresolved += len(olds)
			continue
		}
		row := archiveReleaseScene(r, entities)
		if row.Studio == nil && len(row.Performers) == 0 && len(row.Tags) == 0 {
			stats.Unresolved += len(olds)
			continue
		}
		assigned := map[string]bool{}
		for _, old := range olds {
			row.Plays = max(row.Plays, old.Plays)
			row.O = max(row.O, old.O)
			for _, e := range events[old.ID] {
				archiveEvent(&row, e, now)
			}
			if lib := archiveLibrary(old.Path, libraries); lib != "" {
				assigned[lib] = true
			}
		}
		archiveCounters(&row)
		if len(assigned) == 0 {
			if lib := archiveLibrary(r.FilePath, libraries); lib != "" {
				assigned[lib] = true
			}
		}
		if len(assigned) == 1 {
			for lib := range assigned {
				row.LibraryID = lib
			}
			stats.LibraryAssigned++
		}
		history = append(history, row)
		stats.HistoryOnly++
	}
	return scenes, history, stats, nil
}
