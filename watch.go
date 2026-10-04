package main

import (
	"context"
	"fmt"
	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	"strconv"
	"strings"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type watchSyncServer struct {
	pluginv1.UnimplementedWatchSyncProviderServer
	runtime *runtimeServer
}

func (s *watchSyncServer) ExchangeAPIKey(_ context.Context, req *pluginv1.WatchSyncExchangeAPIKeyRequest) (*pluginv1.WatchSyncCredentialResponse, error) {
	c := s.runtime.stash()
	if !c.configured() {
		return &pluginv1.WatchSyncCredentialResponse{Fault: &pluginv1.WatchSyncFault{Code: pluginv1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_CREDENTIAL, SafeMessage: "Configure the Stash URL and API key first."}}, nil
	}
	token := req.GetApiKey()
	if token == "" {
		token = "stash"
	}
	return &pluginv1.WatchSyncCredentialResponse{Credentials: &pluginv1.WatchSyncCredentials{AccessToken: token, TokenType: "bearer"}, Account: &pluginv1.WatchSyncAccount{ExternalSubject: "stash", Username: "Stash", DisplayName: "StashApp"}}, nil
}
func (s *watchSyncServer) RefreshCredentials(_ context.Context, req *pluginv1.WatchSyncRefreshCredentialsRequest) (*pluginv1.WatchSyncCredentialResponse, error) {
	return &pluginv1.WatchSyncCredentialResponse{Credentials: req.GetContext().GetCredentials()}, nil
}
func (s *watchSyncServer) GetAccount(context.Context, *pluginv1.WatchSyncGetAccountRequest) (*pluginv1.WatchSyncGetAccountResponse, error) {
	return &pluginv1.WatchSyncGetAccountResponse{Account: &pluginv1.WatchSyncAccount{ExternalSubject: "stash", Username: "Stash", DisplayName: "StashApp"}}, nil
}
func (s *watchSyncServer) ApplyEvents(ctx context.Context, req *pluginv1.WatchSyncApplyEventsRequest) (*pluginv1.WatchSyncApplyEventsResponse, error) {
	out := &pluginv1.WatchSyncApplyEventsResponse{}
	for _, e := range req.GetEvents() {
		out.Results = append(out.Results, s.applyOne(ctx, e))
	}
	return out, nil
}
func (s *watchSyncServer) applyOne(ctx context.Context, e *pluginv1.WatchSyncEvent) *pluginv1.WatchSyncApplyResult {
	result := &pluginv1.WatchSyncApplyResult{EventId: e.GetEventId(), Status: pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_NO_CHANGE}
	switch e.GetOperation() {
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_START,
		pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_PAUSE,
		pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP,
		pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED:
	default:
		return result
	}
	c := s.runtime.stash()
	id := ""
	if e.GetMedia() != nil {
		id = e.GetMedia().GetExternalIds()[capabilityID]
	}
	if id == "" {
		id = e.GetProviderItemKey()
	}
	releaseID := int64(0)
	if e.GetMedia() != nil {
		releaseID, _ = strconv.ParseInt(strings.TrimSpace(e.GetMedia().GetExternalIds()["javbeacon"]), 10, 64)
	}
	if releaseID == 0 {
		if raw, ok := strings.CutPrefix(e.GetProviderItemKey(), "javbeacon:"); ok {
			releaseID, _ = strconv.ParseInt(raw, 10, 64)
		}
	}
	if strings.HasPrefix(id, "javbeacon:") || (releaseID > 0 && e.GetMedia().GetExternalIds()[capabilityID] == "" && !strings.HasPrefix(e.GetProviderItemKey(), "stash:")) {
		id = ""
	}
	id = stashPlaybackID(id)
	if id == "" && releaseID == 0 && e.GetMedia().GetMediaItemId() != "" {
		s.runtime.mu.RLock()
		base, key := s.runtime.siloBase, s.runtime.siloKey
		s.runtime.mu.RUnlock()
		if base != "" && key != "" && c.configured() {
			paths, err := provider.NewSiloClient(base, key).ItemFilePaths(ctx, e.GetMedia().GetMediaItemId())
			if err != nil {
				return retryResult(e, "Unable to read Silo playback file identity")
			}
			id, err = c.sceneIDForExactPaths(ctx, paths)
			if err != nil {
				return retryResult(e, "Unable to resolve an unambiguous Stash playback file")
			}
		}
	}
	if c != nil && c.excludedScenes[id] {
		return result
	}
	if s.runtime.legacy != nil && s.runtime.legacy.Provider().Configured() && (id != "" || releaseID > 0) {
		return s.applyViaJAVBeacon(ctx, e, id, releaseID)
	}
	if id == "" {
		return result
	}
	if !c.configured() {
		return retryResult(e, "Stash connection not configured")
	}
	var err error
	changed := false
	switch e.GetOperation() {
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_PAUSE, pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP:
		// Silo reports the absolute position, not a duration delta. Preserve resume
		// without inflating Stash's play_duration on retries.
		err = c.saveActivity(ctx, id, e.GetPositionSeconds(), 0)
		changed = err == nil
		if err == nil && e.GetOperation() == pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP && e.GetCompleted() {
			changed, err = c.addPlayOnce(ctx, id, eventTime(e))
		}
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED:
		changed, err = c.addPlayOnce(ctx, id, eventTime(e))
	default:
		return result
	}
	if err != nil {
		return retryResult(e, err.Error())
	}
	if changed {
		result.Status = pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED
	}
	return result
}

func stashPlaybackID(raw string) string {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "stash:")
	if raw == "" {
		return ""
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return raw
}
func (s *watchSyncServer) applyViaJAVBeacon(ctx context.Context, e *pluginv1.WatchSyncEvent, sceneID string, releaseID int64) *pluginv1.WatchSyncApplyResult {
	result := &pluginv1.WatchSyncApplyResult{EventId: e.GetEventId(), Status: pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_NO_CHANGE}
	pb := provider.PlaybackEvent{ReleaseID: releaseID, StashSceneID: sceneID, SessionID: e.GetPlaybackSessionId(), PositionSeconds: e.GetPositionSeconds(), RuntimeSeconds: e.GetDurationSeconds(), OccurredAt: eventTime(e)}
	switch e.GetOperation() {
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_START:
		pb.Event = "start"
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_PAUSE:
		pb.Event = "progress"
		pb.IsPaused = true
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP:
		pb.Event = "stop"
		pb.IsPlayed = e.GetCompleted()
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED:
		pb.Event = "stop"
		pb.IsPlayed = true
	default:
		return result
	}
	if sceneID != "" && pb.IsPlayed && !pb.OccurredAt.IsZero() {
		if stash := s.runtime.stash(); stash.configured() {
			row, err := stash.findScene(ctx, sceneID)
			if err != nil {
				return retryResult(e, err.Error())
			}
			if row != nil && row.hasPlayAt(pb.OccurredAt) {
				return result
			}
			if row == nil || row.PlayCount != len(row.PlayHistory) {
				return retryResult(e, "Stash play history is incomplete; refusing automatic play write")
			}
		}
	}
	if pb.SessionID == "" {
		pb.SessionID = e.GetWatchHistoryId()
	}
	if pb.SessionID == "" {
		pb.SessionID = e.GetEventId()
	}
	if _, err := s.runtime.legacy.Provider().ReportPlayback(ctx, pb); err != nil {
		return retryResult(e, err.Error())
	}
	result.Status = pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED
	return result
}

func eventTime(e *pluginv1.WatchSyncEvent) time.Time {
	if at := e.GetOccurredAt(); at != nil && at.IsValid() {
		return at.AsTime()
	}
	return time.Time{}
}
func retryResult(e *pluginv1.WatchSyncEvent, msg string) *pluginv1.WatchSyncApplyResult {
	return &pluginv1.WatchSyncApplyResult{EventId: e.GetEventId(), Status: pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_RETRY, Fault: &pluginv1.WatchSyncFault{Code: pluginv1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_TEMPORARY, SafeMessage: msg}}
}
func (s *watchSyncServer) ListRemoteState(ctx context.Context, req *pluginv1.WatchSyncListRemoteStateRequest) (*pluginv1.WatchSyncListRemoteStateResponse, error) {
	c := s.runtime.stash()
	if !c.configured() {
		return &pluginv1.WatchSyncListRemoteStateResponse{Fault: &pluginv1.WatchSyncFault{Code: pluginv1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_CREDENTIAL, SafeMessage: "Stash connection not configured"}}, nil
	}
	wantWatched := len(req.GetStateKinds()) == 0
	watchlistRequested := false
	for _, kind := range req.GetStateKinds() {
		if kind == pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHED {
			wantWatched = true
		}
		if kind == pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHLIST {
			watchlistRequested = true
		}
	}
	// This provider has no personal Watchlist capability. An explicit legacy request
	// must never return an authoritative empty snapshot that could clear Silo state.
	if !wantWatched {
		return &pluginv1.WatchSyncListRemoteStateResponse{CompleteSnapshot: false}, nil
	}
	page := 1
	if req.GetPageToken() != "" {
		n, err := strconv.Atoi(req.GetPageToken())
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid page token")
		}
		page = n
	}
	size := int(req.GetPageSize())
	if size < 1 || size > 100 {
		size = 100
	}
	var data struct {
		Found struct {
			Scenes []scene `json:"scenes"`
		} `json:"findScenes"`
	}
	err := c.graphql(ctx, `query($filter:FindFilterType) { findScenes(filter:$filter) { scenes { id play_count last_played_at } } }`, map[string]any{"filter": map[string]any{"page": page, "per_page": size, "sort": "created_at", "direction": "DESC"}}, &data)
	if err != nil {
		return &pluginv1.WatchSyncListRemoteStateResponse{Fault: &pluginv1.WatchSyncFault{Code: pluginv1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_TEMPORARY, SafeMessage: err.Error()}}, nil
	}
	out := &pluginv1.WatchSyncListRemoteStateResponse{CompleteSnapshot: !watchlistRequested}
	for _, row := range data.Found.Scenes {
		state := &pluginv1.WatchSyncRemoteState{ProviderItemKey: "stash:" + row.ID, Media: &pluginv1.WatchSyncMedia{MediaType: pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, ExternalIds: map[string]string{capabilityID: row.ID}}}
		if wantWatched && row.PlayCount > 0 {
			w := &pluginv1.WatchSyncRemoteWatchedState{PlayCount: int32(row.PlayCount)}
			if at, err := time.Parse(time.RFC3339Nano, row.LastPlayedAt); err == nil {
				w.LastWatchedAt = timestamppb.New(at)
			}
			state.Watched = w
		}
		if state.Watched != nil {
			out.Items = append(out.Items, state)
		}
	}
	if len(data.Found.Scenes) == size {
		out.NextPageToken = strconv.Itoa(page + 1)
	}
	return out, nil
}
