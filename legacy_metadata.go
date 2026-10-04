package main

import (
	"context"
	"strconv"
	"strings"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func legacyReleaseID(raw string, ids *structpb.Struct) int64 {
	if ids != nil {
		values := ids.AsMap()
		if value := text(values["javbeacon"]); value != "" {
			raw = value
		} else if value := text(values["stash"]); strings.HasPrefix(value, "javbeacon:") {
			raw = value
		}
	}
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "javbeacon:")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func legacyImagePath(path string) string {
	if path == "" || !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return ""
	}
	return "javbeacon://" + strings.TrimPrefix(path, "/")
}

func legacyIDs(m *provider.Metadata) *structpb.Struct {
	ids := map[string]any{"javbeacon": strconv.FormatInt(m.ReleaseID, 10)}
	if m.StashSceneID != "" {
		ids["stash"] = m.StashSceneID
	}
	out, _ := structpb.NewStruct(ids)
	return out
}

func legacySearchResult(m *provider.Metadata) *pluginv1.ProviderSearchResult {
	title := m.Code
	if title == "" {
		title = m.Title
	}
	return &pluginv1.ProviderSearchResult{ProviderId: "javbeacon:" + strconv.FormatInt(m.ReleaseID, 10), ItemType: "movie", Title: title, Overview: m.Overview, Year: int32(m.ProductionYear), ProviderIds: legacyIDs(m), ImageUrl: legacyImagePath(m.CoverPath)}
}

func legacyMetadataItem(m *provider.Metadata) *pluginv1.MetadataItem {
	title := m.Code
	if title == "" {
		title = m.Title
	}
	genres := []string{}
	for _, genre := range m.Genres {
		if strings.EqualFold(genre, "Watchlist") || strings.HasPrefix(strings.ToLower(genre), "collection: ") {
			continue
		}
		genres = append(genres, genre)
	}
	people := []*pluginv1.PersonRecord{}
	for i, name := range m.Performers {
		p := &pluginv1.PersonRecord{Name: name, Kind: "actor", SortOrder: int32(i), PhotoPath: legacyImagePath(m.PerformerImages[name])}
		if detail, ok := m.PerformerDetails[name]; ok && detail.StashID != "" {
			p.PlexGuid = "stash:" + detail.StashID
		}
		people = append(people, p)
	}
	for _, name := range m.Directors {
		people = append(people, &pluginv1.PersonRecord{Name: name, Kind: "director"})
	}
	studios := []string{}
	if m.Studio != "" {
		studios = append(studios, m.Studio)
	}
	poster := legacyImagePath(m.CoverPath)
	if poster == "" {
		poster = legacyImagePath(m.StashPosterURL)
	}
	if poster == "" {
		poster = legacyImagePath(m.StashScreenshotURL)
	}
	backdrop := legacyImagePath(m.CoverBackdropPath)
	if backdrop == "" && len(m.BackdropURLs) > 0 {
		backdrop = legacyImagePath(m.BackdropURLs[0])
	}
	if backdrop == "" {
		backdrop = legacyImagePath(m.StashScreenshotURL)
	}
	return &pluginv1.MetadataItem{ProviderId: "javbeacon:" + strconv.FormatInt(m.ReleaseID, 10), ItemType: "movie", Title: title, OriginalTitle: m.OriginalTitle, SortTitle: title, Year: int32(m.ProductionYear), Overview: m.Overview, Runtime: int32(m.RuntimeSeconds / 60), Genres: genres, ProviderIds: legacyIDs(m), ReleaseDate: m.PremiereDate, PosterPath: poster, BackdropPath: backdrop, People: people, Studios: studios}
}

func legacyImages(m *provider.Metadata) []*pluginv1.ImageRecord {
	images := []*pluginv1.ImageRecord{}
	add := func(kind, path string) {
		if url := legacyImagePath(path); url != "" {
			images = append(images, &pluginv1.ImageRecord{Kind: kind, Url: url})
		}
	}
	add("poster", m.CoverPath)
	add("backdrop", m.CoverBackdropPath)
	for _, path := range m.BackdropURLs {
		add("backdrop", path)
	}
	if m.StashScreenshotURL != "" {
		poster := m.StashPosterURL
		if poster == "" {
			poster = m.StashScreenshotURL
		}
		add("poster", poster)
		add("backdrop", m.StashScreenshotURL)
	}
	return images
}

func (s *metadataServer) fetchLegacy(ctx context.Context, id int64) (*provider.Metadata, error) {
	if id == 0 || s.runtime.legacy == nil || !s.runtime.legacy.Provider().Configured() {
		return nil, nil
	}
	return s.runtime.legacy.Provider().GetMetadata(ctx, id)
}
