package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
)

// previewCompletedPlays never writes activity. A missing timestamp is only a
// candidate: Stash has no durable cross-process session idempotency contract.
func (s *scheduledTaskServer) previewCompletedPlays(ctx context.Context) (map[string]any, error) {
	s.runtime.mu.RLock()
	base, key, profile := s.runtime.siloBase, s.runtime.siloKey, s.runtime.playtimeProfile
	if profile == "" {
		profile = s.runtime.recommendationConfig.Profile
	}
	s.runtime.mu.RUnlock()
	c := s.runtime.stash()
	if base == "" || key == "" || profile == "" || !c.configured() {
		return map[string]any{"status": "not_configured", "read_only": true, "detail": "Configure Stash, Silo admin access and the active playtime or recommendation owner profile"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	silo := provider.NewSiloClient(base, key)
	// Fail closed if any history page is unavailable or the cursor is invalid.
	attempts, err := silo.CompletedPlaybackHistory(ctx, profile)
	if err != nil {
		return nil, fmt.Errorf("playback preview: unable to read complete Silo history")
	}
	unique := make(map[string]provider.CompletedPlayback)
	rows := make([]provider.CompletedPlayback, 0, len(attempts))
	duplicates, skipped := 0, 0
	for _, row := range attempts {
		if !row.Completed || row.MediaType != "movie" || row.SessionID == "" || row.MediaItemID == "" || row.EndedAt.IsZero() {
			skipped++
			continue
		}
		if old, ok := unique[row.SessionID]; ok {
			if old.MediaItemID != row.MediaItemID || !old.EndedAt.Equal(row.EndedAt) {
				return nil, fmt.Errorf("playback preview: conflicting session identities")
			}
			if row.PlayedSeconds != nil && (old.PlayedSeconds == nil || *row.PlayedSeconds > *old.PlayedSeconds) {
				// Copies finalized on several replicas: keep the largest measurement.
				old.PlayedSeconds = row.PlayedSeconds
				unique[row.SessionID] = old
				for i := range rows {
					if rows[i].SessionID == row.SessionID {
						rows[i] = old
					}
				}
			}
			duplicates++
			continue
		}
		unique[row.SessionID] = row
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].EndedAt.Equal(rows[j].EndedAt) {
			return rows[i].SessionID < rows[j].SessionID
		}
		return rows[i].EndedAt.After(rows[j].EndedAt)
	})
	total := len(rows)
	const limit = 100
	if len(rows) > limit {
		rows = rows[:limit]
	}
	missing, present, unresolved, excluded, incomplete, unmeasured := 0, 0, 0, 0, 0, 0
	missingWatch := 0.0
	results := make([]any, 0, len(rows))
	// Cache only inside this invocation: repeated sessions compare against one
	// fresh history read, and the next preview must observe any new Stash plays.
	histories := make(map[string]*scene)
	for _, row := range rows {
		result := map[string]any{
			"session_id": row.SessionID, "media_item_id": row.MediaItemID,
			"ended_at": row.EndedAt.UTC().Format(time.RFC3339Nano), "outcome": "unresolved",
		}
		measured := row.PlayedSeconds != nil && *row.PlayedSeconds >= 0
		if measured {
			result["played_seconds"] = *row.PlayedSeconds
		}
		results = append(results, result)
		paths, err := silo.ItemFilePaths(ctx, row.MediaItemID)
		if err != nil {
			return nil, fmt.Errorf("playback preview: unable to read Silo file identity")
		}
		id, err := c.sceneIDForExactPaths(ctx, paths)
		if err != nil {
			return nil, fmt.Errorf("playback preview: Stash identity lookup failed or is ambiguous")
		}
		if id == "" {
			unresolved++
			continue
		}
		result["stash_scene_id"] = id
		if c.excludedScenes[id] {
			result["outcome"] = "excluded"
			excluded++
			continue
		}
		scene, loaded := histories[id]
		if !loaded {
			scene, err = c.findScene(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("playback preview: unable to read fresh Stash history")
			}
			histories[id] = scene
		}
		if scene == nil {
			unresolved++
			continue
		}
		if scene.hasPlayAt(row.EndedAt) {
			result["outcome"] = "already_present"
			present++
		} else if scene.PlayCount > len(scene.PlayHistory) {
			result["outcome"] = "incomplete_history"
			incomplete++
		} else {
			result["outcome"] = "missing"
			missing++
			if measured {
				missingWatch += *row.PlayedSeconds
			} else {
				unmeasured++
			}
		}
	}
	status := "complete"
	if total > len(rows) {
		status = "partial"
	}
	return map[string]any{
		"status": status, "read_only": true, "eligible": total, "checked": len(rows), "remaining": total - len(rows),
		"missing": missing, "already_present": present, "unresolved": unresolved, "excluded": excluded, "incomplete_history": incomplete, "duplicates": duplicates, "skipped": skipped,
		"missing_watch_seconds": missingWatch, "missing_unmeasured": unmeasured,
		"results": results,
		"detail":  "Newest 100 unique completed movie sessions at most; exact file identity and fresh Stash history, with a 30-second timestamp tolerance. Missing plays are candidates only; missing_watch_seconds sums Silo's measured watch time for them, and missing_unmeasured counts those recorded before Silo measured watch time. No plays, duration, resume positions or O counts are written.",
	}, nil
}
