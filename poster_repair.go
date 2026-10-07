package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	"github.com/Net005/silo-plugin-metadata-stash/internal/poster"
)

var javPosterCode = regexp.MustCompile(`(?i)^[a-z]{2,12}[-_][0-9]{2,7}$`)

type posterMarker struct {
	Source  string    `json:"source"`
	Hash    string    `json:"hash"`
	Stored  string    `json:"stored"`
	Checked time.Time `json:"checked"`
}
type posterRepairState struct {
	Cursor    string                  `json:"cursor"`
	Items     map[string]posterMarker `json:"items"`
	Revision  string                  `json:"revision"`
	Offset    int                     `json:"offset"`
	Scanned   int                     `json:"scanned"`
	Applied   int                     `json:"applied"`
	Skipped   int                     `json:"skipped"`
	Errors    map[string]string       `json:"errors"`
	Completed time.Time               `json:"completed"`
	Updated   time.Time               `json:"updated"`
}

// The server receives one unpredictable localhost URL only while applying an
// image. The supported acting-admin image API permits private network access.
// No public route, login token or persistent image-serving daemon is needed.
func applyRenderedPoster(ctx context.Context, base, key, id string, data []byte) (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		listener.Close()
		return "", err
	}
	path := "/" + hex.EncodeToString(token)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(data))
		w.Header().Set("Cache-Control", "no-store")
		w.Write(data)
	})}
	go server.Serve(listener)
	defer server.Close()
	var result struct {
		ContentID  string `json:"content_id"`
		StoredPath string `json:"stored_path"`
	}
	err = siloRequest(ctx, base, key, http.MethodPost, "/api/v2/admin/items/"+url.PathEscape(id)+"/images/apply", map[string]string{"original_url": "http://" + listener.Addr().String() + path, "type": "poster", "provider_id": "stash"}, &result)
	if err != nil {
		return "", err
	}
	if result.ContentID != id || result.StoredPath == "" {
		return "", fmt.Errorf("poster apply returned no stored image")
	}
	return result.StoredPath, nil
}

func (s *scheduledTaskServer) repairPosters(ctx context.Context) (map[string]any, error) {
	return s.repairSelectedPosters(ctx, "", nil)
}

func (s *scheduledTaskServer) repairSelectedPosters(ctx context.Context, library string, ids []string) (map[string]any, error) {
	if !s.posterMu.TryLock() {
		return map[string]any{"status": "already_running"}, nil
	}
	defer s.posterMu.Unlock()
	s.runtime.mu.RLock()
	base, key, mode, enabled := s.runtime.siloBase, s.runtime.siloKey, s.runtime.posterLayout, s.runtime.posterRepair
	artwork := s.runtime.artwork
	profile := s.runtime.recommendationConfig.Profile
	stash := s.runtime.client
	s.runtime.mu.RUnlock()
	if !enabled && !stash.configured() {
		return map[string]any{"status": "disabled"}, nil
	}
	if base == "" || key == "" || profile == "" || !stash.configured() {
		return nil, fmt.Errorf("poster repair requires Silo/Stash connections and owner profile")
	}
	client := provider.NewSiloClient(base, key)
	libs, err := client.ListJAVBeaconMovieLibraries(ctx)
	if err != nil {
		return nil, err
	}
	records, err := client.RecommendationRecords(ctx)
	if err != nil {
		return nil, err
	}
	applied, skipped := 0, 0
	more := false
	for _, lib := range libs {
		if library != "" && lib.ID != library {
			continue
		}
		slug := "stash-recommendations-poster-layout-" + lib.ID
		var record provider.RecommendationRecord
		for _, r := range records {
			if r.Slug == slug {
				if record.ID != "" {
					return nil, fmt.Errorf("duplicate poster repair state")
				}
				record = r
			}
		}
		if record.ID == "" {
			record, err = client.CreateRecommendationRecord(ctx, slug, lib.ID, "Stash poster layout state", true)
			if err != nil {
				return nil, err
			}
		}
		record, tag, err := client.ReadRecommendationRecord(ctx, record.ID)
		if err != nil {
			return nil, err
		}
		if record.SourceConfig == nil {
			record.SourceConfig = map[string]json.RawMessage{}
		}
		state := posterRepairState{Items: map[string]posterMarker{}}
		if b := record.SourceConfig["poster_layout"]; len(b) > 0 {
			if err = json.Unmarshal(b, &state); err != nil {
				return nil, err
			}
		}
		if state.Items == nil {
			state.Items = map[string]posterMarker{}
		}
		if library == "" {
			revision := fmt.Sprintf("renewal-v5-context:%s:%t", mode, enabled)
			if state.Revision != revision {
				state.Cursor, state.Offset, state.Scanned, state.Applied, state.Skipped = "", 0, 0, 0, 0
				state.Completed = time.Time{}
				state.Revision = revision
			}
			if !state.Completed.IsZero() && time.Since(state.Completed) < 7*24*time.Hour {
				continue
			}
			if !state.Completed.IsZero() {
				state.Completed = time.Time{}
				state.Cursor, state.Offset, state.Scanned, state.Applied, state.Skipped = "", 0, 0, 0, 0
			}
		}
		if state.Errors == nil {
			state.Errors = map[string]string{}
		}
		var items []provider.CatalogItem
		var next string
		if library != "" {
			for _, id := range ids {
				items = append(items, provider.CatalogItem{ContentID: id})
			}
		} else {
			items, next, err = client.ListMatchedCatalogPage(ctx, lib.ID, state.Cursor)
		}
		if err != nil {
			return nil, err
		}
		// One bounded page per library. Commit the cursor only after the full page
		// succeeds; markers make retries safe after partial publication.
		for index, item := range items {
			if library == "" && index < state.Offset {
				continue
			}
			before := applied
			itemErr := func() error {
				var detail cacheArtworkItem
				if err = siloProfileRequest(ctx, base, key, profile, http.MethodGet, "/api/v2/catalog/items/"+url.PathEscape(item.ContentID), nil, &detail); err != nil {
					return err
				}
				if detail.Type != "movie" || slices.Contains(detail.LockedFields, artworkLockField) {
					skipped++
					return nil
				}
				paths, e := client.ItemFilePathsForLibrary(ctx, item.ContentID, lib.ID)
				if e != nil {
					return e
				}
				sceneID, e := stash.sceneIDForExactPaths(ctx, paths)
				var row *scene
				var sharedArtwork []byte
				if e != nil && library != "" && len(ids) > 0 && e.Error() == "multiple Stash scenes match playback files" {
					row, sharedArtwork, e = stash.sharedJacketForParts(ctx, paths, detail.PosterURL)
					if row != nil {
						sceneID = row.ID
					}
				}
				if e != nil {
					if library == "" && e.Error() == "multiple Stash scenes match playback files" {
						// Ambiguous files are never repaired automatically; keep the scan moving.
						skipped++
						return nil
					}
					return fmt.Errorf("poster item %s: %w", item.ContentID, e)
				}
				if sceneID == "" {
					skipped++
					return nil
				}
				if row == nil {
					row, e = stash.findScene(ctx, sceneID)
				}
				if e != nil {
					return e
				}
				if row == nil || row.Paths.Screenshot == "" {
					skipped++
					return nil
				}
				studioName := ""
				if row.Studio != nil {
					studioName = row.Studio.Name
				}
				isJAV := javPosterCode.MatchString(strings.TrimSpace(row.Code))
				// Automatic JAV repair remains paused pending review of the bulk previews.
				// Only an explicitly scoped repair can change a JAV poster.
				if (isJAV && len(ids) == 0) || (!isJAV && !enabled) {
					skipped++
					return nil
				}
				if art := s.runtime.sceneArtwork(ctx, sceneID); !isJAV && art != nil && art.ReleaseID > 0 {
					skipped++
					return nil
				}
				fingerprint := fmt.Sprintf("layout-v6-yunet-context|%t|%s|%s|%s|%s|%s|%s", isJAV, mode, row.ID, row.Title, row.Date, row.Paths.Screenshot, studioName)
				fp := sha256.Sum256([]byte(fingerprint))
				source := hex.EncodeToString(fp[:])
				old := state.Items[item.ContentID]
				if library == "" && old.Source == source && time.Since(old.Checked) < 7*24*time.Hour {
					skipped++
					return nil
				}
				raw := sharedArtwork
				if len(raw) == 0 {
					imageURL := stash.imageURL(row.Paths.Screenshot)
					req, e := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
					if e != nil {
						return e
					}
					resp, e := (&http.Client{Timeout: 30 * time.Second}).Do(req)
					if e != nil {
						return e
					}
					raw, e = io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
					resp.Body.Close()
					if e != nil || resp.StatusCode != 200 || len(raw) > 16<<20 {
						return fmt.Errorf("poster source unavailable or oversized")
					}
				}
				hash := sha256.Sum256(raw)
				digest := hex.EncodeToString(hash[:])
				stored := old.Stored
				if library != "" || old.Source != source || old.Hash != digest {
					// Recheck locks immediately before publication; never replace a manual
					// image selected while this worker downloaded the source.
					if e = siloProfileRequest(ctx, base, key, profile, http.MethodGet, "/api/v2/catalog/items/"+url.PathEscape(item.ContentID), nil, &detail); e != nil {
						return e
					}
					if slices.Contains(detail.LockedFields, artworkLockField) {
						skipped++
						return nil
					}
					footer := row.Date
					if row.Studio != nil {
						footer = row.Studio.Name + " / " + footer
					}
					var rendered []byte
					if isJAV {
						var changed bool
						rendered, changed, e = poster.RenderJAVPoster(raw)
						if e == nil && !changed {
							state.Items[item.ContentID] = posterMarker{Source: source, Hash: digest, Checked: time.Now().UTC()}
							skipped++
							return nil
						}
					} else if mode == "smart" {
						rendered, e = artwork.renderScenePoster(ctx, raw)
						if errors.Is(e, errInsufficientPosterContext) {
							state.Items[item.ContentID] = posterMarker{Source: source, Hash: digest, Checked: time.Now().UTC()}
							skipped++
							return nil
						}
					} else {
						rendered, e = poster.RenderScenePoster(raw, row.Title, footer, mode)
					}
					if e != nil {
						return e
					}
					stored, e = applyRenderedPoster(ctx, base, key, item.ContentID, rendered)
					// Restore the artwork lock only when the API actually returns the
					// complete lock list. An omitted field is not an empty list; never
					// clear unknown existing metadata locks.
					cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
					var current cacheArtworkItem
					lockErr := siloProfileRequest(cleanup, base, key, profile, http.MethodGet, "/api/v2/catalog/items/"+url.PathEscape(item.ContentID), nil, &current)
					if lockErr == nil && current.LockedFields != nil {
						locks := []int{}
						for _, n := range current.LockedFields {
							if n != artworkLockField {
								locks = append(locks, n)
							}
						}
						lockErr = siloRequest(cleanup, base, key, http.MethodPatch, "/api/v2/admin/items/"+url.PathEscape(item.ContentID)+"/metadata", map[string]any{"locked_fields": locks}, nil)
					}
					cancel()
					if e != nil {
						return e
					}
					if lockErr != nil {
						return lockErr
					}
					applied++
				} else {
					skipped++
				}
				state.Items[item.ContentID] = posterMarker{source, digest, stored, time.Now().UTC()}
				return nil
			}()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if itemErr != nil {
				state.Errors[item.ContentID] = regexp.MustCompile(`(?i)(apikey|api_key|token|access_token)=([^&\s"<>]+)`).ReplaceAllString(itemErr.Error(), "$1=[redacted]")
			} else {
				delete(state.Errors, item.ContentID)
			}
			if library == "" {
				state.Offset = index + 1
				state.Scanned++
				state.Applied += applied - before
				if itemErr == nil && applied == before {
					state.Skipped++
				}
			}
			state.Updated = time.Now().UTC()
			// A skipped item has no publication to recover. Checkpoint skips in
			// groups, but persist every changed cover and failure immediately.
			if itemErr != nil || applied > before || (index+1)%20 == 0 {
				record.SourceConfig["poster_layout"], _ = json.Marshal(state)
				if err = client.UpdateRecommendationRecord(ctx, record.ID, tag, map[string]any{"source_config": record.SourceConfig}); err != nil {
					return nil, err
				}
				record, tag, err = client.ReadRecommendationRecord(ctx, record.ID)
				if err != nil {
					return nil, err
				}
			}
			if applied >= 20 {
				return map[string]any{"status": "partial", "applied": applied, "skipped": skipped}, nil
			}
		}

		if library == "" {
			state.Cursor = next
			state.Offset = 0
			if next != "" {
				more = true
			} else {
				state.Completed = time.Now().UTC()
			}
		}
		record.SourceConfig["poster_layout"], _ = json.Marshal(state)
		if err = client.UpdateRecommendationRecord(ctx, record.ID, tag, map[string]any{"source_config": record.SourceConfig}); err != nil {
			return nil, err
		}
	}
	status := "complete"
	if more {
		status = "partial"
	}
	return map[string]any{"status": status, "applied": applied, "skipped": skipped, "libraries": len(libs)}, nil
}

func (s *scheduledTaskServer) pollPosters() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		result, err := s.repairPosters(ctx)
		cancel()
		s.posterStatusMu.Lock()
		s.posterLastRun = time.Now().UTC()
		s.posterLastError = ""
		if err != nil {
			s.posterLastError = err.Error()
		}
		s.posterStatusMu.Unlock()
		if err != nil {
			s.log.Warn("Poster layout repair failed", "error", err)
		} else if result["applied"] != nil {
			s.log.Info("Poster layout repair", "summary", result)
		}
		// Drain a newly enabled/changed layout in bounded batches, then resume
		// hourly checks. Durable item markers prevent repeat downloads/uploads.
		wait := time.Hour
		if err != nil {
			// Connection and recommendation-owner configuration arrive separately
			// at startup. Retry readiness without postponing renewal for an hour.
			wait = 30 * time.Second
		}
		if err == nil && result["status"] == "partial" {
			wait = 5 * time.Second
		}
		select {
		case <-time.After(wait):
		case <-s.posterWake:
		}
	}
}
