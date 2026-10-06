package legacyprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Serializes reservations across the independently constructed clients used by
// scheduled recommendations and realtime saved-filter sync in this process.
var collectionPosterMu sync.Mutex

var posterSignatureCache = map[string][]byte{}

const uniquePosterKey = "stash_unique_poster"

type uniquePosterMarker struct {
	Policy          int    `json:"policy"`
	MediaID         string `json:"media_id"`
	SourceDigest    string `json:"source_digest"`
	PosterURL       string `json:"poster_url"`
	PosterThumbhash string `json:"poster_thumbhash,omitempty"`
	Signature       []byte `json:"signature"`
}

// Admin detail responses omit signed URLs; thumbhash is stable across URL expiry.
func posterMarkerMatches(m uniquePosterMarker, r siloCollection) bool {
	if m.Policy != 1 {
		return false
	}
	if m.PosterThumbhash != "" && r.PosterThumbhash != "" {
		return m.PosterThumbhash == r.PosterThumbhash
	}
	if m.PosterURL != "" && m.PosterURL == r.PosterURL {
		return true
	}
	// Older plugin versions wrote empty or expiring signed URLs because Silo admin detail omits
	// them. Retain these known managed reservations until the cover is updated
	// with a stable thumbnail hash; never apply this migration to user shelves.
	return m.PosterThumbhash == "" && m.SourceDigest != "" && len(m.Signature) > 0 && strings.HasPrefix(r.Slug, "stash-recommendations-") && strings.HasPrefix(r.Description, RecommendationOwner)
}

// pixelDigest ignores file metadata/encoding and hashes decoded image pixels.
func pixelDigest(data []byte) (string, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	if cfg.Width > 40000000/max(1, cfg.Height) {
		return "", fmt.Errorf("oversized image")
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	b := im.Bounds()
	if b.Dx()*b.Dy() > 40_000_000 {
		return "", fmt.Errorf("oversized collection image")
	}
	h := sha256.New()
	fmt.Fprintf(h, "%dx%d:", b.Dx(), b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p := color.NRGBAModel.Convert(im.At(x, y)).(color.NRGBA)
			h.Write([]byte{p.R, p.G, p.B, p.A})
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// A normalized grid also detects the same photo after Silo resizing or lossy encoding.
func posterSignature(data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width > 40000000/max(1, cfg.Height) {
		return nil, fmt.Errorf("oversized image")
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := im.Bounds()
	if b.Dx()*b.Dy() > 40_000_000 {
		return nil, fmt.Errorf("oversized image")
	}
	out := []byte{}
	for gy := 0; gy < 18; gy++ {
		for gx := 0; gx < 12; gx++ {
			var r, g, bl uint64
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					x := b.Min.X + (gx*4+sx)*b.Dx()/48
					y := b.Min.Y + (gy*4+sy)*b.Dy()/72
					p := color.NRGBAModel.Convert(im.At(x, y)).(color.NRGBA)
					r += uint64(p.R)
					g += uint64(p.G)
					bl += uint64(p.B)
				}
			}
			out = append(out, byte(r/16), byte(g/16), byte(bl/16))
		}
	}
	return out, nil
}
func samePoster(a, b []byte) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	sum := 0
	large := 0
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		sum += d
		if d > 25 {
			large++
		}
	}
	return sum <= 5*len(a) && large <= len(a)/20
}

// SameArtworkImage compares decoded picture content across cache resizing and
// lossy encoding. It is used only to retain an already selected artwork source.
func SameArtworkImage(a, b []byte) bool {
	normalized := func(raw []byte) ([]byte, error) {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 40000000/max(1, cfg.Height) {
			return nil, fmt.Errorf("invalid image")
		}
		im, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		small := image.NewRGBA(image.Rect(0, 0, 32, 48))
		// Area-aware downsampling avoids text-edge aliasing between differently
		// sized JPEG originals and Silo's smaller WebP cache.
		xdraw.CatmullRom.Scale(small, small.Bounds(), im, im.Bounds(), draw.Src, nil)
		out := make([]byte, 0, 32*48*3)
		for y := 0; y < 48; y++ {
			for x := 0; x < 32; x++ {
				p := small.RGBAAt(x, y)
				out = append(out, p.R, p.G, p.B)
			}
		}
		return out, nil
	}
	x, err := normalized(a)
	if err != nil {
		return false
	}
	y, err := normalized(b)
	if err != nil || len(x) != len(y) {
		return false
	}
	sum, large := 0, 0
	for i := range x {
		delta := int(x[i]) - int(y[i])
		if delta < 0 {
			delta = -delta
		}
		sum += delta
		if delta > 25 {
			large++
		}
	}
	// Go's JPEG/WebP colour conversion may introduce a small uniform drift.
	// Require whole-picture similarity AND very few large local differences.
	return sum <= 10*len(x) && large <= len(x)/20
}
func (c *SiloClient) posterBytes(ctx context.Context, u string) ([]byte, error) {
	p, e := url.Parse(u)
	if e != nil || p.Host == "" || (p.Scheme != "http" && p.Scheme != "https") {
		return nil, fmt.Errorf("invalid poster URL")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return nil, e
	}
	resp, e := c.httpClient.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("poster fetch HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, (10<<20)+1))
	if len(b) > 10<<20 {
		return nil, fmt.Errorf("poster exceeds 10 MB")
	}
	return b, e
}

// Unique posters are reserved across ALL Silo collections, including user
// collections (which are inspected but never edited). Membership is unchanged.
// If no unclaimed member image exists, preserve existing artwork and report the conflict.
func (c *SiloClient) SetUniqueCollectionPoster(ctx context.Context, id string, candidates []CollectionArtwork) error {
	collectionPosterMu.Lock()
	defer collectionPosterMu.Unlock()
	return c.setUniqueCollectionPoster(ctx, id, candidates, true)
}

func (c *SiloClient) setUniqueCollectionPoster(ctx context.Context, id string, candidates []CollectionArtwork, rebalance bool) error {
	rows, e := c.collections(ctx)
	if e != nil {
		return e
	}
	var current siloCollection
	signatures := [][]byte{}
	usedIDs, usedDigests, usedURLs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, r := range rows {
		if r.ID == id {
			current = r
			continue
		}
		var m uniquePosterMarker
		_ = json.Unmarshal(r.SourceConfig[uniquePosterKey], &m)
		if posterMarkerMatches(m, r) {
			usedIDs[m.MediaID] = true
			usedDigests[m.SourceDigest] = true
			if len(m.Signature) > 0 {
				signatures = append(signatures, m.Signature)
			}
		}
		var old string
		_ = json.Unmarshal(r.SourceConfig["stash_recommendation_artwork"], &old)
		if old != "" && m.Policy != 1 {
			usedIDs[old] = true
		}
		var legacy artworkMarker
		_ = json.Unmarshal(r.SourceConfig["javbeacon_artwork"], &legacy)
		if legacy.PosterID != "" && m.Policy != 1 {
			usedIDs[legacy.PosterID] = true
		}
		if r.PosterURL != "" {
			usedURLs[r.PosterURL] = true
		}
	}
	for _, r := range rows {
		if r.ID == id || r.PosterURL == "" {
			continue
		}
		var m uniquePosterMarker
		_ = json.Unmarshal(r.SourceConfig[uniquePosterKey], &m)
		if posterMarkerMatches(m, r) && len(m.Signature) > 0 {
			continue
		}
		sig, ok := posterSignatureCache[r.PosterURL]
		if !ok {
			b, err := c.posterBytes(ctx, r.PosterURL)
			if err != nil {
				return fmt.Errorf("cannot verify existing collection poster: %w", err)
			}
			sig, err = posterSignature(b)
			if err != nil {
				return err
			}
			posterSignatureCache[r.PosterURL] = sig
		}
		signatures = append(signatures, sig)
	}
	if current.ID == "" {
		return fmt.Errorf("collection missing during artwork reservation")
	}
	var previous uniquePosterMarker
	_ = json.Unmarshal(current.SourceConfig[uniquePosterKey], &previous)
	// Keeping a still-valid reservation avoids re-uploading artwork on every poll.
	for _, a := range candidates {
		if posterMarkerMatches(previous, current) && previous.MediaID == a.MediaID && !usedIDs[a.MediaID] && !usedDigests[previous.SourceDigest] && func() bool {
			for _, v := range signatures {
				if samePoster(previous.Signature, v) {
					return false
				}
			}
			return len(previous.Signature) > 0
		}() {
			return nil
		}
	}
	var selected CollectionArtwork
	var data []byte
	var digest string
	// Current cover first, then remaining candidates in their supplied rank order.
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].MediaID == previous.MediaID && candidates[j].MediaID != previous.MediaID
	})
	var failures int
	for _, a := range candidates {
		if a.PosterURL == "" || usedIDs[a.MediaID] || usedURLs[a.PosterURL] {
			continue
		}
		b, err := c.posterBytes(ctx, a.PosterURL)
		if err != nil {
			failures++
			continue
		}
		d, err := pixelDigest(b)
		if err != nil {
			failures++
			continue
		}
		if usedDigests[d] {
			continue
		}
		sig, err := posterSignature(b)
		if err != nil {
			failures++
			continue
		}
		duplicate := false
		for _, other := range signatures {
			if samePoster(sig, other) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		selected = a
		data = b
		digest = d
		break
	}
	if data == nil && failures == 0 && rebalance && current.ItemCount > 0 {
		// Small shelves have fewer artwork choices. Move a larger managed
		// shelf to another verified member before using a generated fallback.
		blocked := map[string]bool{}
		for _, a := range candidates {
			blocked[a.MediaID] = true
		}
		for _, a := range candidates {
			for _, owner := range rows {
				if owner.ID == id || owner.ItemCount <= current.ItemCount || !strings.HasPrefix(owner.Slug, "stash-recommendations-") || !strings.HasPrefix(owner.Description, RecommendationOwner) {
					continue
				}
				var mark uniquePosterMarker
				_ = json.Unmarshal(owner.SourceConfig[uniquePosterKey], &mark)
				if mark.MediaID != a.MediaID || !posterMarkerMatches(mark, owner) {
					continue
				}
				members, err := c.collectionMembers(ctx, owner.ID)
				if err != nil {
					return err
				}
				ids := []string{}
				for mid := range members {
					if !blocked[mid] && !usedIDs[mid] && !strings.HasPrefix(mid, "movie-tmdb-") {
						ids = append(ids, mid)
					}
				}
				sort.Slice(ids, func(i, j int) bool { return members[ids[i]] < members[ids[j]] })
				alternatives := []CollectionArtwork{}
				profileID := ""
				for _, mid := range ids {
					var images struct {
						Current struct {
							PosterURL string `json:"poster_url"`
						} `json:"current"`
					}
					if err = c.collectionRequest(ctx, http.MethodGet, "/api/v2/admin/items/"+url.PathEscape(mid)+"/images", nil, &images); err != nil {
						return err
					}
					// Admin artwork can be an S3 storage key, not a fetchable URL.
					if images.Current.PosterURL != "" && !strings.HasPrefix(images.Current.PosterURL, "https://") && !strings.HasPrefix(images.Current.PosterURL, "http://") {
						if profileID == "" {
							profileID, err = c.PrimaryProfileID(ctx)
							if err != nil {
								return err
							}
						}
						images.Current.PosterURL, _, err = c.ItemArtwork(ctx, profileID, mid)
						if err != nil {
							return err
						}
					}
					if images.Current.PosterURL != "" {
						alternatives = append(alternatives, CollectionArtwork{MediaID: mid, PosterURL: images.Current.PosterURL})
					}
					if len(alternatives) >= 500 {
						break
					}
				}
				if len(alternatives) == 0 {
					continue
				}
				if err = c.setUniqueCollectionPoster(ctx, owner.ID, alternatives, false); err != nil {
					continue
				}
				return c.setUniqueCollectionPoster(ctx, id, candidates, false)
			}
		}
	}
	if data == nil {
		if failures > 0 {
			return fmt.Errorf("collection poster sources unavailable (%d)", failures)
		}
		// Preserve the saved artwork when no distinct real member cover is available.
		// Never synthesize title cards or collages as collection posters.
		return fmt.Errorf("no unclaimed member cover available")
	}
	path := "/api/v2/admin/collections/" + url.PathEscape(id)
	if e = c.uploadCollectionArtworkBytes(ctx, path+"/poster", data); e != nil {
		return e
	}
	var latest siloCollection
	tag, e := c.collectionRequestETag(ctx, http.MethodGet, path, nil, &latest, "")
	if e != nil {
		return e
	}
	if latest.SourceConfig == nil {
		latest.SourceConfig = map[string]json.RawMessage{}
	}
	latest.SourceConfig[uniquePosterKey], _ = json.Marshal(uniquePosterMarker{Policy: 1, MediaID: selected.MediaID, SourceDigest: digest, PosterURL: latest.PosterURL, PosterThumbhash: latest.PosterThumbhash, Signature: func() []byte { v, _ := posterSignature(data); return v }()})
	_, e = c.collectionRequestETag(ctx, http.MethodPatch, path, map[string]any{"source_config": latest.SourceConfig}, nil, tag)
	return e
}

// RepairCollectionPosters changes only artwork on owned collections. It uses
// current membership and cached catalog posters; no AI or history writes occur.
func (c *SiloClient) RepairCollectionPosters(ctx context.Context, profile string) (int, error) {
	rows, err := c.collections(ctx)
	if err != nil {
		return 0, err
	}
	catalogs := map[string]map[string]CatalogItem{}
	count := 0
	for _, r := range rows {
		owned := (strings.HasPrefix(r.Slug, "stash-recommendations-") && strings.HasPrefix(r.Description, RecommendationOwner)) || (strings.HasPrefix(r.Slug, "javbeacon-") && strings.HasPrefix(r.Description, collectionOwner))
		if !owned {
			continue
		}
		if catalogs[r.LibraryID] == nil {
			items, e := c.ListRecommendationCatalog(ctx, r.LibraryID, profile)
			if e != nil {
				return count, e
			}
			catalogs[r.LibraryID] = map[string]CatalogItem{}
			for _, item := range items {
				catalogs[r.LibraryID][item.ContentID] = item
			}
		}
		members, e := c.collectionMembers(ctx, r.ID)
		if e != nil {
			return count, e
		}
		ids := []string{}
		for id := range members {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return members[ids[i]] < members[ids[j]] })
		candidates := []CollectionArtwork{}
		for _, id := range ids {
			a, ok := catalogs[r.LibraryID][id]
			if !ok || a.PosterURL == "" {
				continue
			}
			if strings.HasPrefix(id, "movie-tmdb-") {
				continue
			}
			candidates = append(candidates, CollectionArtwork{MediaID: id, PosterURL: a.PosterURL, BackdropURL: a.BackdropURL})
		}
		if len(candidates) == 0 {
			continue
		}
		if e = c.SetUniqueCollectionPoster(ctx, r.ID, candidates); e != nil {
			return count, fmt.Errorf("repair collection %s: %w", r.ID, e)
		}
		count++
	}
	return count, nil
}
