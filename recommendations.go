package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	rec "github.com/Net005/silo-plugin-metadata-stash/internal/recommendations"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

type recommendationConfig struct {
	Enabled    bool                       `json:"enabled"`
	Preview    bool                       `json:"preview_only"`
	Profile    string                     `json:"owner_profile_id"`
	APIKey     string                     `json:"openai_api_key"`
	Effort     string                     `json:"reasoning_effort"`
	MonthlyCap float64                    `json:"monthly_cap_usd"`
	Options    rec.Options                `json:"defaults"`
	Libraries  map[string]json.RawMessage `json:"libraries"`
	Day        int                        `json:"day_of_week"`
	At         string                     `json:"time_of_day"`
	Zone       string                     `json:"timezone"`
	Archive    bool                       `json:"use_archived_history"`
}

func defaultRecommendationConfig() recommendationConfig {
	return recommendationConfig{MonthlyCap: 2, Effort: "none", Options: rec.DefaultOptions(), Libraries: map[string]json.RawMessage{}, Day: 0, At: "03:30", Zone: "Europe/Amsterdam", Archive: true}
}
func parseRecommendationConfig(v map[string]any, old recommendationConfig) (recommendationConfig, error) {
	c := defaultRecommendationConfig()
	b, _ := json.Marshal(v)
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if raw := text(v["defaults_json"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &c.Options); err != nil {
			return c, fmt.Errorf("invalid recommendation defaults JSON: %w", err)
		}
	}
	if raw := text(v["libraries_json"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &c.Libraries); err != nil {
			return c, fmt.Errorf("invalid per-library JSON: %w", err)
		}
	}
	if c.APIKey == "" {
		c.APIKey = old.APIKey
	}

	if c.Effort != "none" && c.Effort != "low" {
		return c, fmt.Errorf("reasoning_effort must be none or low")
	}
	if c.MonthlyCap < 0 || c.MonthlyCap > 100 {
		return c, fmt.Errorf("monthly cap must be between 0 and 100 USD")
	}
	if c.Day < 0 || c.Day > 6 {
		return c, fmt.Errorf("invalid recommendation weekday")
	}
	if _, err := time.Parse("15:04", c.At); err != nil {
		return c, fmt.Errorf("time must use HH:MM")
	}
	if _, err := time.LoadLocation(c.Zone); err != nil {
		return c, fmt.Errorf("invalid recommendation timezone")
	}
	if c.Enabled && c.Profile == "" {
		return c, fmt.Errorf("recommendation owner profile ID is required")
	}
	c.Options = migrateRecommendationCount(c.Options)
	if err := c.Options.Validate(); err != nil {
		return c, err
	}
	for id := range c.Libraries {
		if _, err := c.libraryOptions(id); err != nil {
			return c, err
		}
	}
	return c, nil
}
func (c recommendationConfig) libraryOptions(id string) (rec.Options, error) {
	var o rec.Options
	raw, _ := json.Marshal(c.Options)
	_ = json.Unmarshal(raw, &o)
	if b := c.Libraries[id]; len(b) > 0 {
		if err := json.Unmarshal(b, &o); err != nil {
			return o, fmt.Errorf("library %s options: %w", id, err)
		}
	}
	o = migrateRecommendationCount(o).WithWatchlistPeriods()
	return o, o.Validate()
}

// Retire the previous shipped 250-item limit while preserving custom lower
// limits and an explicit opt-out for installations that intentionally want 250.
func migrateRecommendationCount(o rec.Options) rec.Options {
	if o.Count == 250 && !o.KeepLegacyCount {
		o.Count = 500
	}
	return o
}

func recommendationFingerprint(c recommendationConfig) string {
	raw, _ := json.Marshal(struct {
		Revision  string
		Profile   string
		Options   rec.Options
		Libraries map[string]json.RawMessage
		Archive   bool
	}{"watchlist-periods-500-compact-luna-batches-v3", c.Profile, c.Options, c.Libraries, c.Archive})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func recommendationCurrent(state recommendationState, c recommendationConfig, now time.Time) bool {
	return state.Report.Status == "complete" && state.Report.Fingerprint == state.LastFingerprint && state.LastWeek == recommendationPeriod(now, c) && state.LastFingerprint == recommendationFingerprint(c)
}

type recommendationReport struct {
	Fingerprint string `json:"configuration_fingerprint,omitempty"`

	Phase     string                     `json:"phase,omitempty"`
	Status    string                     `json:"status"`
	Started   time.Time                  `json:"started_at"`
	Finished  time.Time                  `json:"finished_at"`
	Week      string                     `json:"week"`
	Model     string                     `json:"model"`
	Preview   bool                       `json:"preview"`
	Libraries []rec.LibraryReport        `json:"libraries"`
	Warnings  []string                   `json:"warnings"`
	Archive   recommendationArchiveStats `json:"archive"`
	Usage     rec.Usage                  `json:"usage"`
	Totals    map[string]int             `json:"source_totals"`
	Unmatched int                        `json:"unmatched_scenes"`
}
type recommendationState struct {
	Version         int                                `json:"version"`
	Lease           string                             `json:"lease,omitempty"`
	LeaseUntil      time.Time                          `json:"lease_until,omitempty"`
	LastWeek        string                             `json:"last_published_week"`
	LastFingerprint string                             `json:"last_fingerprint"`
	Dirty           bool                               `json:"dirty"`
	Totals          map[string]int                     `json:"totals"`
	Previous        map[string]map[string][]string     `json:"previous"`
	Exposure        map[string]map[string]rec.Exposure `json:"exposure"`
	Dismissed       map[string]map[string]bool         `json:"dismissed"`
	Spend           map[string]float64                 `json:"spend_usd"`
	Report          recommendationReport               `json:"report"`
}

func emptyRecommendationState() recommendationState {
	return recommendationState{Version: 1, Totals: map[string]int{}, Previous: map[string]map[string][]string{}, Exposure: map[string]map[string]rec.Exposure{}, Dismissed: map[string]map[string]bool{}, Spend: map[string]float64{}}
}

type recommendationServer struct {
	pluginv1.UnimplementedHttpRoutesServer
	runtime  *runtimeServer
	mu       sync.Mutex
	running  bool
	pollOnce sync.Once
}

func (s *recommendationServer) configuration() (recommendationConfig, string, string, string, *stashClient, *artworkClient) {
	s.runtime.mu.RLock()
	defer s.runtime.mu.RUnlock()
	return s.runtime.recommendationConfig, s.runtime.siloBase, s.runtime.siloKey, s.runtime.stashPrefix, s.runtime.client, s.runtime.artwork
}
func (s *recommendationServer) state(ctx context.Context, c *provider.SiloClient, owner, library string, create bool) (recommendationState, provider.RecommendationRecord, string, error) {
	rows, err := c.RecommendationRecords(ctx)
	if err != nil {
		return recommendationState{}, provider.RecommendationRecord{}, "", err
	}
	slug := provider.RecommendationSlug(owner, "state", "control")
	var record provider.RecommendationRecord
	for _, r := range rows {
		if r.Slug == slug {
			if record.ID != "" {
				return recommendationState{}, record, "", fmt.Errorf("duplicate recommendation state records")
			}
			record = r
		}
	}
	if record.ID == "" {
		if !create {
			return emptyRecommendationState(), record, "", nil
		}
		record, err = c.CreateRecommendationRecord(ctx, slug, library, "Stash recommendation state", true)
		if err != nil {
			return recommendationState{}, record, "", err
		}
	}
	record, tag, err := c.ReadRecommendationRecord(ctx, record.ID)
	if err != nil {
		return recommendationState{}, record, "", err
	}
	state := emptyRecommendationState()
	if raw := record.SourceConfig["stash_recommendations"]; len(raw) > 0 {
		if state, err = decodeRecommendationState(raw); err != nil || state.Version != 1 {
			return state, record, tag, fmt.Errorf("recommendation state is invalid; refusing to reset budget/history")
		}
	}
	return state, record, tag, nil
}
func (s *recommendationServer) save(ctx context.Context, c *provider.SiloClient, r provider.RecommendationRecord, tag string, state recommendationState) (provider.RecommendationRecord, string, error) {
	if r.SourceConfig == nil {
		r.SourceConfig = map[string]json.RawMessage{}
	}
	b, err := encodeRecommendationState(state)
	if err != nil {
		return r, tag, err
	}
	r.SourceConfig["stash_recommendations"] = b
	if err = c.UpdateRecommendationRecord(ctx, r.ID, tag, map[string]any{"source_config": r.SourceConfig}); err != nil {
		return r, tag, err
	}
	return c.ReadRecommendationRecord(ctx, r.ID)
}

// Persist compressed reports so large multi-library plans fit Silo's 1 MiB body limit.
func encodeRecommendationState(state recommendationState) ([]byte, error) {
	// Shortlists are transient ranking inputs. Persist only verified AI priorities
	// for future reuse; retain every selected pick and its explanation in full.
	// Copy the slices so progress saves never alter the active ranking inputs.
	state.Report.Libraries = append([]rec.LibraryReport(nil), state.Report.Libraries...)
	for i := range state.Report.Libraries {
		lib := &state.Report.Libraries[i]
		lib.Collections = append([]rec.Collection(nil), lib.Collections...)
		for j := range lib.Collections {
			col := &lib.Collections[j]
			priorities := []rec.Pick{}
			for _, candidate := range col.Candidates {
				if candidate.LunaPriority != nil {
					priorities = append(priorities, rec.Pick{ID: candidate.ID, MediaID: candidate.MediaID, LunaPriority: candidate.LunaPriority})
				}
			}
			col.Candidates = priorities
		}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	encoding := "gzip-base64-v1"
	if len(raw) > 1000000 {
		raw, err = internRecommendationStrings(raw)
		if err != nil {
			return nil, err
		}
		encoding = "gzip-base64-v2"
	}
	var buf bytes.Buffer
	z, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err = z.Write(raw); err != nil {
		return nil, err
	}
	if err = z.Close(); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"encoding": encoding, "data": base64.StdEncoding.EncodeToString(buf.Bytes())})
}
func decodeRecommendationState(raw []byte) (recommendationState, error) {
	var envelope struct {
		Encoding string `json:"encoding"`
		Data     string `json:"data"`
	}
	state := emptyRecommendationState()
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return state, err
	}
	if envelope.Encoding != "" {
		if envelope.Encoding != "gzip-base64-v1" && envelope.Encoding != "gzip-base64-v2" {
			return state, fmt.Errorf("unknown state encoding")
		}
		b, err := base64.StdEncoding.DecodeString(envelope.Data)
		if err != nil {
			return state, err
		}
		z, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return state, err
		}
		defer z.Close()
		raw, err = io.ReadAll(io.LimitReader(z, 16<<20))
		if err != nil {
			return state, err
		}
	}
	if envelope.Encoding == "gzip-base64-v2" {
		var err error
		raw, err = expandRecommendationStrings(raw)
		if err != nil {
			return state, err
		}
	}
	err := json.Unmarshal(raw, &state)
	return state, err
}

func (s *recommendationServer) start(preview bool) bool {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return false
	}
	s.running = true
	s.mu.Unlock()
	go func() {
		defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		if err := s.run(ctx, preview); err != nil {
			s.runtime.task.log.Warn("Recommendation worker failed", "error", err)
		}
	}()
	return true
}
func recommendationPeriod(now time.Time, c recommendationConfig) string {
	loc, _ := time.LoadLocation(c.Zone)
	if loc == nil {
		return ""
	}
	now = now.In(loc)
	at, _ := time.Parse("15:04", c.At)
	delta := (int(now.Weekday()) - c.Day + 7) % 7
	day := now.AddDate(0, 0, -delta)
	anchor := time.Date(day.Year(), day.Month(), day.Day(), at.Hour(), at.Minute(), 0, 0, loc)
	if now.Before(anchor) {
		anchor = anchor.AddDate(0, 0, -7)
	}
	return anchor.Format("2006-01-02")
}
func recommendationDue(state recommendationState, cfg recommendationConfig, now time.Time) bool {
	period := recommendationPeriod(now, cfg)
	if period == "" || state.LeaseUntil.After(now) || recommendationCurrent(state, cfg, now) {
		return false
	}
	// Retry interrupted work once its lease expires. Do not repeatedly charge
	// for a failed or preview run with unchanged settings; manual retry stays available.
	return state.Report.Week != period || state.Report.Fingerprint != recommendationFingerprint(cfg) || state.Report.Status == "running"
}

func (s *recommendationServer) poll() {
	lastPrune := time.Time{}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		c, base, key, _, _, _ := s.configuration()
		if c.Enabled && base != "" && key != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			state, _, _, err := s.state(ctx, provider.NewSiloClient(base, key), c.Profile, "", false)
			cancel()
			if err == nil && recommendationDue(state, c, time.Now()) {
				s.start(c.Preview)
			} else if !c.Preview && time.Since(lastPrune) > time.Hour {
				s.mu.Lock()
				if !s.running {
					s.running = true
					go func() {
						defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
						defer cancel()
						if e := s.prune(ctx); e != nil {
							s.runtime.task.log.Warn("Recommendation prune failed", "error", e)
						}
					}()
				}
				s.mu.Unlock()
				lastPrune = time.Now()
			}
		}
		<-ticker.C
	}
}

const recommendationSceneFields = `id title code date created_at rating100 play_count o_counter last_played_at play_history o_history studio { id name } performers { id name favorite alias_list } tags { id name } groups { group { id name } } files { path duration }`

func recommendationScenes(ctx context.Context, c *stashClient) ([]rec.Scene, error) {
	rows := []rec.Scene{}
	seen := map[string]bool{}
	expected := -1
	for page := 1; page <= 1000; page++ {
		var d struct {
			Found struct {
				Count  int         `json:"count"`
				Scenes []rec.Scene `json:"scenes"`
			} `json:"findScenes"`
		}
		if err := c.graphql(ctx, `query($filter:FindFilterType){findScenes(filter:$filter){count scenes{`+recommendationSceneFields+`}}}`, map[string]any{"filter": map[string]any{"page": page, "per_page": 200, "sort": "id", "direction": "ASC"}}, &d); err != nil {
			return nil, err
		}
		if expected < 0 {
			expected = d.Found.Count
		}
		if expected != d.Found.Count {
			return nil, fmt.Errorf("Stash inventory changed during snapshot; retry next run")
		}
		for _, x := range d.Found.Scenes {
			if seen[x.ID] {
				return nil, fmt.Errorf("duplicate Stash pagination ID")
			}
			seen[x.ID] = true
			rows = append(rows, x)
		}
		if len(rows) == expected {
			return rows, nil
		}
		if len(d.Found.Scenes) == 0 || len(rows) > expected {
			return nil, fmt.Errorf("Stash snapshot pagination incomplete")
		}
	}
	return nil, fmt.Errorf("Stash snapshot page limit exceeded")
}
func localRecommendationID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return "local-" + hex.EncodeToString(sum[:14])
}
func matchRecommendationScenes(scenes []rec.Scene, catalog []provider.CatalogItem, library string) ([]rec.Scene, map[string]provider.CatalogItem, int) {
	items := map[string]provider.CatalogItem{}
	for _, x := range catalog {
		if x.Type == "movie" {
			items[x.ContentID] = x
		}
	}
	paths := map[string][]string{}
	for _, s := range scenes {
		for _, f := range s.Files {
			paths[f.Path] = append(paths[f.Path], s.ID)
		}
	}
	out := []rec.Scene{}
	unmatched := 0
	for _, scene := range scenes {
		ids := []string{}
		for _, f := range scene.Files {
			if len(paths[f.Path]) != 1 {
				continue
			}
			id := localRecommendationID(f.Path)
			if _, ok := items[id]; ok {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			unmatched++
			continue
		}
		scene.MediaID = ids[0]
		scene.LibraryID = library
		scene.SiloPlayed = items[scene.MediaID].UserState.Played
		out = append(out, scene)
	}
	return out, items, unmatched
}

func recommendationTotals(rows []rec.Scene) map[string]int {
	out := map[string]int{"scenes": len(rows)}
	for _, x := range rows {
		out["plays"] += x.Plays
		out["o"] += x.O
	}
	return out
}
func (s *recommendationServer) run(ctx context.Context, preview bool) (runErr error) {
	cfg, base, key, prefix, stash, art := s.configuration()
	if !cfg.Enabled {
		return fmt.Errorf("recommendations are disabled")
	}
	if stash == nil || !stash.configured() {
		return fmt.Errorf("Stash is not configured")
	}
	if base == "" || key == "" {
		return fmt.Errorf("Silo admin connection is required")
	}
	client := provider.NewSiloClient(base, key)
	libs, err := client.ListJAVBeaconMovieLibraries(ctx)
	if err != nil {
		return err
	}
	if len(libs) == 0 {
		return fmt.Errorf("no enabled Stash Metadata movie libraries")
	}
	sort.Slice(libs, func(i, j int) bool { return libs[i].ID < libs[j].ID })
	state, record, tag, err := s.state(ctx, client, cfg.Profile, libs[0].ID, true)
	if err != nil {
		return err
	}
	now := time.Now()
	loc, _ := time.LoadLocation(cfg.Zone)
	now = now.In(loc)
	if state.Lease != "" && state.LeaseUntil.After(now) {
		return fmt.Errorf("another recommendation worker holds the durable lease")
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return err
	}
	state.Lease = hex.EncodeToString(token)
	state.LeaseUntil = now.Add(50 * time.Minute)
	report := recommendationReport{Fingerprint: recommendationFingerprint(cfg), Phase: "Reading Stash history", Status: "running", Started: now, Week: recommendationPeriod(now, cfg), Model: "gpt-6-luna", Preview: preview || cfg.Preview, Libraries: []rec.LibraryReport{}}
	if err = archiveRecommendationReport(ctx, client, cfg.Profile, record.LibraryID, state.Report, now); err != nil {
		return fmt.Errorf("preserving previous recommendation report: %w", err)
	}
	previousReport := state.Report
	state.Report = report
	record, tag, err = s.save(ctx, client, record, tag, state)
	if err != nil {
		return err
	}
	defer func() {
		state.Lease = ""
		state.LeaseUntil = time.Time{}
		for i := range report.Libraries {
			for j := range report.Libraries[i].Collections {
				report.Libraries[i].Collections[j].Candidates = nil
			}
		}
		report.Finished = time.Now()
		if runErr != nil {
			report.Status = "failed"
			report.Warnings = append(report.Warnings, runErr.Error())
		}
		state.Report = report
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, _, e := s.save(cleanup, client, record, tag, state); e != nil {
			s.runtime.task.log.Error("Recommendation final report could not be saved", "error", e)
		}
		if e := archiveRecommendationReport(cleanup, client, cfg.Profile, record.LibraryID, report, time.Now()); e != nil {
			s.runtime.task.log.Error("Recommendation report archive could not be saved", "error", e)
		}
	}()
	progress := func(phase string) error {
		report.Phase = phase
		state.Report = report
		var e error
		record, tag, e = s.save(ctx, client, record, tag, state)
		return e
	}
	scenes, err := recommendationScenes(ctx, stash)
	if err != nil {
		return err
	}
	report.Totals = recommendationTotals(scenes)
	if rec.HistoryDrop(state.Totals, report.Totals) {
		return fmt.Errorf("Stash scene/activity totals dropped by more than 20%%; previous collections preserved")
	}
	if err = progress("Reading archived history and Stash Watchlist"); err != nil {
		return err
	}
	historyOnly := []rec.Scene{}
	if cfg.Archive {
		var stats recommendationArchiveStats
		var e error
		scenes, historyOnly, stats, e = mergeRecommendationArchive(ctx, art, scenes, libs)
		report.Archive = stats
		if e != nil {
			return fmt.Errorf("JAVBeacon archive required but unavailable: %w", e)
		}
	}
	watch := map[string]bool{}
	if s.runtime.legacy != nil {
		filters, e := s.runtime.legacy.Provider().StashSavedFilters(ctx, "Watchlist")
		if e != nil {
			return fmt.Errorf("Stash Watchlist saved filter unavailable; previous recommendations preserved")
		} else {
			for _, f := range filters {
				for _, x := range f.Items {
					watch[x.SceneID] = true
				}
			}
		}
	}
	matchedScenes := map[string]bool{}
	artifacts := map[string]map[string]provider.CatalogItem{}
	options := map[string]rec.Options{}
	for _, lib := range libs {
		o, e := cfg.libraryOptions(lib.ID)
		if e != nil {
			return e
		}
		if !o.Enabled {
			continue
		}
		if err = progress("Matching library " + lib.ID); err != nil {
			return err
		}
		catalog, e := client.ListRecommendationCatalog(ctx, lib.ID, cfg.Profile)
		if e != nil {
			return e
		}
		local, items, unmatched := matchRecommendationScenes(scenes, catalog, lib.ID)
		for _, x := range local {
			matchedScenes[x.ID] = true
		}
		if len(local) == 0 && len(state.Previous[lib.ID]) > 0 {
			return fmt.Errorf("library %s lost every verified match; previous collections preserved", lib.ID)
		}
		_ = unmatched
		artifacts[lib.ID] = items
		options[lib.ID] = o
		learningLocal := append([]rec.Scene(nil), local...)
		for _, old := range historyOnly {
			if old.LibraryID == lib.ID {
				learningLocal = append(learningLocal, old)
			}
		}
		learningGlobal := append(append([]rec.Scene(nil), scenes...), historyOnly...)
		r := rec.Build(lib.ID, learningLocal, learningGlobal, o, watch, state.Previous[lib.ID], state.Exposure[lib.ID], state.Dismissed[lib.ID], now)
		r.Target = o.Count
		r.Matched = len(local)
		r.Evaluation = rec.Evaluate(local, o, now)
		report.Libraries = append(report.Libraries, r)
	}
	report.Unmatched = len(scenes) - len(matchedScenes)
	// Reserve the worst-case request cost durably before calling OpenAI. Unknown
	// outcomes retain the reservation, preventing retries from escaping the cap.
	month := now.Format("2006-01")
	for i := range report.Libraries {
		r := &report.Libraries[i]
		if len(r.Collections) == 0 {
			continue
		}
		if cfg.APIKey == "" {
			r.Warnings = append(r.Warnings, "OpenAI key missing; local ranking used where saved priorities are unavailable")
		}
		for j := range r.Collections {
			if cfg.APIKey == "" || len(r.Collections[j].Candidates) == 0 || rec.LocalOnlyKind(r.Collections[j].Kind) {
				continue
			}
			ranking := *r
			ranking.Collections = []rec.Collection{r.Collections[j]}
			batches := rec.LunaBatches(ranking)
			failed := false
			for bi := range batches {
				batch := batches[bi]
				report.Phase = fmt.Sprintf("Luna ranking library %s: %s (%d/%d)", r.LibraryID, r.Collections[j].Kind, bi+1, len(batches))
				maxOut := 8000
				if cfg.Effort == "low" {
					maxOut = 16000
				}
				input := rec.LunaRequest(batch, cfg.Effort, maxOut)
				reserve := rec.ReserveCost(input, maxOut)
				if len(input) > 240000 {
					r.Warnings = append(r.Warnings, fmt.Sprintf("%s: Luna request exceeds 240 KB (%d candidates, %d bytes); local ranking used", r.Collections[j].Kind, len(r.Collections[j].Candidates), len(input)))
					failed = true
					break
				}
				if state.Spend[month]+reserve > cfg.MonthlyCap {
					r.Warnings = append(r.Warnings, "Monthly spending cap reached; local ranking used")
					failed = true
					break
				}
				state.Spend[month] += reserve
				report.Usage.Reserved += reserve
				state.Report = report
				record, tag, err = s.save(ctx, client, record, tag, state)
				if err != nil {
					state.Spend[month] -= reserve
					report.Usage.Reserved -= reserve
					return err
				}
				usage, e := (rec.Luna{Key: cfg.APIKey, Effort: cfg.Effort}).Organize(ctx, &batch, maxOut)
				if usage.Input > 0 || usage.Output > 0 {
					state.Spend[month] += usage.Cost - reserve
					report.Usage.Reserved -= reserve
					report.Usage.Input += usage.Input
					report.Usage.Output += usage.Output
					report.Usage.Cached += usage.Cached
					report.Usage.CacheWrites += usage.CacheWrites
					report.Usage.Requests += usage.Requests
					report.Usage.RequestIDs = append(report.Usage.RequestIDs, usage.RequestIDs...)
					report.Usage.CostBasis = "estimated_standard_token_rates"
					report.Usage.Cost += usage.Cost
				}
				if e != nil {
					r.Warnings = append(r.Warnings, r.Collections[j].Kind+": "+e.Error())
				}

				state.Report = report
				record, tag, err = s.save(ctx, client, record, tag, state)
				if err != nil {
					return err
				}
				if e != nil {
					failed = true
					break
				}
				batches[bi] = batch
			}
			if !failed {
				if e := rec.MergeLunaBatches(&ranking, batches); e != nil {
					r.Warnings = append(r.Warnings, r.Collections[j].Kind+": "+e.Error())
				} else {
					r.Collections[j].Candidates = ranking.Collections[0].Candidates
				}
			}
		}
		var saved *rec.LibraryReport
		if !previousReport.Started.IsZero() && !previousReport.Started.After(now) && now.Sub(previousReport.Started) <= 42*24*time.Hour {
			for k := range previousReport.Libraries {
				if previousReport.Libraries[k].LibraryID == r.LibraryID {
					saved = &previousReport.Libraries[k]
				}
			}
		}
		rec.ReuseLunaRankings(r, saved)
		rec.Finalize(r, options[r.LibraryID], state.Previous[r.LibraryID])
		state.Report = report
		record, tag, err = s.save(ctx, client, record, tag, state)
		if err != nil {
			return err
		}
	}
	// All source reads and rankings complete before any visible collection write.
	if err = progress("Updating collection membership and artwork"); err != nil {
		return err
	}
	if report.Preview {
		report.Status = "preview_complete"
		return nil
	}
	existing, err := client.RecommendationRecords(ctx)
	if err != nil {
		return err
	}
	bySlug := map[string]provider.RecommendationRecord{}
	for _, r := range existing {
		if _, ok := bySlug[r.Slug]; ok {
			return fmt.Errorf("duplicate owned collection slug")
		}
		bySlug[r.Slug] = r
	}
	for _, lib := range libs {
		o, e := cfg.libraryOptions(lib.ID)
		if e != nil {
			return e
		}
		if o.Enabled {
			continue
		}
		for _, kind := range rec.Kinds {
			if old := bySlug[provider.RecommendationSlug(cfg.Profile, lib.ID, kind)]; old.ID != "" {
				_, t, e := client.ReadRecommendationRecord(ctx, old.ID)
				if e != nil {
					return e
				}
				if e = client.UpdateRecommendationRecord(ctx, old.ID, t, map[string]any{"visibility": "hidden"}); e != nil {
					return e
				}
			}
		}
	}
	for _, r := range report.Libraries {
		if state.Previous[r.LibraryID] == nil {
			state.Previous[r.LibraryID] = map[string][]string{}
		}
		if state.Exposure[r.LibraryID] == nil {
			state.Exposure[r.LibraryID] = map[string]rec.Exposure{}
		}
		selectedKinds := map[string]bool{}
		for _, col := range r.Collections {
			selectedKinds[col.Kind] = true
			slug := provider.RecommendationSlug(cfg.Profile, r.LibraryID, col.Kind)
			current := bySlug[slug]
			if len(col.Picks) == 0 {
				if current.ID != "" {
					_, t, e := client.ReadRecommendationRecord(ctx, current.ID)
					if e != nil {
						return e
					}
					if e = client.UpdateRecommendationRecord(ctx, current.ID, t, map[string]any{"visibility": "hidden"}); e != nil {
						return e
					}
				}
				continue
			}
			if current.ID == "" {
				current, err = client.CreateRecommendationRecord(ctx, slug, r.LibraryID, prefix+col.Title, false)
				if err != nil {
					return err
				}
			}
			ids := []string{}
			sceneIDs := []string{}
			for _, p := range col.Picks {
				ids = append(ids, p.MediaID)
				sceneIDs = append(sceneIDs, p.ID)
			}
			if err = client.ReconcileRecommendation(ctx, current, ids); err != nil {
				return err
			}
			current, t, e := client.ReadRecommendationRecord(ctx, current.ID)
			if e != nil {
				return e
			}
			if current.SourceConfig == nil {
				current.SourceConfig = map[string]json.RawMessage{}
			}
			summary := col
			summary.Candidates = nil
			current.SourceConfig["stash_recommendations"], _ = json.Marshal(map[string]any{"week": report.Week, "owner": cfg.Profile, "selection": summary})
			if err = client.UpdateRecommendationRecord(ctx, current.ID, t, map[string]any{"title": prefix + col.Title, "description": provider.RecommendationOwner + " " + col.Description, "visibility": "visible", "featured": true, "source_config": current.SourceConfig}); err != nil {
				return err
			}
			artChoices := []provider.CollectionArtwork{}
			for _, id := range ids {
				if a, ok := artifacts[r.LibraryID][id]; ok {
					artChoices = append(artChoices, provider.CollectionArtwork{MediaID: a.ContentID, PosterURL: a.PosterURL, BackdropURL: a.BackdropURL})
				}
			}
			if item, ok := artifacts[r.LibraryID][ids[0]]; ok {
				if e = client.SetRecommendationArtwork(ctx, current, provider.CollectionArtwork{MediaID: item.ContentID, PosterURL: item.PosterURL, BackdropURL: item.BackdropURL}, artChoices...); e != nil {
					report.Warnings = append(report.Warnings, "Collection artwork caching failed for "+col.Kind+" in library "+r.LibraryID)
				}
			}
			state.Previous[r.LibraryID][col.Kind] = sceneIDs
			for _, id := range sceneIDs {
				x := state.Exposure[r.LibraryID][id]
				if x.LastWeek != report.Week {
					x.Count++
					x.LastWeek = report.Week
					state.Exposure[r.LibraryID][id] = x
				}
			}
		}
		for _, kind := range rec.Kinds {
			if selectedKinds[kind] {
				continue
			}
			slug := provider.RecommendationSlug(cfg.Profile, r.LibraryID, kind)
			if old := bySlug[slug]; old.ID != "" {
				_, t, e := client.ReadRecommendationRecord(ctx, old.ID)
				if e != nil {
					return e
				}
				if e = client.UpdateRecommendationRecord(ctx, old.ID, t, map[string]any{"visibility": "hidden"}); e != nil {
					return e
				}
			}
		}
	}
	state.Totals = report.Totals
	state.LastWeek = report.Week
	state.LastFingerprint = report.Fingerprint
	state.Dirty = false
	report.Phase = "Finished"
	report.Status = "complete"
	return nil
}

func (s *recommendationServer) Handle(ctx context.Context, req *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	cfg, base, key, _, _, _ := s.configuration()
	client := provider.NewSiloClient(base, key)
	respond := func(status int, v any) (*pluginv1.HandleHTTPResponse, error) {
		b, _ := json.Marshal(v)
		return &pluginv1.HandleHTTPResponse{StatusCode: int32(status), Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"}, Body: b}, nil
	}
	path := strings.TrimSuffix(req.Path, "/")
	if req.Method == "GET" && (path == "/recommendations" || path == "/recommendations/admin") {
		return recommendationPage(ctx)
	}
	switch {
	case req.Method == "POST" && path == "/recommendations/posters/repair":
		var input struct {
			Library string   `json:"library_id"`
			IDs     []string `json:"item_ids"`
		}
		if json.Unmarshal(req.Body, &input) != nil || input.Library == "" || len(input.IDs) == 0 || len(input.IDs) > 20 {
			return respond(400, map[string]any{"error": "Provide a Stash Metadata library and 1–20 item IDs"})
		}
		result, e := s.runtime.task.repairSelectedPosters(ctx, input.Library, input.IDs)
		if e != nil {
			return respond(503, map[string]any{"error": e.Error()})
		}
		return respond(200, result)
	case req.Method == "POST" && path == "/recommendations/watchlist/reconcile":
		if s.runtime.legacy == nil {
			return respond(503, map[string]any{"error": "Watchlist reconciler unavailable"})
		}
		go func() {
			work, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			s.runtime.legacy.Provider().InvalidateStashSavedFilters()
			if e := s.runtime.legacy.ReconcileWatchlist(work); e != nil {
				s.runtime.task.log.Warn("Realtime Watchlist reconciliation failed", "error", e)
			}
		}()
		return respond(202, map[string]any{"status": "queued"})
	case req.Method == "POST" && (path == "/recommendations/run" || path == "/recommendations/preview"):
		if !cfg.Enabled {
			return respond(400, map[string]any{"error": "enable recommendations first"})
		}
		state, _, _, err := s.state(ctx, client, cfg.Profile, "", false)
		if err != nil {
			return respond(503, map[string]any{"error": "recommendation state unavailable"})
		}
		if state.LeaseUntil.After(time.Now()) {
			return respond(409, map[string]any{"status": "worker_reservation_active", "error": "An earlier build still holds its reservation. If interrupted by a restart, it can resume after the reservation expires.", "lease_until": state.LeaseUntil})
		}
		if !s.start(path == "/recommendations/preview") {
			return respond(409, map[string]any{"status": "already_running"})
		}
		return respond(202, map[string]any{"status": "started", "report": "./report"})
	case req.Method == "GET" && path == "/recommendations/report":
		state, _, _, err := s.state(ctx, client, cfg.Profile, "", false)
		if err != nil {
			return respond(503, map[string]any{"error": "recommendation report unavailable"})
		}
		if e := s.preserveCurrentReport(ctx, client, cfg.Profile, state); e != nil {
			return respond(503, map[string]any{"error": "Could not preserve recommendation report"})
		}
		s.mu.Lock()
		active := s.running
		s.mu.Unlock()
		return respond(200, map[string]any{"worker_active_on_this_process": active, "lease_until": state.LeaseUntil, "report": state.Report, "monthly_spend_usd": state.Spend, "last_published_week": state.LastWeek, "dirty": state.Dirty})
	case path == "/recommendations/history" && req.Method == "GET":
		rows, e := recommendationReportHistory(ctx, client, cfg.Profile, time.Now())
		if e != nil {
			return respond(503, map[string]any{"error": "Report history unavailable"})
		}
		return respond(200, map[string]any{"reports": rows, "retention_months": 6})
	case path == "/recommendations/history" && req.Method == "POST":
		var input struct {
			ID string `json:"report_id"`
		}
		if json.Unmarshal(req.Body, &input) != nil || input.ID == "" {
			return respond(400, map[string]any{"error": "Choose a report"})
		}
		report, e := readRecommendationArchivedReport(ctx, client, cfg.Profile, input.ID, time.Now())
		if e != nil {
			return respond(404, map[string]any{"error": "Report not found"})
		}
		return respond(200, map[string]any{"report": report, "historical": true})
	case req.Method == "POST" && (path == "/recommendations/dirty" || path == "/recommendations/dismiss"):
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.running {
			return respond(409, map[string]any{"status": "worker_running"})
		}
		state, r, tag, err := s.state(ctx, client, cfg.Profile, "", false)
		if err != nil || r.ID == "" {
			return respond(503, map[string]any{"error": "recommendation state not initialised"})
		}
		if state.LeaseUntil.After(time.Now()) {
			return respond(409, map[string]any{"status": "worker_running"})
		}
		state.Dirty = true
		if path == "/recommendations/dismiss" {
			var d struct {
				Library string `json:"library_id"`
				Scene   string `json:"scene_id"`
				Undo    bool   `json:"undo"`
			}
			if len(req.Body) > 4096 || json.Unmarshal(req.Body, &d) != nil || d.Library == "" || d.Scene == "" {
				return respond(400, map[string]any{"error": "library_id and scene_id are required"})
			}
			validLibrary := false
			for _, lib := range state.Report.Libraries {
				if lib.LibraryID == d.Library {
					validLibrary = true
				}
			}
			if !validLibrary {
				return respond(400, map[string]any{"error": "unknown recommendation library"})
			}
			if state.Dismissed[d.Library] == nil {
				state.Dismissed[d.Library] = map[string]bool{}
			}
			if d.Undo {
				delete(state.Dismissed[d.Library], d.Scene)
			} else {
				state.Dismissed[d.Library][d.Scene] = true
			}
		}
		if _, _, err = s.save(ctx, client, r, tag, state); err != nil {
			return respond(409, map[string]any{"error": "state changed; retry"})
		}
		return respond(200, map[string]any{"status": "updated"})
	}
	return respond(404, map[string]any{"error": "unknown recommendation route"})
}
