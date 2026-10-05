package main

import (
	"context"
	"encoding/json"
	"fmt"
	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	"sort"
	"strings"
	"time"
)

const reportArchiveKey = "stash_recommendation_report_archive"

type recommendationReportIndex struct {
	ID       string    `json:"id"`
	Started  time.Time `json:"started_at"`
	Finished time.Time `json:"finished_at"`
	Week     string    `json:"week"`
	Status   string    `json:"status"`
	Preview  bool      `json:"preview"`
}

func reportWithinRetention(started, now time.Time) bool {
	return !started.IsZero() && !started.Before(now.AddDate(0, -6, 0))
}

// Each run has its own compressed hidden record, avoiding an ever-growing
// control-state document and Silo's 1 MiB write limit. Picks and reasons are kept.
func archiveRecommendationReport(ctx context.Context, c *provider.SiloClient, owner, library string, report recommendationReport, now time.Time) error {
	eligible := !report.Finished.IsZero() && report.Status != "running" && reportWithinRetention(report.Started, now)
	rows, err := c.RecommendationRecords(ctx)
	if err != nil {
		return err
	}
	prefix := provider.RecommendationSlug(owner, "reports", "")
	slug := prefix + fmt.Sprint(report.Started.UnixNano())
	var target provider.RecommendationRecord
	archived := false
	for _, r := range rows {
		if !strings.HasPrefix(r.Slug, prefix) {
			continue
		}
		if r.Slug == slug {
			target = r
		}
		var meta recommendationReportIndex
		if json.Unmarshal(r.SourceConfig[reportArchiveKey], &meta) != nil || meta.Started.IsZero() {
			continue
		}
		if !reportWithinRetention(meta.Started, now) {
			if err = c.DeleteRecommendationRecord(ctx, r.ID); err != nil {
				return err
			}
			continue
		}
		if r.Slug == slug {
			target = r
			archived = true
		}
	}
	if !eligible {
		return nil
	}
	if archived {
		return nil
	}
	if library == "" {
		for _, r := range rows {
			if r.Slug == provider.RecommendationSlug(owner, "state", "control") {
				library = r.LibraryID
				break
			}
		}
	}
	if library == "" {
		return fmt.Errorf("report archive library unavailable")
	}
	if target.ID == "" {
		target, err = c.CreateRecommendationRecord(ctx, slug, library, "Stash recommendation report "+report.Started.UTC().Format(time.RFC3339), true)
		if err != nil {
			return err
		}
	}
	target, tag, err := c.ReadRecommendationRecord(ctx, target.ID)
	if err != nil {
		return err
	}
	snapshot := emptyRecommendationState()
	snapshot.Report = report
	raw, err := encodeRecommendationState(snapshot)
	if err != nil {
		return err
	}
	if target.SourceConfig == nil {
		target.SourceConfig = map[string]json.RawMessage{}
	}
	target.SourceConfig["stash_recommendations"] = raw
	target.SourceConfig[reportArchiveKey], _ = json.Marshal(recommendationReportIndex{target.ID, report.Started, report.Finished, report.Week, report.Status, report.Preview})
	return c.UpdateRecommendationRecord(ctx, target.ID, tag, map[string]any{"source_config": target.SourceConfig})
}
func recommendationReportHistory(ctx context.Context, c *provider.SiloClient, owner string, now time.Time) ([]recommendationReportIndex, error) {
	rows, err := c.RecommendationRecords(ctx)
	if err != nil {
		return nil, err
	}
	out := []recommendationReportIndex{}
	prefix := provider.RecommendationSlug(owner, "reports", "")
	for _, r := range rows {
		if !strings.HasPrefix(r.Slug, prefix) {
			continue
		}
		var meta recommendationReportIndex
		if json.Unmarshal(r.SourceConfig[reportArchiveKey], &meta) == nil && reportWithinRetention(meta.Started, now) {
			meta.ID = r.ID
			out = append(out, meta)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}
func readRecommendationArchivedReport(ctx context.Context, c *provider.SiloClient, owner, id string, now time.Time) (recommendationReport, error) {
	r, _, err := c.ReadRecommendationRecord(ctx, id)
	if err != nil {
		return recommendationReport{}, err
	}
	if !strings.HasPrefix(r.Slug, provider.RecommendationSlug(owner, "reports", "")) {
		return recommendationReport{}, fmt.Errorf("report ownership mismatch")
	}
	var meta recommendationReportIndex
	if json.Unmarshal(r.SourceConfig[reportArchiveKey], &meta) != nil || !reportWithinRetention(meta.Started, now) {
		return recommendationReport{}, fmt.Errorf("report outside retention")
	}
	state, err := decodeRecommendationState(r.SourceConfig["stash_recommendations"])
	return state.Report, err
}

// Migration saves the currently retained final result without generating again.
func (s *recommendationServer) preserveCurrentReport(ctx context.Context, c *provider.SiloClient, owner string, state recommendationState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	return archiveRecommendationReport(ctx, c, owner, "", state.Report, time.Now())
}
