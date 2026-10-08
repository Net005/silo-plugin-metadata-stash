package main

import (
	"context"
	"encoding/json"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

func TestAddPlayOnceRefusesIncompleteHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.Contains(body.Query, "mutation") {
			t.Error("incomplete history must not be mutated")
		}
		_, _ = w.Write([]byte(`{"data":{"findScene":{"id":"42","play_count":5,"play_history":[]}}}`))
	}))
	defer server.Close()
	c := &stashClient{base: server.URL, key: "key"}
	added, err := c.addPlayOnce(context.Background(), "42", time.Now())
	if added || err == nil {
		t.Fatalf("added=%t err=%v", added, err)
	}
}

func TestExcludedSceneIDs(t *testing.T) {
	ids := excludedSceneIDs("42, stash:43\n44")
	if len(ids) != 3 || !ids["42"] || !ids["43"] || !ids["44"] {
		t.Fatal(ids)
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

func TestConcurrentSceneMetadataLookupsAreCoalesced(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		_, _ = w.Write([]byte(`{"data":{"findScene":{"id":"42","title":"Scene"}}}`))
	}))
	defer server.Close()
	c := &stashClient{base: server.URL, key: "key"}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, err := c.findScene(context.Background(), "42")
			if err != nil || row == nil || row.ID != "42" {
				t.Errorf("row=%v err=%v", row, err)
			}
		}()
	}
	<-entered
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("scene queries=%d", calls.Load())
	}
}

func TestExactTitleSearchFindsReplacementOutsideBroadSearchWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if !strings.Contains(body.Query, "exactTitle:findScenes") {
			t.Error("broad text search should not run after exact title hit")
		}
		w.Write([]byte(`{"data":{"exactTitle":{"scenes":[{"id":"39192","title":"All The Way Through","code":"all-the-way-through"}]},"exactCode":{"scenes":[]}}}`))
	}))
	defer server.Close()
	rows, e := (&stashClient{base: server.URL, key: "secret"}).search(t.Context(), "All the way through")
	if e != nil || len(rows) != 1 || rows[0].ID != "39192" {
		t.Fatal(rows, e)
	}
}

func TestManualSearchContinuesAfterDeletedProviderScene(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if strings.Contains(body.Query, "findScene(id:") {
			w.Write([]byte(`{"data":{"findScene":null}}`))
			return
		}
		w.Write([]byte(`{"data":{"exactTitle":{"scenes":[{"id":"39192","title":"All The Way Through"}]},"exactCode":{"scenes":[]}}}`))
	}))
	defer server.Close()
	s := metadataServer{runtime: &runtimeServer{client: &stashClient{base: server.URL, key: "secret"}}}
	result, e := s.Search(t.Context(), &pluginv1.SearchMetadataRequest{ItemType: "movie", Query: "All the way through", ProviderIds: providerIDs("1074")})
	if e != nil || len(result.Results) != 1 || result.Results[0].ProviderIds.AsMap()["stash"] != "39192" {
		t.Fatal(result, e)
	}
}

func TestExactPathSearchDoesNotMixDuplicateOrSwappedTitles(t *testing.T) {
	path := "/collections/Studio - 2025-05-09 - Part 1 [WEBDL-2160p].mp4"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(req.Query, "exactPath:") || req.Variables["path"] != path {
			t.Errorf("missing exact path lookup: %+v", req)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"exactPath": map[string]any{"scenes": []any{
			map[string]any{"id": "right-file", "title": "Part 2", "files": []any{map[string]string{"path": path}}},
			map[string]any{"id": "wrong-file", "title": "Part 1", "files": []any{map[string]string{"path": "/other.mp4"}}},
		}}}})
	}))
	defer server.Close()
	c := &stashClient{base: server.URL, key: "key"}
	got, err := c.search(context.Background(), path)
	if err != nil || len(got) != 1 || got[0].ID != "right-file" || calls != 1 {
		t.Fatalf("got=%+v err=%v calls=%d", got, err, calls)
	}
}

func TestBlankStashTitleUsesVerifiedMediaFilename(t *testing.T) {
	var row scene
	if err := json.Unmarshal([]byte(`{"id":"42","files":[{"path":"/collections/Freeze/Freeze - 2023-08-29 - Therapy [WEBDL-2160p].mp4"}]}`), &row); err != nil {
		t.Fatal(err)
	}
	if got := displayTitle(row); got != "Therapy" {
		t.Fatalf("title=%q", got)
	}
	row.Title = "Source title"
	if got := displayTitle(row); got != "Source title" {
		t.Fatalf("source title overridden: %q", got)
	}
}
