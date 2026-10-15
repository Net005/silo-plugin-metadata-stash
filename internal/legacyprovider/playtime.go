package legacyprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// PlaybackSession is live telemetry, not the history row's watched_seconds
// (which can contain the final media position).
type PlaybackSession struct {
	ID        string    `json:"session_id"`
	ContentID string    `json:"content_id"`
	FileID    int64     `json:"media_file_id,string"`
	Profile   string    `json:"profile_id"`
	MediaType string    `json:"media_type"`
	Position  float64   `json:"position_seconds"`
	Paused    bool      `json:"is_paused"`
	Updated   time.Time `json:"updated_at"`
}

func (c *SiloClient) PlaybackSessions(ctx context.Context) ([]PlaybackSession, error) {
	var out []PlaybackSession
	cursor := ""
	for page := 0; page < 8; page++ {
		path := "/api/v2/admin/sessions?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("silo: session request failed")
		}
		var data struct {
			Items []PlaybackSession `json:"items"`
			Page  struct {
				HasMore bool   `json:"has_more"`
				Next    string `json:"next_cursor"`
			} `json:"page"`
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("silo: sessions HTTP %d", resp.StatusCode)
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&data)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("silo: invalid session telemetry")
		}
		out = append(out, data.Items...)
		if len(out) > 256 {
			return nil, fmt.Errorf("silo: too many active sessions")
		}
		if !data.Page.HasMore {
			return out, nil
		}
		if data.Page.Next == "" || data.Page.Next == cursor {
			return nil, fmt.Errorf("silo: session pagination did not advance")
		}
		cursor = data.Page.Next
	}
	return nil, fmt.Errorf("silo: too many session pages")
}
