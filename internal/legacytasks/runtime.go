package legacytasks

import provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"

type runtimeServer struct{ provider *provider.Provider }

const capabilityID = "stash"
