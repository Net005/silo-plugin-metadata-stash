package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	legacyprovider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	legacytasks "github.com/Net005/silo-plugin-metadata-stash/internal/legacytasks"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtimedefault"
	"github.com/hashicorp/go-hclog"
	"google.golang.org/protobuf/types/known/structpb"
)

//go:embed manifest.json
var manifestJSON []byte
var version string

const capabilityID = "stash"

type runtimeServer struct {
	runtimedefault.Server
	manifest                                                 *pluginv1.PluginManifest
	mu                                                       sync.RWMutex
	client                                                   *stashClient
	artwork                                                  *artworkClient
	siloBase                                                 string
	siloKey                                                  string
	pollOnce                                                 sync.Once
	task                                                     *scheduledTaskServer
	legacy                                                   *legacytasks.Manager
	siloLibraryID                                            string
	stashFilters, stashPrefix, releaseFilters, releasePrefix string
	watchListCollectionID                                    string
}

func (s *runtimeServer) GetManifest(context.Context, *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	return &pluginv1.GetManifestResponse{Manifest: s.manifest}, nil
}
func (s *runtimeServer) Configure(_ context.Context, req *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	for _, entry := range req.GetConfig() {
		if entry.GetKey() != "connection" {
			continue
		}
		v := entry.GetValue().AsMap()
		s.mu.Lock()
		key := text(v["api_key"])
		if key == "" && s.client != nil {
			key = s.client.key
		}
		s.client = &stashClient{base: strings.TrimRight(text(v["base_url"]), "/"), key: key}
		artKey := text(v["javbeacon_api_key"])
		if artKey == "" && s.artwork != nil {
			artKey = s.artwork.key
		}
		s.artwork = &artworkClient{base: strings.TrimRight(text(v["javbeacon_url"]), "/"), key: artKey}
		s.siloBase = strings.TrimRight(text(v["silo_base_url"]), "/")
		siloKey := text(v["silo_api_key"])
		if siloKey != "" {
			s.siloKey = siloKey
		}
		s.siloLibraryID = text(v["silo_library_id"])
		s.stashFilters = text(v["stash_saved_filter_selection"])
		s.stashPrefix = text(v["stash_saved_filter_prefix"])
		s.releaseFilters = text(v["saved_filter_selection"])
		s.releasePrefix = text(v["saved_filter_prefix"])
		s.watchListCollectionID = text(v["silo_watchlist_collection_id"])
		legacy := s.legacy
		config := legacytasks.Config{JAVBeaconURL: s.artwork.base, JAVBeaconKey: s.artwork.key, SiloURL: s.siloBase, SiloKey: s.siloKey, SiloLibraryID: s.siloLibraryID, StashFilters: s.stashFilters, StashPrefix: s.stashPrefix, ReleaseFilters: s.releaseFilters, ReleasePrefix: s.releasePrefix, WatchListCollectionID: s.watchListCollectionID}
		s.mu.Unlock()
		if legacy != nil {
			legacy.Configure(config)
		}
		s.pollOnce.Do(func() { go s.pollMatching() })
	}
	return &pluginv1.ConfigureResponse{}, nil
}
func text(v any) string                      { s, _ := v.(string); return strings.TrimSpace(s) }
func (s *runtimeServer) stash() *stashClient { s.mu.RLock(); defer s.mu.RUnlock(); return s.client }
func (s *runtimeServer) artworkClient() *artworkClient {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.artwork
}
func (s *runtimeServer) sceneArtwork(ctx context.Context, id string) *sceneArtwork {
	art, err := s.artworkClient().fetch(ctx, id)
	if err != nil {
		return nil
	}
	return art
}

type metadataServer struct {
	pluginv1.UnimplementedMetadataProviderServer
	pluginv1.UnimplementedImageResolverServer
	runtime *runtimeServer
	people  personQueue
}

func supported(t string) bool { return t == "" || t == "movie" }
func sceneID(raw string, ids *structpb.Struct) string {
	if ids != nil {
		v := ids.AsMap()
		if id := text(v[capabilityID]); id != "" && !strings.HasPrefix(id, "javbeacon:") {
			return strings.TrimPrefix(id, "stash:")
		}
	}
	id, ok := strings.CutPrefix(strings.TrimSpace(raw), "stash:")
	if ok {
		return id
	}
	return ""
}
func providerIDs(id string) *structpb.Struct {
	x, _ := structpb.NewStruct(map[string]any{capabilityID: id})
	return x
}
func providerIDsWithRelease(id string, releaseID int64) *structpb.Struct {
	values := map[string]any{capabilityID: id}
	if releaseID > 0 {
		values["javbeacon"] = strconv.FormatInt(releaseID, 10)
	}
	out, _ := structpb.NewStruct(values)
	return out
}
func displayTitle(s scene) string {
	if strings.TrimSpace(s.Title) != "" {
		return s.Title
	}
	return s.Code
}
func searchResult(s scene) *pluginv1.ProviderSearchResult {
	imagePath := ""
	if s.Paths.Screenshot != "" {
		imagePath = coverPath(s.ID)
	}
	return &pluginv1.ProviderSearchResult{ProviderId: "stash:" + s.ID, ItemType: "movie", Title: displayTitle(s), Overview: s.Details, ProviderIds: providerIDs(s.ID), ImageUrl: imagePath}
}
func coverPath(id string) string {
	if id == "" {
		return ""
	}
	return "stash://scene/" + id + "/screenshot"
}
func (s *metadataServer) Search(ctx context.Context, req *pluginv1.SearchMetadataRequest) (*pluginv1.SearchMetadataResponse, error) {
	if !supported(req.GetItemType()) {
		return &pluginv1.SearchMetadataResponse{}, nil
	}
	c := s.runtime.stash()
	if id := sceneID("", req.GetProviderIds()); id != "" && c.configured() {
		row, err := c.findScene(ctx, id)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return &pluginv1.SearchMetadataResponse{}, nil
		}
		result := searchResult(*row)
		if art := s.runtime.sceneArtwork(ctx, row.ID); art != nil {
			result.ProviderIds = providerIDsWithRelease(row.ID, art.ReleaseID)
			if art.PosterPath != "" {
				result.ImageUrl = backendImagePath(art.PosterPath)
			}
		}
		return &pluginv1.SearchMetadataResponse{Results: []*pluginv1.ProviderSearchResult{result}}, nil
	}
	if releaseID := legacyReleaseID("", req.GetProviderIds()); releaseID > 0 {
		release, err := s.fetchLegacy(ctx, releaseID)
		if err != nil {
			return nil, err
		}
		if release != nil {
			if release.StashSceneID != "" && c.configured() {
				row, err := c.findScene(ctx, release.StashSceneID)
				if err != nil {
					return nil, err
				}
				if row != nil {
					result := searchResult(*row)
					result.ProviderIds = providerIDsWithRelease(row.ID, releaseID)
					return &pluginv1.SearchMetadataResponse{Results: []*pluginv1.ProviderSearchResult{result}}, nil
				}
			}
			return &pluginv1.SearchMetadataResponse{Results: []*pluginv1.ProviderSearchResult{legacySearchResult(release)}}, nil
		}
	}
	out := &pluginv1.SearchMetadataResponse{}
	if c.configured() {
		rows, err := c.search(ctx, req.GetQuery())
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			result := searchResult(row)
			if art := s.runtime.sceneArtwork(ctx, row.ID); art != nil {
				result.ProviderIds = providerIDsWithRelease(row.ID, art.ReleaseID)
				if art.PosterPath != "" {
					result.ImageUrl = backendImagePath(art.PosterPath)
				}
			}
			out.Results = append(out.Results, result)
		}
	}
	if len(out.Results) == 0 && s.runtime.legacy != nil && s.runtime.legacy.Provider().Configured() {
		releases, err := s.runtime.legacy.Provider().Search(ctx, req.GetQuery(), 25)
		if err != nil {
			return nil, err
		}
		for i := range releases {
			if releases[i].ReleaseID > 0 {
				out.Results = append(out.Results, legacySearchResult(&releases[i]))
			}
		}
	}
	return out, nil
}
func (s *metadataServer) GetMetadata(ctx context.Context, req *pluginv1.GetMetadataRequest) (*pluginv1.GetMetadataResponse, error) {
	if !supported(req.GetItemType()) {
		return &pluginv1.GetMetadataResponse{}, nil
	}
	id := sceneID(req.GetProviderId(), req.GetProviderIds())
	var release *legacyprovider.Metadata
	if id == "" {
		var err error
		release, err = s.fetchLegacy(ctx, legacyReleaseID(req.GetProviderId(), req.GetProviderIds()))
		if err != nil {
			return nil, err
		}
		if release != nil {
			id = release.StashSceneID
		}
	}
	c := s.runtime.stash()
	if id != "" && c.configured() {
		row, err := c.findScene(ctx, id)
		if err != nil {
			return nil, err
		}
		if row != nil {
			item := metadataItem(*row)
			if release != nil {
				item.ProviderIds = providerIDsWithRelease(id, release.ReleaseID)
			}
			if s.runtime.legacy != nil && s.runtime.legacy.Provider().Configured() {
				for i, p := range row.Performers {
					if i < len(item.People) && p.ID != "" {
						item.People[i].PhotoPath = performerImagePath(p.ID)
					}
				}
			}
			if art := s.runtime.sceneArtwork(ctx, id); art != nil {
				if art.ReleaseID > 0 {
					item.ProviderIds = providerIDsWithRelease(id, art.ReleaseID)
				}
				if art.PosterPath != "" {
					item.PosterPath = backendImagePath(art.PosterPath)
				}
				if len(art.BackdropPaths) > 0 {
					item.BackdropPath = backendImagePath(art.BackdropPaths[0])
				}
			}
			s.queuePeople(*row)
			return &pluginv1.GetMetadataResponse{Item: item}, nil
		}
	}
	if release != nil {
		return &pluginv1.GetMetadataResponse{Item: legacyMetadataItem(release)}, nil
	}
	return &pluginv1.GetMetadataResponse{}, nil
}
func metadataItem(s scene) *pluginv1.MetadataItem {
	genres := []string{}
	for _, t := range s.Tags {
		if strings.EqualFold(t.Name, "Watchlist") || strings.HasPrefix(strings.ToLower(t.Name), "collection: ") {
			continue
		}
		genres = append(genres, t.Name)
	}
	people := []*pluginv1.PersonRecord{}
	for i, p := range s.Performers {
		people = append(people, &pluginv1.PersonRecord{Name: p.Name, Kind: "actor", SortOrder: int32(i), PlexGuid: "stash:" + p.ID, PhotoPath: p.ImagePath})
	}
	if s.Director != "" {
		people = append(people, &pluginv1.PersonRecord{Name: s.Director, Kind: "director"})
	}
	studios := []string{}
	if s.Studio != nil && s.Studio.Name != "" {
		studios = append(studios, s.Studio.Name)
	}
	year := int32(0)
	if len(s.Date) >= 4 {
		if n, e := strconv.Atoi(s.Date[:4]); e == nil {
			year = int32(n)
		}
	}
	imagePath := ""
	if s.Paths.Screenshot != "" {
		imagePath = coverPath(s.ID)
	}
	return &pluginv1.MetadataItem{ProviderId: "stash:" + s.ID, ItemType: "movie", Title: displayTitle(s), OriginalTitle: s.Title, SortTitle: displayTitle(s), Year: year, Overview: s.Details, Runtime: runtimeMinutes(s), Genres: genres, ProviderIds: providerIDs(s.ID), ReleaseDate: s.Date, PosterPath: imagePath, BackdropPath: imagePath, People: people, Studios: studios}
}
func (s *metadataServer) GetSeasons(context.Context, *pluginv1.GetSeasonsRequest) (*pluginv1.GetSeasonsResponse, error) {
	return &pluginv1.GetSeasonsResponse{}, nil
}
func (s *metadataServer) GetEpisodes(context.Context, *pluginv1.GetEpisodesRequest) (*pluginv1.GetEpisodesResponse, error) {
	return &pluginv1.GetEpisodesResponse{}, nil
}
func (s *metadataServer) GetImages(ctx context.Context, req *pluginv1.GetImagesRequest) (*pluginv1.GetImagesResponse, error) {
	if !supported(req.GetItemType()) {
		return &pluginv1.GetImagesResponse{}, nil
	}
	id := sceneID(req.GetProviderId(), req.GetProviderIds())
	var release *legacyprovider.Metadata
	if id == "" {
		var err error
		release, err = s.fetchLegacy(ctx, legacyReleaseID(req.GetProviderId(), req.GetProviderIds()))
		if err != nil {
			return nil, err
		}
		if release != nil {
			id = release.StashSceneID
		}
	}
	c := s.runtime.stash()
	if id != "" && c.configured() {
		row, err := c.findScene(ctx, id)
		if err != nil {
			return nil, err
		}
		if row != nil {
			out := &pluginv1.GetImagesResponse{}
			art := s.runtime.sceneArtwork(ctx, id)
			if art != nil && art.PosterPath != "" {
				out.Images = append(out.Images, &pluginv1.ImageRecord{Kind: "poster", Url: backendImagePath(art.PosterPath)})
			}
			if row.Paths.Screenshot != "" && len(out.Images) == 0 {
				out.Images = append(out.Images, &pluginv1.ImageRecord{Kind: "poster", Url: coverPath(id)})
			}
			if art != nil {
				for _, path := range art.BackdropPaths {
					out.Images = append(out.Images, &pluginv1.ImageRecord{Kind: "backdrop", Url: backendImagePath(path)})
				}
			}
			if row.Paths.Screenshot != "" {
				out.Images = append(out.Images, &pluginv1.ImageRecord{Kind: "backdrop", Url: coverPath(id)})
			}
			return out, nil
		}
	}
	if release != nil {
		return &pluginv1.GetImagesResponse{Images: legacyImages(release)}, nil
	}
	return &pluginv1.GetImagesResponse{}, nil
}
func (s *metadataServer) ResolveImageURL(_ context.Context, req *pluginv1.ResolveImageURLRequest) (*pluginv1.ResolveImageURLResponse, error) {
	c := s.runtime.stash()
	if c == nil {
		return nil, fmt.Errorf("Stash connection not configured")
	}
	return &pluginv1.ResolveImageURLResponse{Url: s.resolveImageURL(c, req.GetPath())}, nil
}
func (s *metadataServer) ResolveImageURLs(_ context.Context, req *pluginv1.ResolveImageURLsRequest) (*pluginv1.ResolveImageURLsResponse, error) {
	c := s.runtime.stash()
	if c == nil {
		return nil, fmt.Errorf("Stash connection not configured")
	}
	out := map[string]string{}
	for _, path := range req.GetPaths() {
		out[path] = s.resolveImageURL(c, path)
	}
	return &pluginv1.ResolveImageURLsResponse{Urls: out}, nil
}
func (s *metadataServer) resolveImageURL(stash *stashClient, path string) string {
	if legacyPath, ok := strings.CutPrefix(path, "javbeacon://"); ok {
		if s.runtime.legacy == nil || strings.Contains(legacyPath, "..") || strings.ContainsAny(legacyPath, "?#\\") {
			return ""
		}
		return s.runtime.legacy.Provider().ImageURL("/" + legacyPath)
	}
	if backendPath, ok := strings.CutPrefix(path, "stash://backend"); ok {
		return s.runtime.artworkClient().imageURL(backendPath)
	}
	return stash.imageURL(strings.TrimPrefix(path, "stash://"))
}
func loadManifest() (*pluginv1.PluginManifest, error) {
	m, err := publicmanifest.Load(manifestJSON)
	if err != nil {
		return nil, err
	}
	if version != "" {
		m.Version = version
	}
	path, err := os.Executable()
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	m.Checksum = hex.EncodeToString(sum[:])
	return m, nil
}
func main() {
	m, err := loadManifest()
	if err != nil {
		panic(err)
	}
	logger := hclog.New(&hclog.LoggerOptions{Name: "stash-metadata", Level: hclog.Info})
	rs := &runtimeServer{manifest: m, legacy: legacytasks.New(logger)}
	ms := &metadataServer{runtime: rs}
	ws := &watchSyncServer{runtime: rs}
	rs.task = &scheduledTaskServer{runtime: rs, log: logger}
	runtime.Serve(runtime.ServeConfig{Logger: logger, Servers: runtime.CapabilityServers{Runtime: rs, MetadataProvider: ms, ImageResolver: ms, WatchSyncProvider: ws, ScheduledTask: rs.task}})
}

func runtimeMinutes(s scene) int32 {
	if len(s.Files) == 0 {
		return 0
	}
	return int32(s.Files[0].Duration / 60)
}
