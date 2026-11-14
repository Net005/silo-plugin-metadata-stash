package main

import (
	"context"
	"regexp"
	"sort"
	"sync"
	"time"
)

// A failing export is retried every five seconds. Twelve consecutive failures
// (about a minute) is long enough to call the export stuck.
const (
	watchlistStuckAfter   = 12
	watchlistRepeatEvery  = 120
	watchlistErrorMaxSize = 200
)

var urlInError = regexp.MustCompile(`https?://[^\s"']+`)

// watchlistHealth records the outcome of the background Watchlist export so a
// persistent failure is visible without reading raw journals or server logs.
type watchlistHealth struct {
	mu          sync.Mutex
	lastOK      time.Time
	lastFailure time.Time
	firstFail   time.Time
	failures    int
	lastError   string
}

// record stores one cycle's outcome and reports whether the failure should be
// escalated to an error log (first time stuck, then occasionally).
func (h *watchlistHealth) record(err error, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.lastOK, h.failures, h.lastError, h.firstFail = now, 0, "", time.Time{}
		return false
	}
	if h.failures == 0 {
		h.firstFail = now
	}
	h.failures++
	h.lastFailure = now
	msg := urlInError.ReplaceAllString(err.Error(), "<url>")
	if len(msg) > watchlistErrorMaxSize {
		msg = msg[:watchlistErrorMaxSize]
	}
	h.lastError = msg
	return h.failures == watchlistStuckAfter || (h.failures > watchlistStuckAfter && (h.failures-watchlistStuckAfter)%watchlistRepeatEvery == 0)
}

func (h *watchlistHealth) snapshot(now time.Time) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]any{"consecutive_failures": h.failures, "stuck": h.failures >= watchlistStuckAfter}
	if !h.lastOK.IsZero() {
		out["last_success_at"] = h.lastOK.UTC().Format(time.RFC3339)
	}
	if h.failures > 0 {
		out["last_error"] = h.lastError
		out["failing_since"] = h.firstFail.UTC().Format(time.RFC3339)
		out["failing_for_seconds"] = int(now.Sub(h.firstFail).Seconds())
	}
	return out
}

// watchlistStatus is read-only: it reads the journals and never writes to
// Silo, Stash or JAVBeacon.
func (s *runtimeServer) watchlistStatus(ctx context.Context) map[string]any {
	now := time.Now()
	out := map[string]any{"read_only": true, "export": s.wlHealth.snapshot(now)}
	rows, err := s.selectedWatchlistCollections(ctx)
	if err != nil {
		out["error"] = "unable to read the selected Watchlist collections"
		return out
	}
	if len(rows) == 0 {
		out["status"] = "not_configured"
		out["detail"] = "Select the Stash Watchlist saved filter first"
		return out
	}
	libraries := []any{}
	problems := 0
	for _, row := range rows {
		_, j, _, err := s.watchlistState(ctx, row.ID)
		entry := map[string]any{"library_id": row.LibraryID}
		if err != nil {
			entry["error"] = "journal unreadable"
			problems++
			libraries = append(libraries, entry)
			continue
		}
		entry["members_in_baseline"] = len(j.Baseline)
		entry["pending"] = len(j.Pending)
		entry["incoming_from_stash"] = len(j.Incoming)
		entry["parked_unavailable"] = len(j.Inactive)
		oldest := time.Time{}
		for _, m := range []map[string]watchlistIntent{j.Pending, j.Incoming} {
			for _, intent := range m {
				if oldest.IsZero() || intent.Changed.Before(oldest) {
					oldest = intent.Changed
				}
			}
		}
		if !oldest.IsZero() {
			age := int(now.Sub(oldest).Seconds())
			entry["oldest_queued_age_seconds"] = age
			if age > 600 {
				problems++
			}
		}
		libraries = append(libraries, entry)
	}
	sort.Slice(libraries, func(a, b int) bool {
		return libraries[a].(map[string]any)["library_id"].(string) < libraries[b].(map[string]any)["library_id"].(string)
	})
	out["libraries"] = libraries
	status := "ok"
	if problems > 0 || out["export"].(map[string]any)["stuck"] == true {
		status = "attention"
	}
	out["status"] = status
	return out
}
