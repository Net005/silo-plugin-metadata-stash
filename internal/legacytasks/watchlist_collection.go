package legacytasks

import (
	"context"
	"time"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
)

func (s *collectionSyncTaskServer) startWatchListCollectionSync() (<-chan collectionSyncResult, bool) {
	s.mu.Lock()
	if s.running == nil {
		s.running = map[string]bool{}
	}
	if s.running["watchlist-collection-sync"] {
		s.mu.Unlock()
		return nil, false
	}
	s.running["watchlist-collection-sync"] = true
	s.mu.Unlock()
	done := make(chan collectionSyncResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		summary, err := s.syncWatchListCollection(ctx)
		if err != nil {
			s.logger().Warn("WatchList collection recovery failed", "error", err)
		}
		s.mu.Lock()
		delete(s.running, "watchlist-collection-sync")
		s.mu.Unlock()
		done <- collectionSyncResult{summary, err}
	}()
	return done, true
}

func (s *collectionSyncTaskServer) pollWatchListCollection() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		s.startWatchListCollectionSync()
		<-ticker.C
	}
}

func (s *collectionSyncTaskServer) syncWatchListCollection(ctx context.Context) (map[string]any, error) {
	p := s.runtime.provider
	if !p.Configured() || p.SiloBaseURL() == "" || p.SiloAPIKey() == "" {
		return map[string]any{"status": "skipped", "reason": "connections not configured"}, nil
	}
	snapshot, err := p.LibrarySync(ctx)
	if err != nil {
		return nil, err
	}
	if !snapshot.WatchlistAuthoritative {
		return map[string]any{"status": "skipped", "reason": "Stash WatchList source unavailable"}, nil
	}
	client := provider.NewSiloClient(p.SiloBaseURL(), p.SiloAPIKey())
	client.SetCollectionArtworkResolver(p.ResolveCollectionArtwork)
	libraries, err := client.ListJAVBeaconMovieLibraries(ctx)
	if err != nil {
		return nil, err
	}
	changed := 0
	for _, library := range libraries {
		catalog, _, err := client.ListLibraryCatalogForProfile(ctx, library.ID)
		if err != nil {
			return nil, err
		}
		desired, resolvedPaths := exactWatchListMembers(snapshot, catalog)
		relevant := 0
		resolvedRelevant := 0
		for _, entry := range snapshot.Watchlist {
			if entry.Path != "" && sceneBelongsToLibrary(entry.Path, library) {
				relevant++
				if resolvedPaths[entry.Path] {
					resolvedRelevant++
				}
			}
		}
		allowRemovals := len(snapshot.Watchlist) == 0 || (relevant > 0 && resolvedRelevant == relevant)
		s.mu.Lock()
		collectionID := s.watchListCollectionID
		s.mu.Unlock()
		artByID := map[string]provider.CollectionArtwork{}
		for _, item := range catalog {
			artByID[item.ContentID] = provider.CollectionArtwork{MediaID: item.ContentID, PosterURL: item.PosterURL, BackdropURL: item.BackdropURL, ReleaseDate: item.ReleaseDate, AddedAt: item.AddedAt}
		}
		artwork := []provider.CollectionArtwork{}
		for _, id := range desired {
			if art, ok := artByID[id]; ok {
				artwork = append(artwork, art)
			}
		}
		n, complete, _, err := client.SyncExistingWatchList(ctx, library.ID, collectionID, desired, allowRemovals, 200-changed, artwork)
		changed += n
		if err != nil {
			return map[string]any{"status": "partial", "changes": changed}, err
		}
		if !complete || changed >= 200 {
			return map[string]any{"status": "partial", "changes": changed}, nil
		}
	}
	return map[string]any{"status": "ok", "changes": changed}, nil
}

// Exact path or linked artwork identities are required for automatic recovery.
// A title-only match could add an unrelated scene with the same name.
func exactWatchListMembers(snapshot *provider.LibrarySync, catalog []provider.CatalogItem) ([]string, map[string]bool) {
	byID := map[string]bool{}
	byScene := map[string][]string{}
	byRelease := map[int64][]string{}
	for _, item := range catalog {
		if item.Type != "movie" || item.ContentID == "" {
			continue
		}
		byID[item.ContentID] = true
		sceneID := stashSceneFromArtwork(item.PosterURL)
		if sceneID == "" {
			sceneID = stashSceneFromArtwork(item.BackdropURL)
		}
		if sceneID != "" {
			byScene[sceneID] = append(byScene[sceneID], item.ContentID)
		}
		releaseID := releaseFromArtwork(item.PosterURL)
		if releaseID == 0 {
			releaseID = releaseFromArtwork(item.BackdropURL)
		}
		if releaseID > 0 {
			byRelease[releaseID] = append(byRelease[releaseID], item.ContentID)
		}
	}
	result := []string{}
	resolvedPaths := map[string]bool{}
	seen := map[string]bool{}
	for _, entry := range snapshot.Watchlist {
		id := ""
		if entry.Path != "" {
			if candidate := siloLocalContentID(entry.Path); byID[candidate] {
				id = candidate
			}
		}
		if id == "" && entry.StashSceneID != "" {
			if matches := byScene[entry.StashSceneID]; len(matches) == 1 {
				id = matches[0]
			}
		}
		if id == "" && entry.ReleaseID > 0 {
			if matches := byRelease[entry.ReleaseID]; len(matches) == 1 {
				id = matches[0]
			}
		}
		if id != "" && entry.Path != "" {
			resolvedPaths[entry.Path] = true
		}
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result, resolvedPaths
}
