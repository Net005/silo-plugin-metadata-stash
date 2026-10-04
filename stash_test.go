package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExactSearchDoesNotReturnFuzzyScene(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("ApiKey") != "key" {
			t.Error("missing API key")
		}
		_, _ = w.Write([]byte(`{"data":{"findScenes":{"scenes":[{"id":"42","code":"ATID-705","title":"Original"},{"id":"43","code":"ATID-7050","title":"Near match"}]}}}`))
	}))
	defer server.Close()
	c := &stashClient{base: server.URL, key: "key"}
	got, err := c.search(context.Background(), "ATID-705")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "42" {
		t.Fatalf("unexpected matches: %#v", got)
	}
}
func TestAddPlayOnceChecksTimestampBeforeMutation(t *testing.T) {
	at := time.Date(2026, 10, 4, 7, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	history := []string{}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(body.Query, "sceneAddPlay") {
			writes++
			history = append(history, at.Format(time.RFC3339Nano))
			_, _ = w.Write([]byte(`{"data":{"sceneAddPlay":{"count":1}}}`))
			return
		}
		response, _ := json.Marshal(map[string]any{"data": map[string]any{"findScene": map[string]any{"id": "42", "play_history": history}}})
		_, _ = w.Write(response)
	}))
	defer server.Close()
	c := &stashClient{base: server.URL, key: "key"}
	first, err := c.addPlayOnce(context.Background(), "42", at)
	if err != nil || !first {
		t.Fatalf("first play: %v %v", first, err)
	}
	second, err := c.addPlayOnce(context.Background(), "42", at)
	if err != nil || second {
		t.Fatalf("replayed play: %v %v", second, err)
	}
	if writes != 1 {
		t.Fatalf("mutations=%d", writes)
	}
}
func TestSceneIDPreservesOldStashIdentity(t *testing.T) {
	if got := sceneID("stash:42936", nil); got != "42936" {
		t.Fatal(got)
	}
	if got := compact("ATID-705"); got != "atid705" {
		t.Fatal(got)
	}
}

func TestSearchTermsIncludePureTabooTitleWithoutQualitySuffix(t *testing.T) {
	terms := searchTerms("Pure Taboo - 2026-07-28 - Sample Scene [WEBDL-2160p].mp4")
	found := false
	for _, term := range terms {
		if term == "Sample Scene" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing title fallback: %#v", terms)
	}
}
