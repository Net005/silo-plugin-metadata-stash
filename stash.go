package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type stashClient struct {
	base, key      string
	excludedScenes map[string]bool
	http           *http.Client
	playMu         sync.Mutex
	sceneMu        sync.Mutex
	sceneFlights   map[string]*sceneFlight
}

func excludedSceneIDs(raw string) map[string]bool {
	ids := map[string]bool{}
	for _, id := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }) {
		ids[strings.TrimPrefix(id, "stash:")] = true
	}
	return ids
}

type sceneFlight struct {
	done chan struct{}
	row  *scene
	err  error
}

type scene struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Code         string   `json:"code"`
	Details      string   `json:"details"`
	Director     string   `json:"director"`
	Date         string   `json:"date"`
	Duration     float64  `json:"duration"`
	PlayCount    int      `json:"play_count"`
	LastPlayedAt string   `json:"last_played_at"`
	PlayHistory  []string `json:"play_history"`
	PlayDuration float64  `json:"play_duration"`
	ResumeTime   float64  `json:"resume_time"`
	Studio       *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"studio"`
	Performers []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		ImagePath string `json:"image_path"`
	} `json:"performers"`
	Tags []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"tags"`
	Files []struct {
		Path     string  `json:"path"`
		Duration float64 `json:"duration"`
	} `json:"files"`
	Paths struct {
		Screenshot string `json:"screenshot"`
	} `json:"paths"`
	URLs []string `json:"urls"`
}

const sceneFields = `id title code details director date play_count last_played_at play_history play_duration resume_time studio { id name } performers { id name image_path } tags { id name } files { path duration } paths { screenshot } urls`

func (c *stashClient) configured() bool { return c != nil && c.base != "" && c.key != "" }
func (c *stashClient) graphql(ctx context.Context, query string, variables any, out any) error {
	if !c.configured() {
		return errors.New("configure the Stash URL and API key")
	}
	body, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/graphql", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("ApiKey", c.key)
	hc := c.http
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Stash GraphQL HTTP %d", resp.StatusCode)
	}
	var result struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result); err != nil {
		return err
	}
	if len(result.Errors) > 0 {
		return errors.New(result.Errors[0].Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(result.Data, out)
}
func (c *stashClient) findScene(ctx context.Context, id string) (*scene, error) {
	c.sceneMu.Lock()
	if flight := c.sceneFlights[id]; flight != nil {
		c.sceneMu.Unlock()
		select {
		case <-flight.done:
			return flight.row, flight.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.sceneFlights == nil {
		c.sceneFlights = map[string]*sceneFlight{}
	}
	flight := &sceneFlight{done: make(chan struct{})}
	c.sceneFlights[id] = flight
	c.sceneMu.Unlock()
	var data struct {
		Scene *scene `json:"findScene"`
	}
	flight.err = c.graphql(ctx, `query($id: ID!) { findScene(id:$id) { `+sceneFields+` } }`, map[string]any{"id": id}, &data)
	flight.row = data.Scene
	c.sceneMu.Lock()
	delete(c.sceneFlights, id)
	close(flight.done)
	c.sceneMu.Unlock()
	return flight.row, flight.err
}
func (c *stashClient) search(ctx context.Context, query string) ([]scene, error) {
	terms := searchTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	matches := []scene{}
	seen := map[string]bool{}
	for _, term := range terms {
		var data struct {
			Found struct {
				Scenes []scene `json:"scenes"`
			} `json:"findScenes"`
		}
		err := c.graphql(ctx, `query($filter:FindFilterType) { findScenes(filter:$filter) { scenes { `+sceneFields+` } } }`, map[string]any{"filter": map[string]any{"q": term, "per_page": 50}}, &data)
		if err != nil {
			return nil, err
		}
		key := compact(term)
		for _, item := range data.Found.Scenes {
			exact := item.Code != "" && compact(item.Code) == key || item.Title != "" && compact(item.Title) == key
			for _, file := range item.Files {
				stem := strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path))
				if compact(stem) == key {
					exact = true
				}
			}
			if exact && !seen[item.ID] {
				matches = append(matches, item)
				seen[item.ID] = true
			}
		}
	}
	return matches, nil
}

var qualitySuffix = regexp.MustCompile(`\s*\[[^\]]+\]\s*$`)
var dateSegment = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

func searchTerms(query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	stem := strings.TrimSuffix(filepath.Base(query), filepath.Ext(query))
	terms := []string{query}
	if stem != query {
		terms = append(terms, stem)
	}
	clean := strings.TrimSpace(qualitySuffix.ReplaceAllString(stem, ""))
	if clean != stem {
		terms = append(terms, clean)
	}
	parts := strings.Split(clean, " - ")
	for i := 0; i < len(parts)-1; i++ {
		if dateSegment.MatchString(strings.TrimSpace(parts[i])) {
			terms = append(terms, strings.TrimSpace(strings.Join(parts[i+1:], " - ")))
			break
		}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, term := range terms {
		key := compact(term)
		if key != "" && !seen[key] {
			seen[key] = true
			out = append(out, term)
		}
	}
	return out
}

func compact(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func (c *stashClient) allScenes(ctx context.Context, fields string, visit func(scene) error) error {
	for page := 1; ; page++ {
		var data struct {
			Found struct {
				Scenes []scene `json:"scenes"`
			} `json:"findScenes"`
		}
		err := c.graphql(ctx, `query($filter:FindFilterType) { findScenes(filter:$filter) { scenes { `+fields+` } } }`, map[string]any{"filter": map[string]any{"page": page, "per_page": 100, "sort": "created_at", "direction": "DESC"}}, &data)
		if err != nil {
			return err
		}
		for _, item := range data.Found.Scenes {
			if err = visit(item); err != nil {
				return err
			}
		}
		if len(data.Found.Scenes) < 100 {
			return nil
		}
	}
}
func (c *stashClient) imageURL(raw string) string {
	if raw == "" {
		return ""
	}
	base, err := url.Parse(c.base)
	if err != nil {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.IsAbs() && !strings.EqualFold(u.Host, base.Host) {
		return ""
	}
	full := base.ResolveReference(u)
	q := full.Query()
	q.Set("apikey", c.key)
	full.RawQuery = q.Encode()
	return full.String()
}
func (c *stashClient) saveActivity(ctx context.Context, id string, resume, delta float64) error {
	if resume < 0 {
		resume = 0
	}
	if delta < 0 {
		delta = 0
	}
	var out struct {
		OK bool `json:"sceneSaveActivity"`
	}
	err := c.graphql(ctx, fmt.Sprintf(`mutation { sceneSaveActivity(id:%q,resume_time:%.3f,playDuration:%.3f) }`, id, resume, delta), nil, &out)
	if err != nil {
		return err
	}
	if !out.OK {
		return errors.New("Stash rejected scene activity")
	}
	return nil
}
func (c *stashClient) addPlayOnce(ctx context.Context, id string, at time.Time) (bool, error) {
	if at.IsZero() {
		return false, errors.New("play timestamp required for duplicate-safe sync")
	}
	c.playMu.Lock()
	defer c.playMu.Unlock()
	item, err := c.findScene(ctx, id)
	if err != nil {
		return false, err
	}
	if item == nil {
		return false, errors.New("Stash scene missing")
	}
	if item.hasPlayAt(at) {
		return false, nil
	}
	if item.PlayCount != len(item.PlayHistory) {
		return false, errors.New("Stash play count does not match timestamped history; refusing automatic play write")
	}
	var out struct {
		Added struct {
			Count int `json:"count"`
		} `json:"sceneAddPlay"`
	}
	err = c.graphql(ctx, fmt.Sprintf(`mutation { sceneAddPlay(id:%q,times:[%q]) { count } }`, id, at.UTC().Format(time.RFC3339Nano)), nil, &out)
	return err == nil, err
}
func (s *scene) hasPlayAt(at time.Time) bool {
	if s == nil || at.IsZero() {
		return false
	}
	for _, raw := range s.PlayHistory {
		if old, err := time.Parse(time.RFC3339Nano, raw); err == nil && absDuration(old.Sub(at)) < 30*time.Second {
			return true
		}
	}
	return false
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
