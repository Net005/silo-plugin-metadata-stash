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
	"image/png"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
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
	return m.PosterURL != "" && m.PosterURL == r.PosterURL
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
	x, err := posterSignature(a)
	if err != nil {
		return false
	}
	y, err := posterSignature(b)
	return err == nil && samePoster(x, y)
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
// If no unclaimed member image exists, a collection-specific composition of member artwork is used.
func (c *SiloClient) SetUniqueCollectionPoster(ctx context.Context, id string, candidates []CollectionArtwork) error {
	collectionPosterMu.Lock()
	defer collectionPosterMu.Unlock()
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
	if data == nil {
		// Source outages preserve existing artwork; exhaustion of shared artwork
		// produces a distinct, honest card, never an unrelated movie poster.
		if failures > 0 {
			return fmt.Errorf("collection poster sources unavailable (%d)", failures)
		}
		var sources [][]byte
		for _, a := range candidates {
			if a.PosterURL == "" {
				continue
			}
			b, err := c.posterBytes(ctx, a.PosterURL)
			if err != nil {
				continue
			}
			if _, err = pixelDigest(b); err != nil {
				continue
			}
			sources = append(sources, b)
			if len(sources) == 2 {
				break
			}
		}
		if len(sources) == 0 && len(candidates) > 0 {
			return fmt.Errorf("collection member artwork unavailable")
		}
		for attempt := 0; attempt < 32; attempt++ {
			data = collectionMemberCard(fmt.Sprintf("%s:%d", current.ID, attempt), current.Title, sources)
			sig, _ := posterSignature(data)
			duplicate := false
			for _, other := range signatures {
				if samePoster(sig, other) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				break
			}
			data = nil
		}
		if data == nil {
			return fmt.Errorf("cannot reserve distinct collection artwork")
		}
		digest, _ = pixelDigest(data)
		if posterMarkerMatches(previous, current) && previous.MediaID == "" && previous.SourceDigest == digest {
			return nil
		}
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

// Member artwork stays fully visible: fit rather than crop, with a title band.
// A stable collection-specific accent distinguishes otherwise identical shelves.
func collectionTitleCard(id, title string) []byte {
	return collectionMemberCard(id, title, nil)
}
func collectionMemberCard(id, title string, sources [][]byte) []byte {
	im := image.NewRGBA(image.Rect(0, 0, 500, 750))
	h := sha256.Sum256([]byte(id))
	bg := color.RGBA{18, 22, 30, 255}
	accent := color.RGBA{70 + h[0]/2, 70 + h[1]/2, 70 + h[2]/2, 255}
	draw.Draw(im, im.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	n := min(2, len(sources))
	for i := 0; i < n; i++ {
		src, _, err := image.Decode(bytes.NewReader(sources[i]))
		if err != nil {
			continue
		}
		panel := image.Rect(20+i*460/n, 20, 20+(i+1)*460/n-8, 550)
		bounds := src.Bounds()
		w, height := panel.Dx(), panel.Dy()
		if w*bounds.Dy() > height*bounds.Dx() {
			w = height * bounds.Dx() / bounds.Dy()
		} else {
			height = w * bounds.Dy() / bounds.Dx()
		}
		x, y := panel.Min.X+(panel.Dx()-w)/2, panel.Min.Y+(panel.Dy()-height)/2
		xdraw.CatmullRom.Scale(im, image.Rect(x, y, x+w, y+height), src, bounds, draw.Src, nil)
	}
	draw.Draw(im, image.Rect(0, 565, 500, 750), &image.Uniform{color.RGBA{accent.R / 2, accent.G / 2, accent.B / 2, 255}}, image.Point{}, draw.Src)
	draw.Draw(im, image.Rect(24, 575, 476, 581), &image.Uniform{accent}, image.Point{}, draw.Src)
	parsed, _ := opentype.Parse(goregular.TTF)
	face, _ := opentype.NewFace(parsed, &opentype.FaceOptions{Size: 36, DPI: 72, Hinting: font.HintingFull})
	defer face.Close()
	d := font.Drawer{Dst: im, Src: image.White, Face: face}
	// Provider prefix is already visible beneath the card in Silo.
	if prefix, rest, ok := strings.Cut(title, " | "); ok && len(prefix) < 20 {
		title = rest
	}
	lines := []string{}
	line := ""
	for _, word := range strings.Fields(title) {
		test := strings.TrimSpace(line + " " + word)
		if line != "" && d.MeasureString(test).Ceil() > 444 {
			lines = append(lines, line)
			line = word
		} else {
			line = test
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	for i, line := range lines {
		if i >= 3 {
			break
		}
		// Bound unusually long words without writing outside the card.
		for d.MeasureString(line).Ceil() > 444 && len([]rune(line)) > 1 {
			r := []rune(line)
			line = string(r[:len(r)-2]) + "…"
		}
		d.Dot = fixed.P(28, 634+i*44)
		d.DrawString(line)
	}
	var out bytes.Buffer
	_ = png.Encode(&out, im)
	return out.Bytes()
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
