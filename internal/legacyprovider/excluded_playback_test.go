package legacyprovider

import (
	"context"
	"testing"
)

func TestBackfillExcludedSceneDoesNotContactBackend(t *testing.T) {
	p := NewProvider()
	p.ConfigureExcludedScenes("42,stash:43")
	r, err := p.BackfillSiloPlays(context.Background(), "43", []SiloBackfillPlay{{SessionID: "play"}})
	if err != nil || r.Skipped != 1 || r.Added != 0 {
		t.Fatalf("result=%v err=%v", r, err)
	}
}
