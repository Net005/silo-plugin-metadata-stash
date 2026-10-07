package legacyprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Collection masters must never be copied from the default display thumbnail.
func TestArtworkReadsRequestOriginalStorageVariant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("image_size") != "original" {
			t.Errorf("thumbnail requested: %s", r.URL.Path)
		}
		if r.URL.Path == "/api/v2/profiles" {
			w.Write([]byte(`{"items":[{"id":"profile"}]}`))
			return
		}
		if r.URL.Path == "/api/v2/catalog/items/item" {
			w.Write([]byte(`{"content_id":"item","poster_url":"https://images.example/original.png"}`))
			return
		}
		w.Write([]byte(`{"items":[],"page":{"has_more":false}}`))
	}))
	defer server.Close()
	c := NewSiloClient(server.URL, "key")
	if _, _, err := c.ItemArtwork(context.Background(), "profile", "item"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListRecommendationCatalog(context.Background(), "library", "profile"); err != nil {
		t.Fatal(err)
	}
}
