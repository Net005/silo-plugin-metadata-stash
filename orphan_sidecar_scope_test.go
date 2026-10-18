package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrphanRepairRejectsSharedSidecarScope(t *testing.T) {
	for _, phase := range []string{"initial", "after_reserve", "owned_retry", "retry_after_reserve"} {
		for _, ext := range []string{".mkv", ".MP4", ".avi", ".mov", ".m4v", ".webm", ".wmv", ".mpg", ".mpeg", ".ts", ".m2ts", ".mts", ".vob", ".ogv", ".flv", ".3gp", ".3g2", ".asf", ".mxf", ".rm", ".rmvb", ".divx", ".m2v", ".ogm", ".iso", ".strm"} {
			t.Run(phase+ext, func(t *testing.T) {
				f := newOrphanFixture(t)
				side := strings.TrimSuffix(f.video, filepath.Ext(f.video)) + ".nfo"
				var before os.FileInfo
				var original []byte
				if strings.HasPrefix(phase, "owned_") || strings.HasPrefix(phase, "retry_") {
					f.scanStatus = 500
					if _, err := f.task.repairUnassociatedFiles(t.Context()); err != nil {
						t.Fatal(err)
					}
					if f.scans != 1 {
						t.Fatalf("fixture did not attempt initial scan: %d", f.scans)
					}
					expireOrphanMarkers(t, f)
					f.scanStatus = 0
					var err error
					before, err = os.Lstat(side)
					if err != nil {
						t.Fatal(err)
					}
					original, err = os.ReadFile(side)
					if err != nil {
						t.Fatal(err)
					}
				}
				sibling := filepath.Join(f.root, "example-001"+ext)
				addSibling := func() {
					if err := os.WriteFile(sibling, []byte("other video bytes"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if strings.HasSuffix(phase, "after_reserve") {
					f.store.afterWrite = addSibling
				} else {
					addSibling()
				}
				scans := f.scans
				summary, err := f.task.repairUnassociatedFiles(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if f.scans != scans || summary["admitted"] != 0 {
					t.Errorf("shared sidecar admitted: scans=%d (before=%d) summary=%v", f.scans, scans, summary)
				}
				after, err := os.Lstat(side)
				if before == nil {
					if !os.IsNotExist(err) {
						t.Errorf("shared NFO published: err=%v", err)
					}
				} else {
					raw, readErr := os.ReadFile(side)
					if err != nil || readErr != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || string(raw) != string(original) {
						t.Error("existing owned NFO changed or removed")
					}
				}
				if raw, err := os.ReadFile(sibling); err != nil || string(raw) != "other video bytes" {
					t.Error("sibling video changed or removed")
				}
			})
		}
	}
}

func TestOrphanRepairAllowsNonVideoSidecars(t *testing.T) {
	f := newOrphanFixture(t)
	for _, ext := range []string{".jpg", ".srt"} {
		if err := os.WriteFile(filepath.Join(f.root, "example-001"+ext), []byte("user sidecar"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := f.task.repairUnassociatedFiles(t.Context())
	if err != nil || f.scans != 1 || summary["admitted"] != 1 {
		t.Fatalf("non-video sidecars blocked repair: scans=%d summary=%v err=%v", f.scans, summary, err)
	}
	for _, ext := range []string{".jpg", ".srt"} {
		if raw, err := os.ReadFile(filepath.Join(f.root, "example-001"+ext)); err != nil || string(raw) != "user sidecar" {
			t.Error("user sidecar changed or removed")
		}
	}
}

func TestOrphanRepairRejectsDirectoryWideSidecarNames(t *testing.T) {
	for _, name := range []string{"movie.mp4", "MoViE.MP4", "tvshow.mp4", "TVSHOW.MKV"} {
		t.Run(name, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.video = filepath.Join(f.root, name)
			if err := os.WriteFile(f.video, []byte("synthetic video bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			summary, err := f.task.repairUnassociatedFiles(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if f.scans != 0 || summary["admitted"] != 0 {
				t.Errorf("directory-wide sidecar admitted: scans=%d summary=%v", f.scans, summary)
			}
			side := strings.TrimSuffix(f.video, filepath.Ext(f.video)) + ".nfo"
			if _, err := os.Lstat(side); !os.IsNotExist(err) {
				t.Errorf("directory-wide NFO published: %s (err=%v)", filepath.Base(side), err)
			}
		})
	}
}
