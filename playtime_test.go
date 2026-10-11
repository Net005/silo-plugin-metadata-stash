package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	legacytasks "github.com/Net005/silo-plugin-metadata-stash/internal/legacytasks"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func playtimeRow(at time.Time, pos float64) provider.PlaybackSession {
	return provider.PlaybackSession{ID: "session", ContentID: "item", FileID: 7, Profile: "owner", MediaType: "movie", Position: pos, Updated: at}
}
func TestActivePlaytime(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	before := playtimeRow(at, 120)
	after := playtimeRow(at.Add(5*time.Second), 125)
	tests := []struct {
		name string
		edit func(*provider.PlaybackSession, *provider.PlaybackSession)
		want float64
	}{
		{"normal", func(a, b *provider.PlaybackSession) {}, 5},
		{"double speed", func(a, b *provider.PlaybackSession) { b.Position = 130 }, 5},
		{"half speed", func(a, b *provider.PlaybackSession) { b.Position = 122.5 }, 5},
		{"forward seek", func(a, b *provider.PlaybackSession) { b.Position = 7400 }, 0},
		{"backward seek", func(a, b *provider.PlaybackSession) { b.Position = 30 }, 0},
		{"pause", func(a, b *provider.PlaybackSession) { b.Paused = true }, 0},
		{"resume", func(a, b *provider.PlaybackSession) { a.Paused = true }, 0},
		{"buffer", func(a, b *provider.PlaybackSession) { b.Position = a.Position }, 0},
		{"gap", func(a, b *provider.PlaybackSession) { b.Updated = a.Updated.Add(time.Hour); b.Position = 3720 }, 0},
		{"duplicate", func(a, b *provider.PlaybackSession) { b.Updated = a.Updated }, 0},
		{"file change", func(a, b *provider.PlaybackSession) { b.FileID++ }, 0},
		{"profile change", func(a, b *provider.PlaybackSession) { b.Profile = "other" }, 0},
		{"invalid", func(a, b *provider.PlaybackSession) { b.Position = math.NaN() }, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := before, after
			tt.edit(&a, &b)
			if got := activePlaytime(a, b, b.Updated); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
	if got := activePlaytime(before, after, after.Updated.Add(time.Minute)); got != 0 {
		t.Fatal("credited stale session")
	}
}

type memoryPlaytimeStore struct {
	data []byte
	fail bool
}

func (m *memoryPlaytimeStore) ReadInstanceState(context.Context, string) ([]byte, bool, error) {
	return append([]byte(nil), m.data...), len(m.data) > 0, nil
}
func (m *memoryPlaytimeStore) WriteInstanceState(_ context.Context, _ string, data []byte) error {
	if m.fail {
		return errors.New("storage unavailable")
	}
	m.data = append([]byte(nil), data...)
	return nil
}
func TestPlaytimeSamplingAndRestart(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st := &playtimeState{Pending: map[string]float64{}, Samples: map[string]playtimeSample{}}
	calls := 0
	resolve := func(provider.PlaybackSession) (string, error) { calls++; return "42", nil }
	observe := func(row provider.PlaybackSession) {
		t.Helper()
		if err := st.observe([]provider.PlaybackSession{row}, "owner", row.Updated, resolve); err != nil {
			t.Fatal(err)
		}
	}
	observe(playtimeRow(at, 100))
	observe(playtimeRow(at.Add(5*time.Second), 105))
	observe(playtimeRow(at.Add(5*time.Second), 105))
	observe(playtimeRow(at.Add(3*time.Second), 103))
	observe(playtimeRow(at.Add(10*time.Second), 110))
	observe(playtimeRow(at.Add(15*time.Second), 7000))
	observe(playtimeRow(at.Add(20*time.Second), 7005))
	if st.Pending["42"] != 15 || calls != 1 {
		t.Fatalf("pending=%v calls=%d", st.Pending, calls)
	}
	store := &memoryPlaytimeStore{}
	if err := savePlaytime(t.Context(), store, "test", st); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadPlaytime(t.Context(), store, "test")
	if err != nil || reloaded.Pending["42"] != 15 || len(reloaded.Samples) != 0 {
		t.Fatalf("restart: %v %v", reloaded, err)
	}
	st = reloaded
	observe(playtimeRow(at.Add(25*time.Second), 7010))
	if st.Pending["42"] != 15 {
		t.Fatal("counted restart gap")
	}
	// Other profiles and non-movies never enter the sample set.
	other := playtimeRow(at.Add(30*time.Second), 7015)
	other.Profile = "other"
	observe(other)
	if len(st.Samples) != 0 {
		t.Fatal("counted another profile")
	}
	// Identity failures stop sampling without inventing a scene.
	if err = st.observe([]provider.PlaybackSession{playtimeRow(at, 1)}, "owner", at, func(provider.PlaybackSession) (string, error) { return "", errors.New("ambiguous") }); err == nil {
		t.Fatal("accepted ambiguous identity")
	}
}
func TestPlaytimeFlushDurableAndDoesNotReplay(t *testing.T) {
	for _, mode := range []string{"success", "unknown response", "read failure", "storage failure"} {
		t.Run(mode, func(t *testing.T) {
			st := &playtimeState{Pending: map[string]float64{"42": 5}, Samples: map[string]playtimeSample{}}
			store := &memoryPlaytimeStore{}
			if err := savePlaytime(t.Context(), store, "test", st); err != nil {
				t.Fatal(err)
			}
			writes := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct{ Query string }
				_ = json.NewDecoder(r.Body).Decode(&input)
				if strings.Contains(input.Query, "sceneSaveActivity") {
					writes++
					if strings.Contains(input.Query, "resume_time") || strings.Contains(input.Query, "sceneAddPlay") {
						t.Error("duration write changed resume or play count")
					}
					// The durable reservation must already have removed this delta.
					var saved playtimeState
					_ = json.Unmarshal(store.data, &saved)
					if saved.Pending["42"] != 0 {
						t.Error("sent mutation before durable reservation")
					}
					if mode == "unknown response" {
						w.WriteHeader(502)
						return
					}
					_, _ = w.Write([]byte("{\"data\":{\"sceneSaveActivity\":true}}"))
				} else {
					if mode == "read failure" {
						w.WriteHeader(503)
						return
					}
					_, _ = w.Write([]byte("{\"data\":{\"findScene\":{\"id\":\"42\"}}}"))
				}
			}))
			defer backend.Close()
			client := &stashClient{base: backend.URL, key: "test"}
			store.fail = mode == "storage failure"
			err := st.flush(t.Context(), store, "test", client)
			if (err != nil) != (mode != "success") {
				t.Fatalf("err=%v", err)
			}
			if (mode == "read failure" || mode == "storage failure") && writes != 0 {
				t.Fatal("wrote without read/storage confirmation")
			}
			reloaded, loadErr := loadPlaytime(t.Context(), store, "test")
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if mode == "read failure" || mode == "storage failure" {
				if reloaded.Pending["42"] != 5 {
					t.Fatal("lost pending delta before sending")
				}
			} else {
				if reloaded.Pending["42"] != 0 {
					t.Fatal("would replay additive delta")
				}
				if e := reloaded.flush(t.Context(), store, "test", client); e != nil {
					t.Fatal(e)
				}
				if writes != 1 {
					t.Fatalf("duplicate writes=%d", writes)
				}
			}
		})
	}
}
func TestMeasuredPlaytimeBypassesLegacyDurationWriter(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("duplicate legacy playback writer")
		w.WriteHeader(500)
	}))
	defer backend.Close()
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct{ Query string }
		_ = json.NewDecoder(r.Body).Decode(&input)
		if !strings.Contains(input.Query, "playDuration:0.000") {
			t.Errorf("unexpected write: %s", input.Query)
		}
		_, _ = w.Write([]byte("{\"data\":{\"sceneSaveActivity\":true}}"))
	}))
	defer stash.Close()
	manager := legacytasks.New(nil)
	manager.Configure(legacytasks.Config{JAVBeaconURL: backend.URL, JAVBeaconKey: "test"})
	rs := &runtimeServer{client: &stashClient{base: stash.URL, key: "test"}, legacy: manager, siloBase: "http://silo.invalid", siloKey: "test", playtimeProfile: "owner"}
	result := (&watchSyncServer{runtime: rs}).applyOne(t.Context(), &pluginv1.WatchSyncEvent{EventId: "event", ProviderItemKey: "stash:42", Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_PAUSE, PositionSeconds: 7400})
	if result.Status != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED {
		t.Fatalf("result=%v", result)
	}
}
