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
	manifest *pluginv1.PluginManifest
	mu       sync.RWMutex
	client   *stashClient
	siloBase string
	siloKey  string
	pollOnce sync.Once
	task     *scheduledTaskServer
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
		s.client = &stashClient{base: strings.TrimRight(text(v["base_url"]), "/"), key: key, watchlistTag: text(v["watchlist_tag_id"])}
		s.siloBase = strings.TrimRight(text(v["silo_base_url"]), "/")
		siloKey := text(v["silo_api_key"])
		if siloKey != "" {
			s.siloKey = siloKey
		}
		s.mu.Unlock()
		s.pollOnce.Do(func() { go s.pollMatching() })
	}
	return &pluginv1.ConfigureResponse{}, nil
}
func text(v any) string                      { s, _ := v.(string); return strings.TrimSpace(s) }
func (s *runtimeServer) stash() *stashClient { s.mu.RLock(); defer s.mu.RUnlock(); return s.client }

type metadataServer struct {
	pluginv1.UnimplementedMetadataProviderServer
	pluginv1.UnimplementedImageResolverServer
	runtime *runtimeServer
}

func supported(t string) bool { return t == "" || t == "movie" }
func sceneID(raw string, ids *structpb.Struct) string {
	if ids != nil {
		v := ids.AsMap()
		if id := text(v[capabilityID]); id != "" {
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
func displayTitle(s scene) string {
	if strings.TrimSpace(s.Title) != "" {
		return s.Title
	}
	return s.Code
}
func searchResult(s scene) *pluginv1.ProviderSearchResult {
	return &pluginv1.ProviderSearchResult{ProviderId: "stash:" + s.ID, ItemType: "movie", Title: displayTitle(s), Overview: s.Details, ProviderIds: providerIDs(s.ID), ImageUrl: coverPath(s.ID)}
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
	if c == nil {
		return nil, fmt.Errorf("Stash connection not configured")
	}
	if id := sceneID("", req.GetProviderIds()); id != "" {
		row, err := c.findScene(ctx, id)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return &pluginv1.SearchMetadataResponse{}, nil
		}
		return &pluginv1.SearchMetadataResponse{Results: []*pluginv1.ProviderSearchResult{searchResult(*row)}}, nil
	}
	rows, err := c.search(ctx, req.GetQuery())
	if err != nil {
		return nil, err
	}
	out := &pluginv1.SearchMetadataResponse{}
	for _, row := range rows {
		out.Results = append(out.Results, searchResult(row))
	}
	return out, nil
}
func (s *metadataServer) GetMetadata(ctx context.Context, req *pluginv1.GetMetadataRequest) (*pluginv1.GetMetadataResponse, error) {
	if !supported(req.GetItemType()) {
		return &pluginv1.GetMetadataResponse{}, nil
	}
	id := sceneID(req.GetProviderId(), req.GetProviderIds())
	if id == "" {
		return &pluginv1.GetMetadataResponse{}, nil
	}
	c := s.runtime.stash()
	if c == nil {
		return nil, fmt.Errorf("Stash connection not configured")
	}
	row, err := c.findScene(ctx, id)
	if err != nil || row == nil {
		return &pluginv1.GetMetadataResponse{}, err
	}
	return &pluginv1.GetMetadataResponse{Item: metadataItem(*row)}, nil
}
func metadataItem(s scene) *pluginv1.MetadataItem {
	genres := []string{}
	for _, t := range s.Tags {
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
	return &pluginv1.MetadataItem{ProviderId: "stash:" + s.ID, ItemType: "movie", Title: displayTitle(s), OriginalTitle: s.Title, SortTitle: displayTitle(s), Year: year, Overview: s.Details, Runtime: runtimeMinutes(s), Genres: genres, ProviderIds: providerIDs(s.ID), ReleaseDate: s.Date, PosterPath: coverPath(s.ID), BackdropPath: coverPath(s.ID), People: people, Studios: studios}
}
func (s *metadataServer) GetPersonDetail(context.Context, *pluginv1.GetPersonDetailRequest) (*pluginv1.GetPersonDetailResponse, error) {
	return &pluginv1.GetPersonDetailResponse{}, nil
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
	if id == "" {
		return &pluginv1.GetImagesResponse{}, nil
	}
	c := s.runtime.stash()
	if c == nil {
		return nil, fmt.Errorf("Stash connection not configured")
	}
	row, err := c.findScene(ctx, id)
	if err != nil || row == nil {
		return &pluginv1.GetImagesResponse{}, err
	}
	if row.Paths.Screenshot == "" {
		return &pluginv1.GetImagesResponse{}, nil
	}
	path := coverPath(id)
	return &pluginv1.GetImagesResponse{Images: []*pluginv1.ImageRecord{{Kind: "poster", Url: path}, {Kind: "backdrop", Url: path}}}, nil
}
func (s *metadataServer) ResolveImageURL(_ context.Context, req *pluginv1.ResolveImageURLRequest) (*pluginv1.ResolveImageURLResponse, error) {
	c := s.runtime.stash()
	if c == nil {
		return nil, fmt.Errorf("Stash connection not configured")
	}
	return &pluginv1.ResolveImageURLResponse{Url: c.imageURL(strings.TrimPrefix(req.GetPath(), "stash://"))}, nil
}
func (s *metadataServer) ResolveImageURLs(_ context.Context, req *pluginv1.ResolveImageURLsRequest) (*pluginv1.ResolveImageURLsResponse, error) {
	c := s.runtime.stash()
	if c == nil {
		return nil, fmt.Errorf("Stash connection not configured")
	}
	out := map[string]string{}
	for _, path := range req.GetPaths() {
		out[path] = c.imageURL(strings.TrimPrefix(path, "stash://"))
	}
	return &pluginv1.ResolveImageURLsResponse{Urls: out}, nil
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
	rs := &runtimeServer{manifest: m}
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
