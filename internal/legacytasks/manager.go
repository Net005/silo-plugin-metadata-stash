package legacytasks

import (
	"context"
	"sync"
	"time"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/hashicorp/go-hclog"
)

// Manager keeps the old incremental reconciliation jobs while Stash remains
// the sole metadata provider and the Stash companion owns the WatchList collection.
type Manager struct {
	provider       *provider.Provider
	tasks          *collectionSyncTaskServer
	once           sync.Once
	collectionOnce sync.Once
}

type Config struct {
	JAVBeaconURL, JAVBeaconKey                               string
	StashURL, StashKey                                       string
	SiloURL, SiloKey                                         string
	StashFilters, StashPrefix, ReleaseFilters, ReleasePrefix string
	ExcludedScenes                                           string
	BeforeCollectionSync                                     func(context.Context) error
}

func New(log hclog.Logger) *Manager {
	p := provider.NewProvider()
	return &Manager{provider: p, tasks: &collectionSyncTaskServer{runtime: &runtimeServer{provider: p}, log: log}}
}

func (m *Manager) Configure(c Config) {
	m.tasks.mu.Lock()
	m.tasks.beforeCollectionSync = c.BeforeCollectionSync
	m.tasks.mu.Unlock()
	m.provider.Configure(provider.Config{BaseURL: c.JAVBeaconURL, APIKey: c.JAVBeaconKey})
	m.provider.ConfigureStashConnection(c.StashURL, c.StashKey)
	m.provider.ConfigureExcludedScenes(c.ExcludedScenes)
	m.provider.ConfigureSiloConnection(c.SiloURL, "", c.SiloKey)
	m.provider.ConfigureSavedFilters(c.StashFilters, c.StashPrefix, c.ReleaseFilters, c.ReleasePrefix)
	if c.SiloURL != "" && c.SiloKey != "" && ((c.StashURL != "" && c.StashKey != "") || (c.JAVBeaconURL != "" && c.JAVBeaconKey != "")) {
		m.collectionOnce.Do(func() { go m.tasks.poll(); go m.tasks.pollWatchListCollection() })
	}
	if c.JAVBeaconURL != "" && c.JAVBeaconKey != "" && c.SiloURL != "" && c.SiloKey != "" {
		m.once.Do(func() {
			go m.tasks.pollMetadata()
			go m.tasks.pollWatched()
			go m.tasks.pollRepair()
		})
	}
}

func (m *Manager) Run(ctx context.Context, req *pluginv1.RunScheduledTaskRequest) (*pluginv1.RunScheduledTaskResponse, error) {
	return m.tasks.Run(ctx, req)
}

func (m *Manager) Provider() *provider.Provider { return m.provider }

// Wait for an existing reconciliation before admitting this fresh Stash hook.
func (m *Manager) ReconcileWatchlist(ctx context.Context) error {
	for {
		done, started := m.tasks.startWatchListCollectionSync()
		if started {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case result := <-done:
				return result.err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
