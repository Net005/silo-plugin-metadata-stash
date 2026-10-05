package legacyprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"time"
)

const artworkRotation = 6 * time.Hour

type artworkMarker struct {
	Policy     int    `json:"policy"`
	Bucket     int64  `json:"bucket"`
	PosterID   string `json:"poster_media_id"`
	BackdropID string `json:"backdrop_media_id"`
}

// artworkChoice selects only images belonging to this collection's current
// local members. The choice remains stable for six hours so routine polls
// do not churn Silo artwork or its image cache.
func artworkChoice(spec CollectionSpec, now time.Time) (CollectionArtwork, CollectionArtwork) {
	members := make(map[string]bool, len(spec.MediaIDs))
	for _, id := range spec.MediaIDs {
		members[id] = true
	}
	posterCandidates := []CollectionArtwork{}
	backdropCandidates := []CollectionArtwork{}
	for _, art := range spec.Artwork {
		if !members[art.MediaID] || (strings.HasPrefix(art.MediaID, "movie-tmdb-") && art.StashSceneID == "") {
			continue
		}
		if art.PosterURL != "" {
			posterCandidates = append(posterCandidates, art)
		}
		if art.BackdropURL != "" {
			backdropCandidates = append(backdropCandidates, art)
		}
	}
	poster := weightedArtworkPick(posterCandidates, collectionSlug(spec), "poster", now)
	if poster.BackdropURL != "" {
		return poster, poster
	}
	return poster, weightedArtworkPick(backdropCandidates, collectionSlug(spec), "backdrop", now)
}

func weightedArtworkPick(candidates []CollectionArtwork, slug, kind string, now time.Time) CollectionArtwork {
	if len(candidates) == 0 {
		return CollectionArtwork{}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := artworkDate(candidates[i]), artworkDate(candidates[j])
		if a == b {
			return candidates[i].MediaID < candidates[j].MediaID
		}
		return a > b
	})
	bucket := now.Unix() / int64(artworkRotation.Seconds())
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%s:%s:%d", slug, kind, bucket)
	rng := rand.New(rand.NewSource(int64(h.Sum64())))
	pool := len(candidates)
	// Most rotations favor the newest fifth (at least three images), while
	// the rest can pick any member so older releases still appear.
	if rng.Intn(100) < 70 {
		pool = max(3, (pool+4)/5)
		if pool > len(candidates) {
			pool = len(candidates)
		}
	}
	return candidates[rng.Intn(pool)]
}

func artworkDate(art CollectionArtwork) string {
	if date := strings.TrimSpace(art.ReleaseDate); date != "" {
		return date
	}
	return strings.TrimSpace(art.AddedAt)
}

func (c *SiloClient) uploadCollectionArtwork(ctx context.Context, collectionID, kind, sourceURL string) error {
	parsed, err := url.Parse(sourceURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return fmt.Errorf("silo: invalid collection artwork URL")
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("source_url", sourceURL); err != nil {
		return err
	}
	if err := form.Close(); err != nil {
		return err
	}
	path := "/api/v2/admin/collections/" + url.PathEscape(collectionID) + "/" + kind
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	// Silo may be unable to retrieve a remote source URL even when the plugin
	// can retrieve it. Uploading the same image as a file avoids that server-side
	// fetch, while Silo still creates its normal resized artwork variants.
	if resp.StatusCode == http.StatusInternalServerError {
		if err := c.uploadCollectionArtworkFile(ctx, path, sourceURL); err == nil {
			return nil
		} else {
			return fmt.Errorf("silo: collection %s source URL upload returned HTTP %d; file fallback: %w", kind, resp.StatusCode, err)
		}
	}
	return fmt.Errorf("silo: collection %s upload returned HTTP %d", kind, resp.StatusCode)
}

func (c *SiloClient) uploadCollectionArtworkFile(ctx context.Context, path, sourceURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching artwork: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("artwork source returned HTTP %d", resp.StatusCode)
	}
	const maxImageBytes = 10 << 20
	image, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return fmt.Errorf("reading artwork: %w", err)
	}
	if len(image) > maxImageBytes {
		return fmt.Errorf("artwork exceeds Silo's 10 MB limit")
	}
	return c.uploadCollectionArtworkBytes(ctx, path, image)
}

func (c *SiloClient) uploadCollectionArtworkBytes(ctx context.Context, path string, image []byte) error {
	contentType := http.DetectContentType(image)
	ext := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}[contentType]
	if ext == "" {
		return fmt.Errorf("unsupported artwork content type %s", contentType)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename="artwork.%s"`, ext))
	header.Set("Content-Type", contentType)
	part, err := form.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(image); err != nil {
		return err
	}
	if err := form.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", form.FormDataContentType())
	result, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer result.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(result.Body, 1<<20))
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return fmt.Errorf("file upload returned HTTP %d", result.StatusCode)
	}
	return nil
}

func (c *SiloClient) syncCollectionArtwork(ctx context.Context, collection siloCollection, spec CollectionSpec, now time.Time) (bool, error) {
	poster, backdrop := artworkChoice(spec, now)
	if poster.PosterURL == "" && backdrop.BackdropURL == "" {
		return false, nil
	}
	bucket := now.Unix() / int64(artworkRotation.Seconds())
	path := "/api/v2/admin/collections/" + url.PathEscape(collection.ID)
	var current siloCollection
	etag, err := c.collectionRequestETag(ctx, http.MethodGet, path, nil, &current, "")
	if err != nil {
		return false, err
	}
	var previous artworkMarker
	_ = json.Unmarshal(current.SourceConfig["javbeacon_artwork"], &previous)
	members := map[string]bool{}
	for _, id := range spec.MediaIDs {
		members[id] = true
	}
	if previous.Policy == 3 && len(current.SourceConfig[uniquePosterKey]) > 0 && previous.Bucket == bucket && (previous.PosterID == "" || members[previous.PosterID]) && (previous.BackdropID == "" || members[previous.BackdropID]) {
		return false, c.SetUniqueCollectionPoster(ctx, collection.ID, append([]CollectionArtwork{poster}, spec.Artwork...))
	}
	if c.collectionArtworkResolver != nil {
		if poster.StashSceneID != "" {
			poster, err = c.collectionArtworkResolver(ctx, poster)
			if err != nil {
				return false, err
			}
		}
		if backdrop.StashSceneID != "" {
			if backdrop.StashSceneID == poster.StashSceneID {
				backdrop = poster
			} else {
				backdrop, err = c.collectionArtworkResolver(ctx, backdrop)
				if err != nil {
					return false, err
				}
			}
		}
	}
	if poster.PosterURL != "" {
		if err := c.SetUniqueCollectionPoster(ctx, collection.ID, append([]CollectionArtwork{poster}, spec.Artwork...)); err != nil {
			return false, err
		}
	}
	if backdrop.BackdropURL != "" {
		if err := c.uploadCollectionArtwork(ctx, collection.ID, "backdrop", backdrop.BackdropURL); err != nil {
			return false, err
		}
	}
	// Uploads change the collection ETag. Refresh it before saving our
	// rotation marker, preserving any other source_config values.
	etag, err = c.collectionRequestETag(ctx, http.MethodGet, path, nil, &current, "")
	if err != nil {
		return false, err
	}
	if etag == "" {
		return false, fmt.Errorf("silo: collection artwork ETag is missing")
	}
	config := current.SourceConfig
	if config == nil {
		config = map[string]json.RawMessage{}
	}
	marker, err := json.Marshal(artworkMarker{Policy: 3, Bucket: bucket, PosterID: poster.MediaID, BackdropID: backdrop.MediaID})
	if err != nil {
		return false, err
	}
	config["javbeacon_artwork"] = marker
	if _, err := c.collectionRequestETag(ctx, http.MethodPatch, path, map[string]any{"source_config": config}, nil, etag); err != nil {
		return false, err
	}
	return true, nil
}
