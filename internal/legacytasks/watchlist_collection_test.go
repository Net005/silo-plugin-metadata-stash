package legacytasks

import (
	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	"testing"
)

func TestWatchListRecoveryNeedsExactIdentity(t *testing.T) {
	scenePath := "/stash/original.mp4"
	local := provider.CatalogItem{ContentID: "local-42", Title: "Same Title", Type: "movie", PosterURL: "stash://scene/42/screenshot"}
	snapshot := &provider.LibrarySync{Watchlist: []provider.LibrarySyncItem{{StashSceneID: "42", Path: scenePath}, {StashSceneID: "99", Title: "Same Title", Path: "/stash/unknown.mp4"}}}
	ids, resolved := exactWatchListMembers(snapshot, []provider.CatalogItem{local})
	if len(ids) != 1 || ids[0] != "local-42" || !resolved[scenePath] || resolved["/stash/unknown.mp4"] {
		t.Fatalf("ids=%v resolved=%v", ids, resolved)
	}
}
