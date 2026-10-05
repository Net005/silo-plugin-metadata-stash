package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPosterApplyUsesTemporaryPrivateURL(t *testing.T) {
	want := []byte("jpeg-fixture")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/admin/items/item/images/apply" {
			t.Error(r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["provider_id"] != "stash" || body["type"] != "poster" {
			t.Error(body)
		}
		res, e := http.Get(body["original_url"])
		if e != nil {
			t.Error(e)
			return
		}
		got, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if string(got) != string(want) {
			t.Error("wrong poster bytes")
		}
		json.NewEncoder(w).Encode(map[string]string{"content_id": "item", "stored_path": "storage://poster"})
	}))
	defer server.Close()
	stored, e := applyRenderedPoster(context.Background(), server.URL, "secret", "item", want)
	if e != nil || stored != "storage://poster" {
		t.Fatal(stored, e)
	}
}
