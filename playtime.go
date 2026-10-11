package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
)

var errPlaytimeStorage = errors.New("playtime: unable to reserve checkpoint")

type playtimeStore interface {
	ReadInstanceState(context.Context, string) ([]byte, bool, error)
	WriteInstanceState(context.Context, string, []byte) error
}
type playtimeSample struct {
	Session provider.PlaybackSession
	Scene   string
}
type playtimeState struct {
	Pending map[string]float64
	Samples map[string]playtimeSample
}

// Credit real elapsed time only between fresh, advancing observations.
// Seeks, pauses, stale heartbeats and long gaps establish a new baseline.
func activePlaytime(before, after provider.PlaybackSession, now time.Time) float64 {
	dt := after.Updated.Sub(before.Updated).Seconds()
	dp := after.Position - before.Position
	if before.ID == "" || before.ID != after.ID || before.ContentID != after.ContentID || before.FileID != after.FileID || before.Profile != after.Profile || before.Paused || after.Paused ||
		dt <= 0 || dt > 30 || now.Sub(after.Updated) > 30*time.Second || after.Updated.After(now.Add(2*time.Second)) ||
		math.IsNaN(dp) || math.IsInf(dp, 0) || dp < dt*0.25 || dp > dt*4+1 || before.Position < 0 || after.Position < 0 {
		return 0
	}
	return dt
}

func (s *runtimeServer) playtimeConfig() (string, string, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile := s.playtimeProfile
	if profile == "" {
		profile = s.recommendationConfig.Profile
	}
	return s.siloBase, s.siloKey, profile, !s.playtimeDisabled && s.siloBase != "" && s.siloKey != "" && profile != "" && s.client.configured()
}
func (s *runtimeServer) measuredPlaybackConfigured() bool {
	_, _, _, ok := s.playtimeConfig()
	return ok
}

func loadPlaytime(ctx context.Context, store playtimeStore, key string) (*playtimeState, error) {
	data, found, err := store.ReadInstanceState(ctx, key)
	if err != nil {
		return nil, err
	}
	state := &playtimeState{Pending: map[string]float64{}, Samples: map[string]playtimeSample{}}
	if found {
		if err = json.Unmarshal(data, state); err != nil {
			return nil, err
		}
		if state.Pending == nil {
			state.Pending = map[string]float64{}
		}
	}
	// Never credit time while the plugin was stopped, even after a quick restart.
	state.Samples = map[string]playtimeSample{}
	return state, nil
}
func savePlaytime(ctx context.Context, store playtimeStore, key string, state *playtimeState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return store.WriteInstanceState(ctx, key, data)
}

func (state *playtimeState) observe(rows []provider.PlaybackSession, profile string, now time.Time, resolve func(provider.PlaybackSession) (string, error)) error {
	next := map[string]playtimeSample{}
	seen := map[string]bool{}
	for _, row := range rows {
		if row.ID == "" || row.ContentID == "" || row.Profile != profile || row.MediaType != "movie" || row.Updated.IsZero() {
			continue
		}
		if seen[row.ID] {
			return fmt.Errorf("playtime: duplicate session identity")
		}
		seen[row.ID] = true
		old, exists := state.Samples[row.ID]
		// Reject duplicate/out-of-order snapshots without moving the baseline back.
		if exists && !row.Updated.After(old.Session.Updated) {
			next[row.ID] = old
			continue
		}
		id := old.Scene
		if !exists || old.Session.ContentID != row.ContentID || old.Session.FileID != row.FileID {
			var err error
			id, err = resolve(row)
			if err != nil {
				return err
			}
		}
		if id == "" {
			continue
		}
		if len(state.Pending) >= 256 && state.Pending[id] == 0 {
			continue
		}
		if exists {
			state.Pending[id] += activePlaytime(old.Session, row, now)
		}
		next[row.ID] = playtimeSample{Session: row, Scene: id}
	}
	state.Samples = next
	return nil
}

// Reserve each additive mutation durably BEFORE sending. Stash has no
// idempotency key for sceneSaveActivity: after an ambiguous response, replay
// could double count. At worst one checkpoint is lost; it is never replayed.
func (state *playtimeState) flush(ctx context.Context, store playtimeStore, key string, stash *stashClient) error {
	for id, delta := range state.Pending {
		if delta <= 0 {
			delete(state.Pending, id)
			continue
		}
		if stash.excludedScenes[id] {
			delete(state.Pending, id)
			continue
		}
		row, err := stash.findScene(ctx, id)
		if err != nil {
			return fmt.Errorf("playtime: Stash unavailable; checkpoint retained")
		}
		if row == nil {
			continue
		}
		delete(state.Pending, id)
		if err = savePlaytime(ctx, store, key, state); err != nil {
			return errPlaytimeStorage
		}
		if err = stash.addPlayDuration(ctx, id, delta); err != nil {
			return fmt.Errorf("playtime: checkpoint response uncertain; not replaying additive write")
		}
	}
	return nil
}

func (s *runtimeServer) pollPlaytime() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var state *playtimeState
	var scope string
	for range ticker.C {
		base, key, profile, enabled := s.playtimeConfig()
		if !enabled {
			state = nil
			s.setPlaytimeHealth(false, false, "Configure Silo credentials and an owner profile", 0)
			continue
		}
		stash := s.stash()
		sum := sha256.Sum256([]byte(base + "\x00" + stash.base + "\x00" + profile))
		stateKey := fmt.Sprintf("stash-playtime-v1-%x", sum[:16])
		if scope != stateKey {
			state = nil
			scope = stateKey
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		host := runtime.Host()
		if host == nil {
			s.setPlaytimeHealth(true, false, "Private host state unavailable", 0)
			cancel()
			continue
		}
		var err error
		keepState := false
		active := 0
		first := state == nil
		if state == nil {
			state, err = loadPlaytime(ctx, host, stateKey)
		}
		if err == nil {
			before, _ := json.Marshal(state)
			client := provider.NewSiloClient(base, key)
			var rows []provider.PlaybackSession
			rows, err = client.PlaybackSessions(ctx)
			for _, row := range rows {
				if row.Profile == profile {
					active++
				}
			}
			if err == nil {
				err = state.observe(rows, profile, time.Now(), func(row provider.PlaybackSession) (string, error) {
					paths, e := client.ItemFilePaths(ctx, row.ContentID)
					if e != nil {
						return "", e
					}
					id, e := stash.sceneIDForExactPaths(ctx, paths)
					if stash.excludedScenes[id] {
						return "", nil
					}
					return id, e
				})
			}
			if err == nil {
				after, _ := json.Marshal(state)
				if first || !bytes.Equal(before, after) {
					err = savePlaytime(ctx, host, stateKey, state)
				}
			}
			if err == nil {
				err = state.flush(ctx, host, stateKey, stash)
				keepState = !errors.Is(err, errPlaytimeStorage)
			}
		}
		if err == nil {
			s.setPlaytimeHealth(true, true, "", active)
		}
		if err != nil {
			s.setPlaytimeHealth(true, false, "Checkpoint unavailable; waiting for fresh telemetry", active)
			// Reload after uncertain storage writes, and rebaseline after outages.
			if !keepState {
				state = nil
			}
			if s.task != nil && s.task.log != nil {
				s.task.log.Warn("Active playtime checkpoint interrupted; fresh telemetry required")
			}
		}
		cancel()
	}
}

func (s *runtimeServer) setPlaytimeHealth(enabled, ready bool, issue string, active int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.playtimeHealth = map[string]any{"enabled": enabled, "private_state_ready": ready, "last_check": time.Now().UTC(), "last_error": issue, "active_sessions": active}
}
func (s *runtimeServer) playtimeStatus() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]any{}
	for k, v := range s.playtimeHealth {
		out[k] = v
	}
	return out
}
