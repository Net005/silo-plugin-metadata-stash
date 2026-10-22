package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
)

type orphanFixture struct {
	task         *scheduledTaskServer
	server       *httptest.Server
	root, video  string
	scans        int
	store        *orphanMemoryStore
	alter        func(string, map[string]any)
	scanStatus   int
	matchCalls   int
	matchFailure bool
	retries      int
}

type orphanMemoryStore struct {
	data       []byte
	failWrite  bool
	afterWrite func()
}

func (m *orphanMemoryStore) ReadInstanceState(context.Context, string) ([]byte, bool, error) {
	return append([]byte(nil), m.data...), len(m.data) > 0, nil
}
func (m *orphanMemoryStore) WriteInstanceState(_ context.Context, _ string, data []byte) error {
	if m.failWrite {
		return fmt.Errorf("private state unavailable")
	}
	m.data = append([]byte(nil), data...)
	if m.afterWrite != nil {
		m.afterWrite()
	}
	return nil
}

func newOrphanFixture(t *testing.T) *orphanFixture {
	t.Helper()
	f := &orphanFixture{root: t.TempDir(), store: &orphanMemoryStore{}}
	f.video = filepath.Join(f.root, "EXAMPLE-001.mp4")
	if err := os.WriteFile(f.video, []byte("synthetic video bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body any
		switch r.URL.Path {
		case "/api/v2/libraries/unmatched-items":
			f.matchCalls++
			if f.matchFailure {
				http.Error(w, "catalog unavailable", 503)
				return
			}
			body = map[string]any{"items": []any{}, "page": map[string]any{"has_more": false}}
		case "/api/v2/libraries":
			body = map[string]any{"items": []any{map[string]any{"id": "7", "type": "movies", "enabled": true, "paths": []string{f.root}}}}
		case "/api/v2/libraries/7/providers":
			body = map[string]any{"levels": []any{map[string]any{"content_level": "movie", "entries": []any{map[string]any{"enabled": true, "capability_id": "stash", "provider_slug": "stash"}}}}}
		case "/api/v2/admin/unmatched":
			body = map[string]any{"items": []any{map[string]any{"id": "11", "media_folder_id": "7", "file_path": f.video, "file_size": 21}}, "page": map[string]any{"has_more": false}}
		case "/api/v2/libraries/7/metadata-match-queue":
			body = map[string]any{"library_id": "7", "movies": []any{map[string]any{"media_file_id": "11", "library_id": "7", "file_path": f.video, "state": "pending", "failure_kind": "candidate_rejected", "failure_detail": map[string]any{"decision": map[string]any{"outcome": "candidate_rejected", "candidate_count": 1, "threshold": 55, "top_candidates": []any{map[string]any{"title": "EXAMPLE-001", "year": 2024, "provider_ids": map[string]string{"stash": "stash:42"}, "sources": []string{"stash"}, "score": 63, "reasons": []string{"exact_title", "provider_sources"}}}}}}}, "series": []any{}, "raw_files": []any{}, "page": map[string]any{"has_more": false}}
		case "/graphql":
			var req struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if strings.Contains(req.Query, "play_") || strings.Contains(req.Query, "resume_time") {
				t.Error("orphan repair must not read playback data")
			}
			if strings.Contains(req.Query, "findScenes") {
				if strings.Contains(req.Query, "exactTitle") || !strings.Contains(req.Query, "scene_filter:{path:$path}") {
					t.Error("non-exact Stash query")
				}
				body = map[string]any{"data": map[string]any{"findScenes": map[string]any{"count": 1, "scenes": []any{map[string]any{"id": "42", "files": []any{map[string]any{"path": f.video}}}}}}}
			} else {
				body = map[string]any{"data": map[string]any{"findScene": map[string]any{"id": "42", "code": "EXAMPLE-001", "title": "Example & title", "date": "2024-02-29", "files": []any{map[string]any{"path": f.video}}}}}
			}
		case "/api/v2/scan":
			if r.Method != "POST" {
				t.Error("scan must use POST")
			}
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req["library_id"] != "7" || req["path"] != f.video || len(req) != 2 {
				t.Errorf("unsafe scan scope: %v", req)
			}
			f.scans++
			if f.scanStatus != 0 {
				w.WriteHeader(f.scanStatus)
			} else {
				w.WriteHeader(http.StatusAccepted)
			}
			body = map[string]any{"status": "accepted", "mode": "file", "library_id": "7"}
		case "/api/v2/libraries/7/metadata-match-queue/retry":
			if r.Method != "POST" {
				t.Error("queue retry must use POST")
			}
			f.retries++
			body = map[string]any{"status": "queued", "library_id": "7"}
		default:
			t.Errorf("unexpected endpoint %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		if r.URL.Path != "/graphql" && r.URL.Path != "/api/v2/scan" && !strings.HasSuffix(r.URL.Path, "/metadata-match-queue/retry") && r.Method != "GET" {
			t.Errorf("unexpected write %s", r.URL.Path)
		}
		if f.alter != nil {
			f.alter(r.URL.Path, body.(map[string]any))
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(f.server.Close)
	f.task = &scheduledTaskServer{runtime: &runtimeServer{siloBase: f.server.URL, siloKey: "test-key", client: &stashClient{base: f.server.URL, key: "test-key"}, orphanRepairEnabled: true}, log: hclog.NewNullLogger(), orphanStore: f.store}
	return f
}

func TestOrphanRepairDisabledDoesNotReadOrWrite(t *testing.T) {
	f := newOrphanFixture(t)
	f.task.runtime.orphanRepairEnabled = false
	f.task.orphanStore = nil
	summary, err := f.task.repairUnassociatedFiles(t.Context())
	if err != nil || summary["status"] != "disabled" {
		t.Fatalf("summary=%v err=%v", summary, err)
	}
	if f.scans != 0 {
		t.Fatal("disabled repair scanned")
	}
	if _, err = os.Lstat(strings.TrimSuffix(f.video, ".mp4") + ".nfo"); !os.IsNotExist(err) {
		t.Fatal("disabled repair wrote NFO")
	}
}

func TestOrphanRepairRejectsUnverifiedExactIdentity(t *testing.T) {
	for _, kind := range []string{"count_missing", "count_incomplete", "ambiguous", "duplicate", "name_only", "fresh_path_changed", "fresh_id_changed", "empty_id"} {
		t.Run(kind, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.alter = func(path string, body map[string]any) {
				if path != "/graphql" {
					return
				}
				data := body["data"].(map[string]any)
				if found, ok := data["findScenes"].(map[string]any); ok {
					rows := found["scenes"].([]any)
					row := rows[0].(map[string]any)
					switch kind {
					case "count_missing":
						delete(found, "count")
					case "count_incomplete":
						found["count"] = 2
					case "ambiguous":
						found["count"] = 2
						found["scenes"] = append(rows, map[string]any{"id": "43", "files": row["files"]})
					case "duplicate":
						found["count"] = 2
						found["scenes"] = append(rows, row)
					case "name_only":
						row["files"] = []any{map[string]any{"path": "/media/elsewhere/EXAMPLE-001.mp4"}}
					case "empty_id":
						row["id"] = ""
					}
				} else {
					row := data["findScene"].(map[string]any)
					if kind == "fresh_path_changed" {
						row["files"] = []any{map[string]any{"path": "/media/elsewhere.mp4"}}
					}
					if kind == "fresh_id_changed" {
						row["id"] = "43"
					}
				}
			}
			_, _ = f.task.repairUnassociatedFiles(t.Context())
			if f.scans != 0 {
				t.Fatalf("unverified identity scanned %d times", f.scans)
			}
			if _, err := os.Lstat(strings.TrimSuffix(f.video, ".mp4") + ".nfo"); !os.IsNotExist(err) {
				t.Fatal("unverified identity wrote NFO")
			}
		})
	}
}

func TestOrphanRepairRequiresKnownYearlessSingleProviderFailure(t *testing.T) {
	for _, kind := range []string{"parked", "parked_at", "wrong_failure", "wrong_outcome", "multiple_candidates", "wrong_provider", "wrong_scene", "multiple_sources", "already_year", "low_score", "high_score", "no_reasons", "wrong_library", "wrong_path", "year_mismatch", "missing_date", "invalid_date", "zero_year", "empty_title"} {
		t.Run(kind, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.alter = func(path string, b map[string]any) {
				if strings.HasSuffix(path, "metadata-match-queue") {
					row := b["movies"].([]any)[0].(map[string]any)
					d := row["failure_detail"].(map[string]any)["decision"].(map[string]any)
					c := d["top_candidates"].([]any)[0].(map[string]any)
					switch kind {
					case "parked":
						row["state"] = "parked"
					case "parked_at":
						row["parked_at"] = "2026-01-01T00:00:00Z"
					case "wrong_failure":
						row["failure_kind"] = "no_candidates"
					case "wrong_outcome":
						d["outcome"] = "provider_transient"
					case "multiple_candidates":
						d["candidate_count"] = 2
					case "wrong_provider":
						c["provider_ids"] = map[string]string{"tmdb": "42"}
					case "wrong_scene":
						c["provider_ids"] = map[string]string{"stash": "stash:43"}
					case "multiple_sources":
						c["sources"] = []string{"stash", "tmdb"}
					case "already_year":
						c["reasons"] = []string{"exact_title", "provider_sources", "exact_year"}
					case "low_score":
						c["score"] = 54
					case "high_score":
						c["score"] = 70
					case "no_reasons":
						delete(c, "reasons")
					case "wrong_library":
						row["library_id"] = "8"
					case "wrong_path":
						row["file_path"] = "/media/not-the-file.mp4"
					case "year_mismatch":
						c["year"] = 2023
					}
				}
				if path == "/graphql" {
					if row, ok := b["data"].(map[string]any)["findScene"].(map[string]any); ok {
						switch kind {
						case "missing_date":
							delete(row, "date")
						case "invalid_date":
							row["date"] = "2024-02-30"
						case "zero_year":
							row["date"] = "0000-01-01"
						case "empty_title":
							row["code"] = ""
							row["title"] = ""
						}
					}
				}
			}
			_, _ = f.task.repairUnassociatedFiles(t.Context())
			if f.scans != 0 {
				t.Fatalf("unsafe failure repaired: %s", kind)
			}
			if _, err := os.Lstat(strings.TrimSuffix(f.video, ".mp4") + ".nfo"); !os.IsNotExist(err) {
				t.Fatal("unsafe failure wrote NFO")
			}
		})
	}
}

func TestOrphanRepairPreservesExistingSidecars(t *testing.T) {
	for _, kind := range []string{"file", "case_variant", "directory", "link", "dangling_link"} {
		t.Run(kind, func(t *testing.T) {
			f := newOrphanFixture(t)
			target := strings.TrimSuffix(f.video, ".mp4") + ".nfo"
			side := target
			if kind == "case_variant" {
				side = strings.TrimSuffix(f.video, ".mp4") + ".NFO"
			}
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(side, 0700)
			case "link":
				err = os.Symlink(f.video, side)
			case "dangling_link":
				err = os.Symlink("absent.nfo", side)
			default:
				err = os.WriteFile(side, []byte("user metadata"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(side)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.task.repairUnassociatedFiles(t.Context())
			after, err := os.Lstat(side)
			if err != nil {
				t.Fatal(err)
			}
			if f.scans != 0 || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("preexisting sidecar changed or scanned")
			}
			if kind == "file" || kind == "case_variant" {
				raw, err := os.ReadFile(side)
				if err != nil || string(raw) != "user metadata" {
					t.Fatal("user NFO overwritten")
				}
			}
			if kind == "case_variant" {
				if _, err = os.Lstat(target); !os.IsNotExist(err) {
					t.Fatal("created conflicting lowercase NFO")
				}
			}
		})
	}
}

func TestOrphanRepairRejectsUnsafePhysicalFiles(t *testing.T) {
	for _, kind := range []string{"outside_root", "sibling_prefix", "symlink_file", "symlink_directory", "directory", "empty", "wrong_size", "year_in_name"} {
		t.Run(kind, func(t *testing.T) {
			f := newOrphanFixture(t)
			switch kind {
			case "outside_root":
				f.video = filepath.Join(t.TempDir(), "EXAMPLE-001.mp4")
				if err := os.WriteFile(f.video, []byte("synthetic video bytes"), 0600); err != nil {
					t.Fatal(err)
				}
			case "sibling_prefix":
				old := f.root
				f.root = old + "-other" // fixture library root differs from original video root
			case "symlink_file":
				target := filepath.Join(t.TempDir(), "other.mp4")
				_ = os.WriteFile(target, []byte("synthetic video bytes"), 0600)
				_ = os.Remove(f.video)
				if err := os.Symlink(target, f.video); err != nil {
					t.Fatal(err)
				}
			case "symlink_directory":
				dir := filepath.Join(f.root, "linked")
				if err := os.Symlink(t.TempDir(), dir); err != nil {
					t.Fatal(err)
				}
				f.video = filepath.Join(dir, "EXAMPLE-001.mp4")
				_ = os.WriteFile(f.video, []byte("synthetic video bytes"), 0600)
			case "directory":
				_ = os.Remove(f.video)
				_ = os.Mkdir(f.video, 0700)
			case "empty":
				_ = os.WriteFile(f.video, nil, 0600)
			case "wrong_size":
				_ = os.WriteFile(f.video, []byte("changed bytes"), 0600)
			case "year_in_name":
				old := f.video
				f.video = filepath.Join(f.root, "EXAMPLE-001 (2023).mp4")
				_ = os.Rename(old, f.video)
			}
			_, _ = f.task.repairUnassociatedFiles(t.Context())
			if f.scans != 0 {
				t.Fatalf("unsafe physical file admitted: %s", kind)
			}
			if _, err := os.Lstat(strings.TrimSuffix(f.video, ".mp4") + ".nfo"); !os.IsNotExist(err) {
				t.Fatal("unsafe file got NFO")
			}
		})
	}
}

func TestOrphanRepairRequiresCompletePaginationBeforeWrites(t *testing.T) {
	for _, kind := range []string{"missing_inventory_page", "missing_inventory_more", "missing_inventory_items", "duplicate_inventory_id", "duplicate_inventory_path", "missing_inventory_id", "missing_inventory_path", "missing_queue_page", "missing_queue_more", "missing_queue_movies", "duplicate_queue_row", "wrong_queue_library", "inventory_cursor_missing", "queue_cursor_missing", "inventory_cursor_loop", "queue_cursor_loop", "inventory_limit", "queue_limit", "terminal_cursor"} {
		t.Run(kind, func(t *testing.T) {
			f := newOrphanFixture(t)
			calls := 0
			f.alter = func(path string, b map[string]any) {
				inv := path == "/api/v2/admin/unmatched"
				queue := strings.HasSuffix(path, "metadata-match-queue")
				if !inv && !queue {
					return
				}
				switch kind {
				case "missing_inventory_page":
					if inv {
						delete(b, "page")
					}
				case "missing_inventory_more":
					if inv {
						b["page"] = map[string]any{}
					}
				case "missing_inventory_items":
					if inv {
						delete(b, "items")
					}
				case "duplicate_inventory_id":
					if inv {
						rows := b["items"].([]any)
						b["items"] = append(rows, rows[0])
					}
				case "duplicate_inventory_path":
					if inv {
						rows := b["items"].([]any)
						row := map[string]any{}
						for k, v := range rows[0].(map[string]any) {
							row[k] = v
						}
						row["id"] = "12"
						b["items"] = append(rows, row)
					}
				case "missing_inventory_id":
					if inv {
						delete(b["items"].([]any)[0].(map[string]any), "id")
					}
				case "missing_inventory_path":
					if inv {
						delete(b["items"].([]any)[0].(map[string]any), "file_path")
					}
				case "missing_queue_page":
					if queue {
						delete(b, "page")
					}
				case "missing_queue_more":
					if queue {
						b["page"] = map[string]any{}
					}
				case "missing_queue_movies":
					if queue {
						delete(b, "movies")
					}
				case "duplicate_queue_row":
					if queue {
						rows := b["movies"].([]any)
						b["movies"] = append(rows, rows[0])
					}
				case "wrong_queue_library":
					if queue {
						b["library_id"] = "8"
					}
				case "inventory_cursor_missing":
					if inv {
						b["page"] = map[string]any{"has_more": true}
					}
				case "queue_cursor_missing":
					if queue {
						b["page"] = map[string]any{"has_more": true}
					}
				case "inventory_cursor_loop", "inventory_limit":
					if inv {
						calls++
						b["items"].([]any)[0].(map[string]any)["id"] = fmt.Sprint(calls)
						b["items"].([]any)[0].(map[string]any)["file_path"] = filepath.Join(f.root, fmt.Sprintf("extra-%d.mp4", calls))
						cursor := "same"
						if kind == "inventory_limit" {
							cursor = fmt.Sprint(calls)
						}
						b["page"] = map[string]any{"has_more": true, "next_cursor": cursor}
					}
				case "queue_cursor_loop", "queue_limit":
					if queue {
						calls++
						row := b["movies"].([]any)[0].(map[string]any)
						if calls > 1 {
							row["media_file_id"] = fmt.Sprint(calls + 100)
							row["file_path"] = filepath.Join(f.root, fmt.Sprintf("extra-%d.mp4", calls))
						}
						cursor := "same"
						if kind == "queue_limit" {
							cursor = fmt.Sprint(calls)
						}
						b["page"] = map[string]any{"has_more": true, "next_cursor": cursor}
					}
				case "terminal_cursor":
					if inv {
						b["page"] = map[string]any{"has_more": false, "next_cursor": "unexpected"}
					}
				}
			}
			_, err := f.task.repairUnassociatedFiles(t.Context())
			if err == nil {
				t.Fatal("incomplete/ambiguous inventory accepted")
			}
			if f.scans != 0 {
				t.Fatal("scan before pagination completion")
			}
			if _, err = os.Lstat(strings.TrimSuffix(f.video, ".mp4") + ".nfo"); !os.IsNotExist(err) {
				t.Fatal("NFO before pagination completion")
			}
		})
	}
}

func expireOrphanMarkers(t *testing.T, f *orphanFixture) {
	t.Helper()
	markers := map[string]orphanMarker{}
	if err := json.Unmarshal(f.store.data, &markers); err != nil {
		t.Fatal(err)
	}
	for k, m := range markers {
		m.Next = time.Now().Add(-time.Hour)
		m.AdmittedAt = m.AdmittedAt.Add(-time.Hour)
		m.QueueRetryAt = time.Now().Add(-time.Hour)
		markers[k] = m
	}
	raw, err := json.Marshal(markers)
	if err != nil {
		t.Fatal(err)
	}
	f.store.data = raw
}

func TestOrphanRepairAdmissionIsPendingAndNeverRepeated(t *testing.T) {
	f := newOrphanFixture(t)
	if _, err := f.task.repairUnassociatedFiles(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(strings.TrimSuffix(f.video, ".mp4") + ".nfo")
	if err != nil {
		t.Fatal(err)
	}
	expireOrphanMarkers(t, f)
	summary, err := f.task.repairUnassociatedFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if f.scans != 1 || summary["admitted"] != 0 || summary["pending"] != 1 {
		t.Fatalf("repeated or falsely completed: scans=%d summary=%v", f.scans, summary)
	}
	if f.retries != 1 || summary["queue_retried"] != 1 {
		t.Fatalf("still-orphaned admission must force one match-queue retry: retries=%d summary=%v", f.retries, summary)
	}
	after, err := os.Stat(strings.TrimSuffix(f.video, ".mp4") + ".nfo")
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("auto NFO rewritten")
	}
}

func TestOrphanRepairFailedScanRetryIsDurableAndCooledDown(t *testing.T) {
	f := newOrphanFixture(t)
	f.scanStatus = 500
	summary, _ := f.task.repairUnassociatedFiles(t.Context())
	if f.scans != 1 || summary["scan_failed"] != 1 {
		t.Fatalf("missing failure summary: %v", summary)
	}
	if _, err := f.task.repairUnassociatedFiles(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.scans != 1 {
		t.Fatal("failed scan retried before cooldown")
	}
	expireOrphanMarkers(t, f)
	f.scanStatus = 0
	// A fresh task reads durable markers, not a transient in-memory retry map.
	f.task = &scheduledTaskServer{runtime: f.task.runtime, orphanStore: f.store, log: hclog.NewNullLogger()}
	summary, err := f.task.repairUnassociatedFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if f.scans != 2 || summary["admitted"] != 1 || summary["pending"] != 1 {
		t.Fatalf("retry lost: scans=%d summary=%v", f.scans, summary)
	}
}

func TestOrphanRepairRefusesMutationWhenPrivateStateFails(t *testing.T) {
	f := newOrphanFixture(t)
	f.store.failWrite = true
	if _, err := f.task.repairUnassociatedFiles(t.Context()); err == nil {
		t.Fatal("state failure ignored")
	}
	if f.scans != 0 {
		t.Fatal("scan without durable reservation")
	}
	if _, err := os.Lstat(strings.TrimSuffix(f.video, ".mp4") + ".nfo"); !os.IsNotExist(err) {
		t.Fatal("NFO published without durable ownership marker")
	}
}

func TestOrphanRepairNeverRetriesUserModifiedOwnedFiles(t *testing.T) {
	for _, kind := range []string{"nfo_edited", "nfo_replaced", "nfo_removed", "nfo_case_conflict", "video_replaced", "video_changed"} {
		t.Run(kind, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.scanStatus = 500
			_, _ = f.task.repairUnassociatedFiles(t.Context())
			expireOrphanMarkers(t, f)
			side := strings.TrimSuffix(f.video, ".mp4") + ".nfo"
			raw, err := os.ReadFile(side)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "nfo_edited":
				_ = os.WriteFile(side, []byte("user replacement metadata"), 0600)
			case "nfo_replaced":
				_ = os.Remove(side)
				_ = os.WriteFile(side, raw, 0600)
			case "nfo_removed":
				_ = os.Remove(side)
			case "nfo_case_conflict":
				_ = os.WriteFile(strings.TrimSuffix(f.video, ".mp4")+".NFO", []byte("user metadata"), 0600)
			case "video_replaced":
				_ = os.Remove(f.video)
				_ = os.WriteFile(f.video, []byte("synthetic video bytes"), 0600)
			case "video_changed":
				_ = os.WriteFile(f.video, []byte("changed media bytes!!"), 0600)
			}
			_, _ = f.task.repairUnassociatedFiles(t.Context())
			if f.scans != 1 {
				t.Fatal("retried user-modified NFO or video")
			}
			if kind == "nfo_removed" {
				if _, err = os.Lstat(side); !os.IsNotExist(err) {
					t.Fatal("recreated removed NFO")
				}
			}
		})
	}
}

func TestOrphanRepairRevalidatesAfterStateWriteAndPublishesNoReplace(t *testing.T) {
	for _, kind := range []string{"video_changed", "nfo_race", "case_race"} {
		t.Run(kind, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.store.afterWrite = func() {
				f.store.afterWrite = nil
				if kind == "video_changed" {
					_ = os.WriteFile(f.video, []byte("new media bytes"), 0600)
				} else {
					ext := ".nfo"
					if kind == "case_race" {
						ext = ".NFO"
					}
					_ = os.WriteFile(strings.TrimSuffix(f.video, ".mp4")+ext, []byte("user racing metadata"), 0600)
				}
			}
			_, _ = f.task.repairUnassociatedFiles(t.Context())
			if f.scans != 0 {
				t.Fatal("scan after file identity race")
			}
			target := strings.TrimSuffix(f.video, ".mp4") + ".nfo"
			if kind == "nfo_race" {
				raw, err := os.ReadFile(target)
				if err != nil || string(raw) != "user racing metadata" {
					t.Fatal("racing metadata overwritten")
				}
			} else {
				if _, err := os.Lstat(target); !os.IsNotExist(err) {
					t.Fatal("published despite identity or sidecar race")
				}
			}
		})
	}
}

func TestScheduledStartKeepsOrphanAndCatalogErrorsIndependent(t *testing.T) {
	for _, kind := range []string{"catalog_error", "orphan_error"} {
		t.Run(kind, func(t *testing.T) {
			f := newOrphanFixture(t)
			if kind == "catalog_error" {
				f.matchFailure = true
			} else {
				f.alter = func(path string, b map[string]any) {
					if path == "/api/v2/admin/unmatched" {
						delete(b, "page")
					}
				}
			}
			if !f.task.start() {
				t.Fatal("task not started")
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				f.task.mu.Lock()
				running := f.task.running
				f.task.mu.Unlock()
				if !running {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("worker did not finish")
				}
				time.Sleep(time.Millisecond)
			}
			if f.matchCalls != 1 {
				t.Fatalf("catalog matching starved: %d", f.matchCalls)
			}
			if kind == "catalog_error" && f.scans != 1 {
				t.Fatal("catalog error starved orphan repair")
			}
			if kind == "orphan_error" && f.scans != 0 {
				t.Fatal("invalid snapshot scanned")
			}
		})
	}
}

func TestOrphanRepairRejectsNullPrivateState(t *testing.T) {
	f := newOrphanFixture(t)
	f.store.data = []byte("null")
	if _, err := f.task.repairUnassociatedFiles(t.Context()); err == nil {
		t.Fatal("null state accepted")
	}
	if f.scans != 0 {
		t.Fatal("scan using invalid private state")
	}
}

func TestOrphanRepairLimitsFailedScanAttempts(t *testing.T) {
	f := newOrphanFixture(t)
	f.scanStatus = 500
	for n := 0; n < 4; n++ {
		_, _ = f.task.repairUnassociatedFiles(t.Context())
		expireOrphanMarkers(t, f)
	}
	if f.scans != 3 {
		t.Fatalf("unbounded scan retries: %d", f.scans)
	}
}

func TestOrphanRepairRejectsMismatchedScanAdmission(t *testing.T) {
	for _, kind := range []string{"status", "mode", "library_id"} {
		t.Run(kind, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.alter = func(path string, b map[string]any) {
				if path == "/api/v2/scan" {
					b[kind] = "unexpected"
				}
			}
			summary, err := f.task.repairUnassociatedFiles(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if summary["admitted"] != 0 || summary["pending"] != 1 || summary["scan_failed"] != 1 {
				t.Fatalf("wrong admission accepted: %v", summary)
			}
		})
	}
}

func TestOrphanRepairRestrictsEnabledStashMovieLibraries(t *testing.T) {
	for _, kind := range []string{"disabled", "series", "wrong_provider", "provider_disabled", "wrong_file_library", "no_paths", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.alter = func(path string, b map[string]any) {
				if path == "/api/v2/libraries" {
					row := b["items"].([]any)[0].(map[string]any)
					switch kind {
					case "disabled":
						row["enabled"] = false
					case "series":
						row["type"] = "tvshows"
					case "no_paths":
						row["paths"] = []string{}
					case "mixed":
						row["type"] = "mixed"
					}
				}
				if path == "/api/v2/libraries/7/providers" {
					row := b["levels"].([]any)[0].(map[string]any)["entries"].([]any)[0].(map[string]any)
					if kind == "wrong_provider" {
						row["provider_slug"] = "other"
					}
					if kind == "provider_disabled" {
						row["enabled"] = false
					}
				}
				if path == "/api/v2/admin/unmatched" && kind == "wrong_file_library" {
					b["items"].([]any)[0].(map[string]any)["media_folder_id"] = "8"
				}
			}
			_, err := f.task.repairUnassociatedFiles(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if kind == "mixed" {
				want = 1
			}
			if f.scans != want {
				t.Fatalf("scope %s scans=%d", kind, f.scans)
			}
		})
	}
}

func TestOrphanRepairReadsTerminalInventoryAndQueuePages(t *testing.T) {
	f := newOrphanFixture(t)
	inv, queue := 0, 0
	f.alter = func(path string, b map[string]any) {
		if path == "/api/v2/admin/unmatched" {
			inv++
			if inv == 1 {
				row := b["items"].([]any)[0].(map[string]any)
				row["id"] = "12"
				row["file_path"] = filepath.Join(f.root, "other.mp4")
				b["page"] = map[string]any{"has_more": true, "next_cursor": "inventory-next"}
			}
		}
		if strings.HasSuffix(path, "metadata-match-queue") {
			queue++
			if queue == 1 {
				row := b["movies"].([]any)[0].(map[string]any)
				row["media_file_id"] = "12"
				row["file_path"] = filepath.Join(f.root, "other.mp4")
				b["page"] = map[string]any{"has_more": true, "next_cursor": "queue-next"}
			}
		}
	}
	summary, err := f.task.repairUnassociatedFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if inv != 2 || queue != 2 || f.scans != 1 || summary["admitted"] != 1 {
		t.Fatalf("pagination not completed: inventory=%d queue=%d summary=%v", inv, queue, summary)
	}
}

func TestOrphanRepairAdmitsOnlyExactFileAndPreservesVideo(t *testing.T) {
	f := newOrphanFixture(t)
	before, err := os.Stat(f.video)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := f.task.repairUnassociatedFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if f.scans != 1 || summary["admitted"] != 1 || summary["pending"] != 1 {
		t.Fatalf("scans=%d summary=%v", f.scans, summary)
	}
	raw, err := os.ReadFile(strings.TrimSuffix(f.video, ".mp4") + ".nfo")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		XMLName   xml.Name
		Title     string `xml:"title"`
		Year      int    `xml:"year"`
		Premiered string `xml:"premiered"`
		ID        struct {
			Type  string `xml:"type,attr"`
			Value string `xml:",chardata"`
		} `xml:"uniqueid"`
	}
	if err = xml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.XMLName.Local != "movie" || doc.Title != "EXAMPLE-001" || doc.Year != 2024 || doc.Premiered != "2024-02-29" || doc.ID.Type != "stash" || doc.ID.Value != "stash:42" {
		t.Fatalf("unexpected NFO: %s", raw)
	}
	after, err := os.Stat(f.video)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(f.video)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || string(bytes) != "synthetic video bytes" {
		t.Fatal("video was modified")
	}
	if len(f.store.data) == 0 {
		t.Fatal("no durable ownership/retry marker")
	}
}

// Silo backs a rejected file off for an hour, so a file-mode scan alone does
// not re-read the new sidecar. A still-orphaned admission must force a bounded
// match-queue retry, never immediately and never more than three times.
func TestOrphanRepairForcesBoundedQueueRetryUntilLinked(t *testing.T) {
	f := newOrphanFixture(t)
	if _, err := f.task.repairUnassociatedFiles(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.task.repairUnassociatedFiles(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.retries != 0 {
		t.Fatal("queue retried before the scan had time to sync")
	}
	for i := 0; i < 5; i++ {
		expireOrphanMarkers(t, f)
		if _, err := f.task.repairUnassociatedFiles(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if f.retries != 3 || f.scans != 1 {
		t.Fatalf("retries=%d scans=%d, want 3 bounded retries and one scan", f.retries, f.scans)
	}
}

// Markers written by earlier versions have no admission time; those stuck
// files must be retried too.
func TestOrphanRepairRetriesLegacyAdmittedMarkers(t *testing.T) {
	f := newOrphanFixture(t)
	if _, err := f.task.repairUnassociatedFiles(t.Context()); err != nil {
		t.Fatal(err)
	}
	markers := map[string]map[string]any{}
	if err := json.Unmarshal(f.store.data, &markers); err != nil {
		t.Fatal(err)
	}
	for k, m := range markers {
		delete(m, "admitted_at")
		delete(m, "queue_retry_after")
		markers[k] = m
	}
	f.store.data, _ = json.Marshal(markers)
	summary, err := f.task.repairUnassociatedFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if f.retries != 1 || summary["queue_retried"] != 1 {
		t.Fatalf("legacy admitted marker not retried: retries=%d summary=%v", f.retries, summary)
	}
}
