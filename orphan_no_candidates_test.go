package main

import (
	"encoding/xml"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
)

func noCandidatesDiagnostic(path string, body map[string]any) {
	if path != "/api/v2/libraries/7/metadata-match-queue" {
		return
	}
	row := body["movies"].([]any)[0].(map[string]any)
	row["failure_kind"] = "no_candidates"
	row["failure_detail"] = map[string]any{"decision": map[string]any{"outcome": "no_candidates", "candidate_count": 0, "threshold": 55, "top_candidates": []any{}}}
}

func runOrphanWorker(t *testing.T, f *orphanFixture) {
	t.Helper()
	if !f.task.start() {
		t.Fatal("worker not started")
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		f.task.mu.Lock()
		running := f.task.running
		f.task.mu.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestOrphanNoCandidatesWorkerKeepsNativeBackoffAfterRestart(t *testing.T) {
	f := newOrphanFixture(t)
	f.alter = func(path string, body map[string]any) {
		noCandidatesDiagnostic(path, body)
		if path == "/api/v2/libraries/7/metadata-match-queue" {
			body["movies"] = append(body["movies"].([]any), map[string]any{"media_file_id": "99", "library_id": "7", "file_path": "/media/unrelated.mp4", "state": "parked", "parked_at": "2026-01-01T00:00:00Z"})
		}
	}
	runOrphanWorker(t, f)
	if f.scans != 1 {
		t.Fatalf("initial recovery missing: scans=%d", f.scans)
	}
	expireOrphanMarkers(t, f)
	f.task = &scheduledTaskServer{runtime: f.task.runtime, orphanStore: f.store, log: hclog.NewNullLogger()}
	runOrphanWorker(t, f)
	if f.retries != 0 || f.scans != 1 {
		t.Fatalf("native backoff bypassed after restart: library retries=%d scans=%d", f.retries, f.scans)
	}
}

func TestOrphanNoCandidatesWorkerSkipsUnsafeRecovery(t *testing.T) {
	for _, kind := range []string{"absent", "ambiguous", "fresh_path_changed", "invalid_date", "missing_code", "blank_code", "parked", "parked_at", "wrong_outcome", "wrong_threshold", "nonzero_count", "nonempty_candidates", "missing_count", "existing_nfo", "wrong_size"} {
		t.Run(kind, func(t *testing.T) {
			f := newOrphanFixture(t)
			side := strings.TrimSuffix(f.video, ".mp4") + ".nfo"
			if kind == "existing_nfo" {
				if err := os.WriteFile(side, []byte("user metadata"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f.alter = func(path string, b map[string]any) {
				noCandidatesDiagnostic(path, b)
				if path == "/api/v2/libraries/7/metadata-match-queue" {
					row := b["movies"].([]any)[0].(map[string]any)
					d := row["failure_detail"].(map[string]any)["decision"].(map[string]any)
					switch kind {
					case "parked":
						row["state"] = "parked"
					case "parked_at":
						row["parked_at"] = "2026-01-01T00:00:00Z"
					case "wrong_outcome":
						d["outcome"] = "candidate_rejected"
					case "wrong_threshold":
						d["threshold"] = 54
					case "nonzero_count":
						d["candidate_count"] = 1
					case "nonempty_candidates":
						d["top_candidates"] = []any{map[string]any{"title": "unexpected"}}
					case "missing_count":
						delete(d, "candidate_count")
					}
				}
				if path == "/api/v2/admin/unmatched" && kind == "wrong_size" {
					b["items"].([]any)[0].(map[string]any)["file_size"] = 99
				}
				if path == "/graphql" {
					data := b["data"].(map[string]any)
					if found, ok := data["findScenes"].(map[string]any); ok {
						if kind == "absent" {
							found["count"] = 0
							found["scenes"] = []any{}
						}
						if kind == "ambiguous" {
							found["count"] = 2
							found["scenes"] = append(found["scenes"].([]any), map[string]any{"id": "43", "files": []any{map[string]any{"path": f.video}}})
						}
					}
					if row, ok := data["findScene"].(map[string]any); ok {
						switch kind {
						case "fresh_path_changed":
							row["files"] = []any{map[string]any{"path": "/media/elsewhere.mp4"}}
						case "invalid_date":
							row["date"] = "2024-02-30"
						case "missing_code":
							delete(row, "code")
						case "blank_code":
							row["code"] = "  "
						}
					}
				}
			}
			runOrphanWorker(t, f)
			if f.scans != 0 || f.retries != 0 || len(f.store.data) != 0 {
				t.Fatalf("unsafe recovery mutated state: scans=%d retries=%d state=%d", f.scans, f.retries, len(f.store.data))
			}
			raw, err := os.ReadFile(side)
			if kind == "existing_nfo" {
				if err != nil || string(raw) != "user metadata" {
					t.Fatal("user sidecar changed")
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("unsafe recovery wrote a sidecar")
			}
		})
	}
}

func TestOrphanNoCandidatesWorkerRecoversFreshExactScene(t *testing.T) {
	f := newOrphanFixture(t)
	f.alter = noCandidatesDiagnostic
	runOrphanWorker(t, f)
	if f.scans != 1 || f.matchCalls != 1 {
		t.Fatalf("fresh exact scene not admitted: scans=%d catalog=%d", f.scans, f.matchCalls)
	}
	raw, err := os.ReadFile(strings.TrimSuffix(f.video, ".mp4") + ".nfo")
	if err != nil {
		t.Fatal(err)
	}
	var doc orphanNFO
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Title != "EXAMPLE-001" || doc.Year != 2024 || doc.Premiered != "2024-02-29" || doc.ID.Value != "stash:42" {
		t.Fatalf("unexpected sidecar: %s", raw)
	}
	if f.retries != 0 {
		t.Fatal("new recovery retried the library queue")
	}
}

// Silo omits top_candidates when empty; the live no-candidates diagnostic
// contains an explicit zero count but no top_candidates field.
func TestOrphanNoCandidatesWorkerAcceptsCompactDiagnostic(t *testing.T) {
	f := newOrphanFixture(t)
	f.alter = func(path string, b map[string]any) {
		noCandidatesDiagnostic(path, b)
		if path == "/api/v2/libraries/7/metadata-match-queue" {
			row := b["movies"].([]any)[0].(map[string]any)
			delete(row["failure_detail"].(map[string]any)["decision"].(map[string]any), "top_candidates")
		}
	}
	runOrphanWorker(t, f)
	if f.scans != 1 {
		t.Fatalf("compact zero-candidate diagnostic ignored: scans=%d", f.scans)
	}
}
