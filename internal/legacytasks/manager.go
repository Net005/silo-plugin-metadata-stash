package legacytasks

import (
	"context"
	"sync"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/hashicorp/go-hclog"
)

// Manager keeps the old incremental reconciliation jobs while Stash remains
// the sole metadata provider and the Stash companion owns the WatchList collection.
type Manager struct {
	provider *provider.Provider
	tasks    *collectionSyncTaskServer
	once     sync.Once
}

type Config struct {
	JAVBeaconURL, JAVBeaconKey                               string
	SiloURL, SiloKey, SiloLibraryID                          string
	StashFilters, StashPrefix, ReleaseFilters, ReleasePrefix string
	WatchListCollectionID                                    string
}

func New(log hclog.Logger) *Manager {
	p := provider.NewProvider()
	return &Manager{provider: p, tasks: &collectionSyncTaskServer{runtime: &runtimeServer{provider: p}, log: log}}
}

func (m *Manager) Configure(c Config) {
	m.provider.Configure(provider.Config{BaseURL: c.JAVBeaconURL, APIKey: c.JAVBeaconKey})
	m.provider.ConfigureSiloConnection(c.SiloURL, c.SiloLibraryID, c.SiloKey)
	m.provider.ConfigureSavedFilters(c.StashFilters, c.StashPrefix, c.ReleaseFilters, c.ReleasePrefix)
	m.tasks.mu.Lock()
	m.tasks.watchListCollectionID = c.WatchListCollectionID
	m.tasks.mu.Unlock()
	if c.JAVBeaconURL != "" && c.JAVBeaconKey != "" && c.SiloURL != "" && c.SiloKey != "" {
		m.once.Do(func() {
			go m.tasks.poll()
			go m.tasks.pollMetadata()
			go m.tasks.pollWatched()
			go m.tasks.pollRepair()
			go m.tasks.pollWatchListCollection()
		})
	}
}

func (m *Manager) Run(ctx context.Context, req *pluginv1.RunScheduledTaskRequest) (*pluginv1.RunScheduledTaskResponse, error) {
	return m.tasks.Run(ctx, req)
}

func (m *Manager) Provider() *provider.Provider { return m.provider }
