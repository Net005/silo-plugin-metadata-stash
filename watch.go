package main

import (
	"context"
	"fmt"
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
	c := s.runtime.stash()
	if !c.configured() {
		return retryResult(e, "Stash connection not configured")
	}
	id := ""
	if e.GetMedia() != nil {
		id = e.GetMedia().GetExternalIds()[capabilityID]
	}
	if id == "" {
		id = e.GetProviderItemKey()
	}
	id = strings.TrimPrefix(id, "stash:")
	if id == "" {
		return result
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
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST:
		changed, err = c.setWatchlist(ctx, id, true)
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FROM_WATCHLIST:
		changed, err = c.setWatchlist(ctx, id, false)
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
	err := c.graphql(ctx, `query($filter:FindFilterType) { findScenes(filter:$filter) { scenes { id play_count last_played_at tags { id } } } }`, map[string]any{"filter": map[string]any{"page": page, "per_page": size, "sort": "created_at", "direction": "DESC"}}, &data)
	if err != nil {
		return &pluginv1.WatchSyncListRemoteStateResponse{Fault: &pluginv1.WatchSyncFault{Code: pluginv1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_TEMPORARY, SafeMessage: err.Error()}}, nil
	}
	wantWatched, wantWatchlist := len(req.GetStateKinds()) == 0, len(req.GetStateKinds()) == 0
	for _, kind := range req.GetStateKinds() {
		if kind == pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHED {
			wantWatched = true
		}
		if kind == pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHLIST {
			wantWatchlist = true
		}
	}
	out := &pluginv1.WatchSyncListRemoteStateResponse{CompleteSnapshot: true}
	for _, row := range data.Found.Scenes {
		state := &pluginv1.WatchSyncRemoteState{ProviderItemKey: "stash:" + row.ID, Media: &pluginv1.WatchSyncMedia{MediaType: pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, ExternalIds: map[string]string{capabilityID: row.ID}}}
		if wantWatched && row.PlayCount > 0 {
			w := &pluginv1.WatchSyncRemoteWatchedState{PlayCount: int32(row.PlayCount)}
			if at, err := time.Parse(time.RFC3339Nano, row.LastPlayedAt); err == nil {
				w.LastWatchedAt = timestamppb.New(at)
			}
			state.Watched = w
		}
		if wantWatchlist && c.watchlistTag != "" {
			for _, tag := range row.Tags {
				if tag.ID == c.watchlistTag {
					state.Watchlist = &pluginv1.WatchSyncRemoteListState{}
					break
				}
			}
		}
		if state.Watched != nil || state.Watchlist != nil {
			out.Items = append(out.Items, state)
		}
	}
	if len(data.Found.Scenes) == size {
		out.NextPageToken = strconv.Itoa(page + 1)
	}
	return out, nil
}
