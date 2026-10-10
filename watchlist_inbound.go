package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	"sort"
	"strings"
	"time"
)

type inboundWatchlistChange struct {
	SceneID string    `json:"scene_id"`
	Desired *bool     `json:"desired"`
	Changed time.Time `json:"changed_at"`
	Paths   []string  `json:"paths"`
}

// Explicit events retain re-add intent even when recovery snapshots see the
// same membership before and after a quick remove/add.
func (s *runtimeServer) applyInboundWatchlist(ctx context.Context, change inboundWatchlistChange) error {
	if change.SceneID == "" || change.Desired == nil || change.Changed.IsZero() || len(change.Paths) == 0 {
		return fmt.Errorf("scene, desired membership, event time and exact file paths are required")
	}
	s.watchlistMu.Lock()
	defer s.watchlistMu.Unlock()
	rows, err := s.selectedWatchlistCollections(ctx)
	if err != nil {
		return err
	}
	s.mu.RLock()
	client := provider.NewSiloClient(s.siloBase, s.siloKey)
	s.mu.RUnlock()
	paths := map[string]bool{}
	for _, path := range change.Paths {
		if path != "" {
			paths[path] = true
		}
	}
	matched := 0
	for _, row := range rows {
		for path := range paths {
			hash := sha256.Sum256([]byte(path))
			media := "local-" + hex.EncodeToString(hash[:14])
			files, err := client.ItemFilePathsForLibrary(ctx, media, row.LibraryID)
			if err != nil { // A scene may have files absent from this Silo library.
				if strings.Contains(err.Error(), "HTTP 404") {
					continue
				}
				return err
			}
			if len(files) == 0 {
				continue
			}
			all, err := client.ItemFilePaths(ctx, media)
			if err != nil {
				return err
			}
			valid := len(all) > 0
			for _, file := range all {
				if !paths[file] {
					valid = false
				}
			}
			if !valid {
				continue
			}
			r, j, tag, err := s.watchlistState(ctx, row.ID)
			if err != nil {
				return err
			}
			if j.Version == 0 {
				j.Version = 1
				j.Baseline, err = s.watchlistMembers(ctx, row.ID)
				if err != nil {
					return err
				}
			}
			if old := j.LastActions[media]; !old.Changed.IsZero() && !change.Changed.After(old.Changed) {
				matched++
				continue
			}
			if pending, ok := j.Pending[media]; ok {
				if !change.Changed.After(pending.Changed) {
					matched++
					continue
				}
				delete(j.Pending, media)
			}
			if j.Incoming == nil {
				j.Incoming = map[string]watchlistIntent{}
			}
			if old, ok := j.Incoming[media]; ok && !change.Changed.After(old.Changed) {
				matched++
				continue
			}
			j.Incoming[media] = watchlistIntent{Desired: *change.Desired, SceneID: change.SceneID, Changed: change.Changed}
			// Persist before touching membership/order so the normal retry worker can finish a partial application.
			if err = s.saveWatchlistState(ctx, r, j, tag); err != nil {
				return err
			}
			if err = s.drainInboundWatchlist(ctx, row.ID); err != nil {
				return err
			}
			matched++
		}
	}
	if matched == 0 {
		return fmt.Errorf("no exact local item in the selected Watchlist libraries")
	}
	return nil
}

func (s *runtimeServer) drainInboundWatchlist(ctx context.Context, id string) error {
	r, j, tag, err := s.watchlistState(ctx, id)
	if err != nil {
		return err
	}
	if len(j.Incoming) == 0 {
		return nil
	}
	mediaIDs := make([]string, 0, len(j.Incoming))
	for media := range j.Incoming {
		mediaIDs = append(mediaIDs, media)
	}
	sort.Slice(mediaIDs, func(a, b int) bool { return j.Incoming[mediaIDs[a]].Changed.Before(j.Incoming[mediaIDs[b]].Changed) })
	for _, media := range mediaIDs {
		intent := j.Incoming[media]
		if old := j.LastActions[media]; old.Changed.After(intent.Changed) {
			delete(j.Incoming, media)
			continue
		}
		// Do not apply native Watchlist order: a Stash re-add is itself the newest action.
		if err = s.applyLocalWatchlist(ctx, id, media, intent.Desired); err != nil {
			return err
		}
		if intent.Desired {
			j.Baseline[media] = true
		} else {
			delete(j.Baseline, media)
		}
		j.LastActions[media] = watchlistActionVersion{Changed: intent.Changed, Desired: intent.Desired}
		delete(j.Incoming, media)
	}
	return s.saveWatchlistState(ctx, r, j, tag)
}
