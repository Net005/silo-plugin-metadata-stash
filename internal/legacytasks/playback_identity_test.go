package legacytasks

import "testing"

func TestBackfillResolvesOnlyExactArtworkIdentities(t *testing.T) {
	for raw, want := range map[string]string{
		"stash://scene/42/screenshot":                                          "42",
		"https://silo.example/image?url=stash%3A%2F%2Fscene%2F43%2Fscreenshot": "43",
		"https://jav.example/api/v1/integrations/silo/stash/scenes/44/cover":   "44",
		"https://untrusted.example/image/45":                                   "",
	} {
		if got := stashSceneFromArtwork(raw); got != want {
			t.Errorf("%q => %q want %q", raw, got, want)
		}
	}
	if got := releaseFromArtwork("https://silo.example/image?url=stash%3A%2F%2Fbackend%2Fcovers%2F7%2Fstash-poster"); got != 7 {
		t.Fatalf("release=%d", got)
	}
}
