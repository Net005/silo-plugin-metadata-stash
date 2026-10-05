package main

import (
	"context"
	"fmt"
	"time"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	rec "github.com/Net005/silo-plugin-metadata-stash/internal/recommendations"
)

// prune removes invalid discoveries without reranking or spending API tokens.
// Its reads finish before writes, and use the same reset guard as weekly runs.
func (s *recommendationServer) prune(ctx context.Context) error {
	cfg, base, key, _, stash, _ := s.configuration()
	if !cfg.Enabled || cfg.Preview || stash == nil {
		return nil
	}
	client := provider.NewSiloClient(base, key)
	state, record, tag, err := s.state(ctx, client, cfg.Profile, "", false)
	if err != nil || record.ID == "" {
		return err
	}
	if state.LeaseUntil.After(time.Now()) {
		return nil
	}
	if state.LastWeek == "" {
		return nil
	}
	rows, err := recommendationScenes(ctx, stash)
	if err != nil {
		return err
	}
	if rec.HistoryDrop(state.Totals, recommendationTotals(rows)) {
		return fmt.Errorf("activity drop detected; pruning skipped")
	}
	scenes := map[string]rec.Scene{}
	for _, x := range rows {
		scenes[x.ID] = x
	}
	type change struct {
		record        provider.RecommendationRecord
		ids           []string
		library, kind string
		sceneIDs      []string
	}
	changes := []change{}
	records, err := client.RecommendationRecords(ctx)
	if err != nil {
		return err
	}
	bySlug := map[string]provider.RecommendationRecord{}
	for _, r := range records {
		bySlug[r.Slug] = r
	}
	for library, kinds := range state.Previous {
		catalog, e := client.ListRecommendationCatalog(ctx, library, cfg.Profile)
		if e != nil {
			return e
		}
		local, _, _ := matchRecommendationScenes(rows, catalog, library)
		verified := map[string]rec.Scene{}
		for _, x := range local {
			verified[x.ID] = x
		}
		if len(verified) == 0 {
			return fmt.Errorf("library %s lost all matches; pruning skipped", library)
		}
		for kind, ids := range kinds {
			current := bySlug[provider.RecommendationSlug(cfg.Profile, library, kind)]
			if current.ID == "" {
				continue
			}
			wanted, sceneIDs := []string{}, []string{}
			for _, id := range ids {
				x, ok := verified[id]
				if !ok || state.Dismissed[library][id] {
					continue
				}
				if kind != "top-rated" && kind != "revisit" && kind != "watchlist" && (scenes[id].Plays > 0 || scenes[id].O > 0 || x.SiloPlayed) {
					continue
				}
				wanted = append(wanted, x.MediaID)
				sceneIDs = append(sceneIDs, id)
			}
			if len(sceneIDs) != len(ids) {
				changes = append(changes, change{current, wanted, library, kind, sceneIDs})
			}
		}
	}
	if len(changes) == 0 {
		return nil
	}
	state.Lease = "prune"
	state.LeaseUntil = time.Now().Add(20 * time.Minute)
	record, tag, err = s.save(ctx, client, record, tag, state)
	if err != nil {
		return err
	}
	defer func() {
		state.Lease = ""
		state.LeaseUntil = time.Time{}
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, _, e := s.save(cleanup, client, record, tag, state); e != nil {
			s.runtime.task.log.Warn("Recommendation prune report save failed", "error", e)
		}
	}()
	for _, ch := range changes {
		if err = client.ReconcileRecommendation(ctx, ch.record, ch.ids); err != nil {
			return err
		}
		state.Previous[ch.library][ch.kind] = ch.sceneIDs
		allowed := map[string]bool{}
		for _, id := range ch.sceneIDs {
			allowed[id] = true
		}
		for i := range state.Report.Libraries {
			r := &state.Report.Libraries[i]
			if r.LibraryID != ch.library {
				continue
			}
			for j := range r.Collections {
				c := &r.Collections[j]
				if c.Kind != ch.kind {
					continue
				}
				picks := []rec.Pick{}
				for _, p := range c.Picks {
					if allowed[p.ID] {
						picks = append(picks, p)
					}
				}
				c.Picks = picks
			}
		}
	}
	return nil
}
