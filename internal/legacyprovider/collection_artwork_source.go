package legacyprovider

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// ResolveCollectionArtwork uses the selected Stash scene, never a previous
// Silo/TMDB poster. The result is uploaded into Silo storage by the caller.
func (p *Provider) ResolveCollectionArtwork(ctx context.Context, art CollectionArtwork) (CollectionArtwork, error) {
	if art.StashSceneID == "" {
		return art, nil
	}
	art.PosterURL, art.BackdropURL = "", ""
	if c, err := p.activeClient(); err == nil {
		var source struct {
			SceneID       string   `json:"scene_id"`
			PosterPath    string   `json:"poster_path"`
			BackdropPaths []string `json:"backdrop_paths"`
		}
		if err := c.getJSON(ctx, "/api/v1/integrations/stash/enrichment/"+url.PathEscape(art.StashSceneID), &source); err == nil && source.SceneID == art.StashSceneID {
			if collectionSourcePath(source.PosterPath) {
				art.PosterURL = c.ImageURL(source.PosterPath)
			}
			for _, path := range source.BackdropPaths {
				if collectionSourcePath(path) {
					art.BackdropURL = c.ImageURL(path)
					break
				}
			}
			if art.PosterURL != "" && art.BackdropURL != "" {
				return art, nil
			}
		}
	}
	base, key, err := p.stashConnection()
	if err != nil {
		return art, err
	}
	var result struct {
		Data struct {
			Scene *struct {
				ID    string `json:"id"`
				Paths struct {
					Screenshot string `json:"screenshot"`
				} `json:"paths"`
			} `json:"findScene"`
		} `json:"data"`
	}
	if err := p.savedFilterGraphQL(ctx, base, key, `query($id:ID!){findScene(id:$id){id paths{screenshot}}}`, map[string]any{"id": art.StashSceneID}, &result); err != nil {
		return art, err
	}
	if result.Data.Scene == nil || result.Data.Scene.ID != art.StashSceneID {
		return art, fmt.Errorf("collection artwork: Stash scene unavailable")
	}
	u, err := url.Parse(result.Data.Scene.Paths.Screenshot)
	origin, _ := url.Parse(base)
	if err != nil || u.Host != origin.Host || (u.Scheme != "http" && u.Scheme != "https") {
		return art, fmt.Errorf("collection artwork: invalid Stash screenshot source")
	}
	q := u.Query()
	q.Set("apikey", key)
	u.RawQuery = q.Encode()
	if art.PosterURL == "" {
		art.PosterURL = u.String()
	}
	if art.BackdropURL == "" {
		art.BackdropURL = u.String()
	}
	return art, nil
}

func collectionSourcePath(path string) bool {
	return (strings.HasPrefix(path, "/covers/") || strings.HasPrefix(path, "/screenshots/") || strings.HasPrefix(path, "/api/v1/integrations/silo/stash/scenes/")) && !strings.Contains(path, "..") && !strings.ContainsAny(path, "?#\\")
}
