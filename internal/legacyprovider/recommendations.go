package legacyprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const RecommendationOwner = "Managed by Stash Metadata recommendations."

type RecommendationRecord struct {
	ID           string                     `json:"id"`
	Slug         string                     `json:"slug"`
	Title        string                     `json:"title"`
	LibraryID    string                     `json:"library_id"`
	Description  string                     `json:"description"`
	SourceConfig map[string]json.RawMessage `json:"source_config"`
}

func RecommendationSlug(owner, library, kind string) string {
	h := sha256.Sum256([]byte(owner))
	return "stash-recommendations-" + hex.EncodeToString(h[:6]) + "-" + library + "-" + kind
}
func (c *SiloClient) RecommendationRecords(ctx context.Context) ([]RecommendationRecord, error) {
	rows, err := c.collections(ctx)
	if err != nil {
		return nil, err
	}
	out := []RecommendationRecord{}
	for _, x := range rows {
		if strings.HasPrefix(x.Slug, "stash-recommendations-") && strings.HasPrefix(x.Description, RecommendationOwner) {
			out = append(out, RecommendationRecord{ID: x.ID, Slug: x.Slug, Title: x.Title, LibraryID: x.LibraryID, Description: x.Description, SourceConfig: x.SourceConfig})
		}
	}
	return out, nil
}
func (c *SiloClient) ReadRecommendationRecord(ctx context.Context, id string) (RecommendationRecord, string, error) {
	var r RecommendationRecord
	tag, err := c.collectionRequestETag(ctx, http.MethodGet, "/api/v2/admin/collections/"+url.PathEscape(id), nil, &r, "")
	if err == nil && (!strings.HasPrefix(r.Description, RecommendationOwner) || !strings.HasPrefix(r.Slug, "stash-recommendations-")) {
		err = fmt.Errorf("recommendation record ownership mismatch")
	}
	return r, tag, err
}
func (c *SiloClient) CreateRecommendationRecord(ctx context.Context, slug, library, title string, hidden bool) (RecommendationRecord, error) {
	visibility := "visible"
	if hidden {
		visibility = "hidden"
	}
	var r RecommendationRecord
	err := c.collectionRequest(ctx, http.MethodPost, "/api/v2/admin/collections", map[string]any{"title": title, "slug": slug, "library_id": library, "collection_type": "manual", "visibility": visibility, "featured": !hidden, "description": RecommendationOwner, "source_config": map[string]any{}}, &r)
	return r, err
}
func (c *SiloClient) UpdateRecommendationRecord(ctx context.Context, id, etag string, patch map[string]any) error {
	if etag == "" {
		return fmt.Errorf("recommendation ETag missing")
	}
	_, err := c.collectionRequestETag(ctx, http.MethodPatch, "/api/v2/admin/collections/"+url.PathEscape(id), patch, nil, etag)
	return err
}

// ReconcileRecommendation changes membership only in the exact owned collection.
// Additions happen before removals. The final order uses Silo's captured validator.
func (c *SiloClient) ReconcileRecommendation(ctx context.Context, r RecommendationRecord, ids []string) error {
	if _, _, err := c.ReadRecommendationRecord(ctx, r.ID); err != nil {
		return err
	}
	members, err := c.collectionMembers(ctx, r.ID)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	ordered := []string{}
	for _, id := range ids {
		if id == "" || want[id] {
			continue
		}
		want[id] = true
		ordered = append(ordered, id)
		if _, ok := members[id]; !ok {
			if err = c.collectionRequest(ctx, http.MethodPut, "/api/v2/admin/collections/"+url.PathEscape(r.ID)+"/items/"+url.PathEscape(id), map[string]int{"position": len(ordered) - 1}, nil); err != nil {
				return err
			}
		}
	}
	for id := range members {
		if !want[id] {
			if err = c.collectionRequest(ctx, http.MethodDelete, "/api/v2/admin/collections/"+url.PathEscape(r.ID)+"/items/"+url.PathEscape(id), nil, nil); err != nil {
				return err
			}
		}
	}
	if len(ordered) > 0 {
		path := "/api/v2/admin/collections/" + url.PathEscape(r.ID) + "/items/order"
		var doc json.RawMessage
		tag, e := c.collectionRequestETag(ctx, http.MethodGet, path, nil, &doc, "")
		if e != nil {
			return e
		}
		if tag == "" {
			return fmt.Errorf("collection order ETag missing")
		}
		_, err = c.collectionRequestETag(ctx, http.MethodPut, path, map[string]any{"ordered_ids": ordered}, nil, tag)
	}
	return err
}

// SetRecommendationArtwork reuses only a verified member's cached Silo images.
// No title-based lookup or unrelated external movie artwork is involved.
func (c *SiloClient) SetRecommendationArtwork(ctx context.Context, r RecommendationRecord, art CollectionArtwork) error {
	var current siloCollection
	path := "/api/v2/admin/collections/" + url.PathEscape(r.ID)
	if err := c.collectionRequest(ctx, http.MethodGet, path, nil, &current); err != nil {
		return err
	}
	var old string
	_ = json.Unmarshal(current.SourceConfig["stash_recommendation_artwork"], &old)
	if old == art.MediaID {
		return nil
	}
	if art.PosterURL != "" {
		if err := c.uploadCollectionArtwork(ctx, r.ID, "poster", art.PosterURL); err != nil {
			return err
		}
	}
	if art.BackdropURL != "" {
		if err := c.uploadCollectionArtwork(ctx, r.ID, "backdrop", art.BackdropURL); err != nil {
			return err
		}
	}
	tag, err := c.collectionRequestETag(ctx, http.MethodGet, path, nil, &current, "")
	if err != nil {
		return err
	}
	if current.SourceConfig == nil {
		current.SourceConfig = map[string]json.RawMessage{}
	}
	current.SourceConfig["stash_recommendation_artwork"], _ = json.Marshal(art.MediaID)
	return c.UpdateRecommendationRecord(ctx, r.ID, tag, map[string]any{"source_config": current.SourceConfig})
}

// ListRecommendationCatalog honours the configured owner instead of choosing
// whichever profile happens to be first in the server response.
func (c *SiloClient) ListRecommendationCatalog(ctx context.Context, library, profile string) ([]CatalogItem, error) {
	if profile == "" {
		return nil, fmt.Errorf("recommendation owner profile is required")
	}
	out := []CatalogItem{}
	cursor := ""
	for page := 0; page < 500; page++ {
		path := "/api/v2/catalog?library_id=" + url.QueryEscape(library) + "&limit=200&skip_total=true"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("X-Profile-Id", profile)
		req.Header.Set("Accept", "application/json")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		var d struct {
			Items []CatalogItem `json:"items"`
			Page  struct {
				HasMore bool   `json:"has_more"`
				Next    string `json:"next_cursor"`
			} `json:"page"`
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, fmt.Errorf("recommendation catalog HTTP %d", resp.StatusCode)
		}
		err = json.NewDecoder(resp.Body).Decode(&d)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, d.Items...)
		if !d.Page.HasMore {
			return out, nil
		}
		if d.Page.Next == "" || d.Page.Next == cursor {
			return nil, fmt.Errorf("recommendation catalog pagination stalled")
		}
		cursor = d.Page.Next
	}
	return nil, fmt.Errorf("recommendation catalog page limit reached")
}

// RecommendationClient is intentionally restricted to the collection API.
// Its durable state is stored in an admin-only hidden collection source_config,
// surviving process restarts and binary upgrades without a writable plugin directory.
