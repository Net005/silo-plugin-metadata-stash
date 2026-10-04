package legacytasks

import (
	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	"testing"
)

func TestSavedFiltersSurviveDifferentStashAndSiloPaths(t *testing.T) {
	local := provider.CatalogItem{ContentID: "local-42", Title: "A Descriptive Stash Title", Type: "movie", PosterURL: "https://silo.example/image?url=stash%3A%2F%2Fbackend%2Fcovers%2F7%2Fstash-poster"}
	snapshot := &provider.LibrarySync{FilterPresets: []provider.FilterPresetCollection{{ID: 3, Name: "Selected", ReleaseIDs: []int64{7}}}, ReleaseCodes: map[int64]string{7: "ATID-7"}, ReleasePaths: map[int64]string{7: "/stash/different-name.mp4"}, ReleaseSceneIDs: map[int64]string{7: "42"}}
	specs := collectionSpecsFromCatalog(snapshot, []provider.CatalogItem{local}, snapshot.ReleaseCodes, "16")
	if len(specs) != 2 || len(specs[1].MediaIDs) != 1 || specs[1].MediaIDs[0] != "local-42" {
		t.Fatalf("release filter=%+v", specs)
	}
	filters := []provider.StashSavedFilter{{ID: "9", Name: "Stash Picks", Items: []provider.StashSavedFilterItem{{SceneID: "42", Path: "/stash/other-copy.mp4", Title: "Different Title"}}}}
	stashSpecs := stashFilterSpecsFromCatalog(filters, []provider.CatalogItem{local}, "16", "Stash | ", snapshot.ReleaseSceneIDs)
	if len(stashSpecs) != 1 || len(stashSpecs[0].MediaIDs) != 1 || stashSpecs[0].MediaIDs[0] != "local-42" {
		t.Fatalf("Stash filter=%+v", stashSpecs)
	}
}

func TestSavedFilterSkipsAmbiguousTitleFallback(t *testing.T) {
	filters := []provider.StashSavedFilter{{ID: "9", Name: "Duplicates", Items: []provider.StashSavedFilterItem{{SceneID: "42", Title: "Same"}}}}
	catalog := []provider.CatalogItem{{ContentID: "one", Title: "Same", Type: "movie"}, {ContentID: "two", Title: "Same", Type: "movie"}}
	specs := stashFilterSpecsFromCatalog(filters, catalog, "16", "")
	if len(specs) != 1 || len(specs[0].MediaIDs) != 0 {
		t.Fatalf("ambiguous match=%+v", specs)
	}
}
