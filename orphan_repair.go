package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	hostruntime "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
)

type orphanFile struct {
	ID      string `json:"id"`
	Library string `json:"media_folder_id"`
	Path    string `json:"file_path"`
	Size    int64  `json:"file_size"`
}
type orphanCandidate struct {
	Title   string            `json:"title"`
	Year    int               `json:"year"`
	IDs     map[string]string `json:"provider_ids"`
	Sources []string          `json:"sources"`
	Score   float64           `json:"score"`
	Reasons []string          `json:"reasons"`
}
type orphanQueueRow struct {
	ID       string  `json:"media_file_id"`
	Library  string  `json:"library_id"`
	Path     string  `json:"file_path"`
	State    string  `json:"state"`
	Kind     string  `json:"failure_kind"`
	ParkedAt *string `json:"parked_at"`
	Detail   struct {
		Decision struct {
			Outcome    string            `json:"outcome"`
			Count      int               `json:"candidate_count"`
			Threshold  float64           `json:"threshold"`
			Candidates []orphanCandidate `json:"top_candidates"`
		} `json:"decision"`
	} `json:"failure_detail"`
}
type orphanMarker struct {
	Hash     string    `json:"nfo_sha256"`
	Scene    string    `json:"scene"`
	Next     time.Time `json:"retry_after"`
	Admitted bool      `json:"admitted"`
	Video    string    `json:"video_identity"`
	NFO      string    `json:"nfo_identity"`
	Attempts int       `json:"attempts"`
	// Silo backs rejected files off, so a scan alone may never re-read the
	// sidecar. These bound the forced match-queue retries after admission.
	AdmittedAt   time.Time `json:"admitted_at"`
	QueueRetries int       `json:"queue_retries"`
	QueueRetryAt time.Time `json:"queue_retry_after"`
}

const (
	orphanQueueRetryDelay = time.Minute
	orphanQueueRetryEvery = 10 * time.Minute
	orphanQueueRetryMax   = 3
)

type orphanNFO struct {
	XMLName   xml.Name    `xml:"movie"`
	Title     string      `xml:"title"`
	Year      int         `xml:"year"`
	Premiered string      `xml:"premiered"`
	ID        orphanNFOID `xml:"uniqueid"`
}
type orphanNFOID struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type orphanPage struct {
	More *bool  `json:"has_more"`
	Next string `json:"next_cursor"`
}

// An explicit terminal page is mandatory; absence is never inferred from a
// truncated/malformed page. Read the whole bounded snapshot before any writes.
func orphanNext(page *orphanPage, count, limit int, seen map[string]bool) (string, error) {
	if page == nil || page.More == nil || count > limit || (*page.More && (count == 0 || page.Next == "" || seen[page.Next])) || (!*page.More && page.Next != "") {
		return "", fmt.Errorf("orphan repair: incomplete pagination")
	}
	seen[page.Next] = true
	return page.Next, nil
}

func orphanInventory(ctx context.Context, base, key string) ([]orphanFile, error) {
	out := []orphanFile{}
	cursor := ""
	cursors := map[string]bool{}
	ids := map[string]bool{}
	paths := map[string]bool{}
	for n := 0; n < 50; n++ {
		path := "/api/v2/admin/unmatched?limit=200"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var data struct {
			Items []orphanFile `json:"items"`
			Page  *orphanPage  `json:"page"`
		}
		if err := siloRequest(ctx, base, key, "GET", path, nil, &data); err != nil {
			return nil, err
		}
		if data.Items == nil {
			return nil, fmt.Errorf("orphan repair: missing inventory rows")
		}
		for _, row := range data.Items {
			if row.ID == "" || row.Library == "" || row.Path == "" || ids[row.ID] || paths[row.Path] {
				return nil, fmt.Errorf("orphan repair: invalid or duplicate inventory row")
			}
			ids[row.ID] = true
			paths[row.Path] = true
			out = append(out, row)
		}
		next, err := orphanNext(data.Page, len(data.Items), 200, cursors)
		if err != nil {
			return nil, err
		}
		if next == "" {
			return out, nil
		}
		cursor = next
	}
	return nil, fmt.Errorf("orphan repair: inventory exceeds bounded snapshot")
}

func orphanQueue(ctx context.Context, base, key, library string) ([]orphanQueueRow, error) {
	out := []orphanQueueRow{}
	cursor := ""
	cursors := map[string]bool{}
	ids := map[string]bool{}
	paths := map[string]bool{}
	series := map[string]bool{}
	for n := 0; n < 50; n++ {
		path := "/api/v2/libraries/" + url.PathEscape(library) + "/metadata-match-queue?limit=50"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var data struct {
			Library string           `json:"library_id"`
			Movies  []orphanQueueRow `json:"movies"`
			Raw     []orphanQueueRow `json:"raw_files"`
			Series  []struct {
				Library string `json:"library_id"`
				Path    string `json:"observed_root_path"`
			} `json:"series"`
			Page *orphanPage `json:"page"`
		}
		if err := siloRequest(ctx, base, key, "GET", path, nil, &data); err != nil {
			return nil, err
		}
		if data.Library != library || data.Movies == nil || data.Series == nil || data.Raw == nil || len(data.Movies) > 50 || len(data.Series) > 50 || len(data.Raw) > 50 {
			return nil, fmt.Errorf("orphan repair: incomplete queue snapshot")
		}
		for _, rows := range [][]orphanQueueRow{data.Movies, data.Raw} {
			for _, row := range rows {
				if row.Library != library || row.ID == "" || row.Path == "" || ids[row.ID] || paths[row.Path] {
					return nil, fmt.Errorf("orphan repair: invalid or duplicate queue row")
				}
				ids[row.ID] = true
				paths[row.Path] = true
			}
		}
		for _, row := range data.Series {
			if row.Library != library || row.Path == "" || series[row.Path] {
				return nil, fmt.Errorf("orphan repair: invalid or duplicate series row")
			}
			series[row.Path] = true
		}
		out = append(out, data.Movies...)
		count := max(len(data.Movies), len(data.Raw), len(data.Series))
		next, err := orphanNext(data.Page, count, 50, cursors)
		if err != nil {
			return nil, err
		}
		if next == "" {
			return out, nil
		}
		cursor = next
	}
	return nil, fmt.Errorf("orphan repair: queue exceeds bounded snapshot")
}

var orphanFilenameYear = regexp.MustCompile(`(^|[^0-9])(19|20)[0-9]{2}([^0-9]|$)`)

// Unix inode/device identity is needed for durable ownership. Unsupported
// filesystems/platforms fail closed instead of trusting timestamps alone.
func orphanFingerprint(info os.FileInfo) string {
	if info == nil || !info.Mode().IsRegular() {
		return ""
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return ""
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return ""
	}
	dev, ino := value.FieldByName("Dev"), value.FieldByName("Ino")
	if !dev.IsValid() || !ino.IsValid() || !dev.CanUint() || !ino.CanUint() {
		return ""
	}
	return fmt.Sprintf("%d:%d:%d:%d", dev.Uint(), ino.Uint(), info.Size(), info.ModTime().UnixNano())
}

// A stem-based NFO must not also describe another video in the directory.
func orphanSidecarShared(base string, entries []os.DirEntry) bool {
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	for _, entry := range entries {
		name := entry.Name()
		ext := filepath.Ext(name)
		if name == base || entry.IsDir() || !strings.EqualFold(strings.TrimSuffix(name, ext), stem) {
			continue
		}
		switch strings.ToLower(ext) {
		case ".mp4", ".mkv", ".avi", ".mov", ".m4v", ".webm", ".wmv", ".mpg", ".mpeg", ".ts", ".m2ts", ".mts", ".vob", ".ogv", ".flv", ".3gp", ".3g2", ".asf", ".mxf", ".rm", ".rmvb", ".divx", ".m2v", ".ogm", ".iso", ".strm":
			return true
		}
	}
	return false
}

func orphanPublishNFO(file orphanFile, roots []string, data []byte, owned *orphanMarker, reserve func(string, string) error) (bool, error) {
	if !filepath.IsAbs(file.Path) || filepath.Clean(file.Path) != file.Path || orphanFilenameYear.MatchString(strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path))) {
		return false, nil
	}
	resolved, err := filepath.EvalSymlinks(file.Path)
	if err != nil || resolved != file.Path {
		return false, nil
	}
	for _, path := range roots {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			continue
		}
		rel, err := filepath.Rel(path, file.Path)
		if err != nil || !filepath.IsLocal(rel) || rel == "." {
			continue
		}
		root, err := os.OpenRoot(path)
		if err != nil {
			continue
		}
		defer root.Close()
		before, err := root.Lstat(rel)
		if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() != file.Size {
			return false, nil
		}
		dir, err := root.OpenRoot(filepath.Dir(rel))
		if err != nil {
			return false, nil
		}
		defer dir.Close()
		base := filepath.Base(file.Path)
		side := strings.TrimSuffix(base, filepath.Ext(base)) + ".nfo"
		// These names apply to the whole directory, not the verified video.
		if strings.EqualFold(side, "movie.nfo") || strings.EqualFold(side, "tvshow.nfo") {
			return false, nil
		}
		directory, err := dir.Open(".")
		if err != nil {
			return false, err
		}
		entries, err := directory.ReadDir(-1)
		directory.Close()
		if err != nil {
			return false, err
		}
		if orphanSidecarShared(base, entries) {
			return false, nil
		}
		matches := 0
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), side) {
				matches++
			}
		}
		if matches > 1 {
			return false, nil
		}
		for _, entry := range entries {
			if !strings.EqualFold(entry.Name(), side) {
				continue
			}
			if owned == nil || entry.Name() != side || orphanFingerprint(before) == "" || owned.Video != orphanFingerprint(before) {
				return false, nil
			}
			info, e := dir.Lstat(side)
			if e != nil || orphanFingerprint(info) == "" || owned.NFO != orphanFingerprint(info) {
				return false, nil
			}
			raw, e := dir.ReadFile(side)
			if e != nil {
				return false, e
			}
			digest := sha256.Sum256(raw)
			expected := sha256.Sum256(data)
			after, e := dir.Lstat(side)
			if e != nil || orphanFingerprint(after) != owned.NFO || fmt.Sprintf("%x", digest) != owned.Hash || digest != expected {
				return false, nil
			}
			return true, nil
		}
		if owned != nil {
			return false, nil
		} // Respect removal of a previously generated NFO.
		unchanged := func() bool {
			after, e := root.Lstat(rel)
			real, e2 := filepath.EvalSymlinks(file.Path)
			return e == nil && e2 == nil && real == file.Path && after.Mode().IsRegular() && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
		}
		token := make([]byte, 16)
		if _, err = rand.Read(token); err != nil {
			return false, err
		}
		tmp := fmt.Sprintf(".stash-orphan-%x.tmp", token)
		out, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return false, err
		}
		defer dir.Remove(tmp)
		_, err = out.Write(data)
		if err == nil {
			err = out.Sync()
		}
		closeErr := out.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return false, err
		}
		if !unchanged() {
			return false, nil
		}
		info, err := dir.Lstat(tmp)
		if err != nil {
			return false, err
		}
		videoID, nfoID := orphanFingerprint(before), orphanFingerprint(info)
		if videoID == "" || nfoID == "" {
			return false, nil
		}
		if err = reserve(videoID, nfoID); err != nil {
			return false, err
		}
		if !unchanged() {
			return false, nil
		}
		// State persistence can block; recheck sidecar names immediately before
		// publication as well as the video identity.
		directory, err = dir.Open(".")
		if err != nil {
			return false, err
		}
		entries, err = directory.ReadDir(-1)
		directory.Close()
		if err != nil {
			return false, err
		}
		if orphanSidecarShared(base, entries) {
			return false, nil
		}
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), side) {
				return false, nil
			}
		}
		// Hard-link publication is atomic and refuses any preexisting target.
		if err = dir.Link(tmp, side); os.IsExist(err) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		if !unchanged() {
			return false, fmt.Errorf("orphan repair: video changed during publication")
		}
		return true, nil
	}
	return false, nil
}

// Recognize only the documented single-source, no-year confidence gate.
// Unknown diagnostics or other sources are not repair authorization.
func orphanYearlessFailure(row *orphanQueueRow) bool {
	d := row.Detail.Decision
	if row.State != "pending" || row.ParkedAt != nil || row.Kind != "candidate_rejected" || d.Outcome != "candidate_rejected" || d.Count != 1 || len(d.Candidates) != 1 {
		return false
	}
	c := d.Candidates[0]
	// The compact server diagnostic may omit the year. A linked JAVBeacon
	// identity is an alias from the same source, not independent corroboration.
	// The release year and Stash identity are read fresh and verified below.
	for key := range c.IDs {
		if key != "stash" && key != "javbeacon" {
			return false
		}
	}
	return d.Threshold == 55 && c.Score >= d.Threshold && c.Score < 70 && c.IDs["stash"] != "" && len(c.Sources) == 1 && c.Sources[0] == "stash" && slices.Contains(c.Reasons, "exact_title") && slices.Contains(c.Reasons, "provider_sources") && !slices.Contains(c.Reasons, "exact_year")
}

func orphanExactPath(row *scene, path string) bool {
	n := 0
	for _, file := range row.Files {
		if file.Path == path {
			n++
		}
	}
	return n == 1
}

// This repairs only orphan files; normal catalog matching remains independent.
// Admission is not proof of a completed content association.
func (s *scheduledTaskServer) repairUnassociatedFiles(ctx context.Context) (map[string]any, error) {
	s.runtime.mu.RLock()
	base, key, enabled := s.runtime.siloBase, s.runtime.siloKey, s.runtime.orphanRepairEnabled
	s.runtime.mu.RUnlock()
	if !enabled {
		return map[string]any{"status": "disabled", "admitted": 0, "pending": 0}, nil
	}
	stash := s.runtime.stash()
	if base == "" || key == "" || !stash.configured() {
		return nil, fmt.Errorf("orphan repair: connections not configured")
	}
	store := s.orphanStore
	if store == nil {
		if host := hostruntime.Host(); host != nil {
			store = host
		}
	}
	if store == nil {
		return nil, fmt.Errorf("orphan repair: private instance state unavailable")
	}
	sum := sha256.Sum256([]byte(base + "\x00" + stash.base))
	stateKey := fmt.Sprintf("stash-orphan-repair-v1-%x", sum[:16])
	markers := map[string]orphanMarker{}
	raw, found, err := store.ReadInstanceState(ctx, stateKey)
	if err != nil {
		return nil, err
	}
	if found {
		if err = json.Unmarshal(raw, &markers); err != nil {
			return nil, err
		}
		if markers == nil || len(markers) > 10000 {
			return nil, fmt.Errorf("orphan repair: invalid private state")
		}
	}
	save := func() error {
		data, e := json.Marshal(markers)
		if e != nil {
			return e
		}
		return store.WriteInstanceState(ctx, stateKey, data)
	}
	libs, err := provider.NewSiloClient(base, key).ListJAVBeaconMovieLibraries(ctx)
	if err != nil {
		return nil, err
	}
	inventory, err := orphanInventory(ctx, base, key)
	if err != nil {
		return nil, err
	}
	queues := map[string][]orphanQueueRow{}
	for _, lib := range libs {
		if _, exists := queues[lib.ID]; exists {
			return nil, fmt.Errorf("orphan repair: duplicate library")
		}
		rows, e := orphanQueue(ctx, base, key, lib.ID)
		if e != nil {
			return nil, e
		}
		queues[lib.ID] = rows
	}
	admitted, pending, scanFailed, attempted, queueRetried := 0, 0, 0, 0, 0
	retriedLibrary := map[string]bool{}
	for _, lib := range libs {
		queue := queues[lib.ID]
		for _, file := range inventory {
			if file.Library != lib.ID {
				continue
			}
			digestKey := sha256.Sum256([]byte(file.Library + "\x00" + file.ID + "\x00" + file.Path))
			markerKey := fmt.Sprintf("%x", digestKey)
			marker, existing := markers[markerKey]
			if existing {
				pending++
				// The file is still unassociated, so admission did not complete.
				// Force Silo to re-read the sidecar instead of waiting out its
				// match backoff, at most once per library per cycle.
				if marker.Admitted {
					now := time.Now()
					if !retriedLibrary[lib.ID] && marker.QueueRetries < orphanQueueRetryMax && now.Sub(marker.AdmittedAt) >= orphanQueueRetryDelay && !marker.QueueRetryAt.After(now) {
						var response struct {
							Status string `json:"status"`
						}
						if e := siloRequest(ctx, base, key, http.MethodPost, "/api/v2/libraries/"+url.PathEscape(lib.ID)+"/metadata-match-queue/retry", map[string]any{}, &response); e == nil && response.Status == "queued" {
							retriedLibrary[lib.ID] = true
							queueRetried++
						}
						marker.QueueRetries++
						marker.QueueRetryAt = now.Add(orphanQueueRetryEvery)
						markers[markerKey] = marker
						if err = save(); err != nil {
							return nil, err
						}
					}
					continue
				}
				if marker.Next.After(time.Now()) || marker.Attempts >= 3 {
					continue
				}
			}
			if attempted >= 10 || (!existing && len(markers) >= 10000) {
				continue
			}
			var diagnostic *orphanQueueRow
			for i := range queue {
				if queue[i].ID == file.ID && queue[i].Path == file.Path {
					diagnostic = &queue[i]
				}
			}
			if diagnostic == nil || diagnostic.Library != lib.ID || !orphanYearlessFailure(diagnostic) {
				continue
			}
			var lookup struct {
				Scenes struct {
					Count *int    `json:"count"`
					Items []scene `json:"scenes"`
				} `json:"findScenes"`
			}
			err = stash.graphql(ctx, `query($path:StringCriterionInput!){findScenes(scene_filter:{path:$path},filter:{per_page:200}){count scenes{id files{path}}}}`, map[string]any{"path": map[string]any{"value": file.Path, "modifier": "EQUALS"}}, &lookup)
			if err != nil {
				return nil, err
			}
			if lookup.Scenes.Count == nil || *lookup.Scenes.Count != len(lookup.Scenes.Items) || len(lookup.Scenes.Items) != 1 || lookup.Scenes.Items[0].ID == "" || !orphanExactPath(&lookup.Scenes.Items[0], file.Path) {
				continue
			}
			var fresh struct {
				Scene *scene `json:"findScene"`
			}
			err = stash.graphql(ctx, `query($id:ID!){findScene(id:$id){id title code date files{path}}}`, map[string]any{"id": lookup.Scenes.Items[0].ID}, &fresh)
			if err != nil {
				return nil, err
			}
			row := fresh.Scene
			if row == nil || row.ID != lookup.Scenes.Items[0].ID || !orphanExactPath(row, file.Path) {
				continue
			}
			date, err := time.Parse("2006-01-02", row.Date)
			if err != nil || date.Year() < 1900 || date.Year() > time.Now().Year()+1 {
				continue
			}
			candidate := diagnostic.Detail.Decision.Candidates[0]
			if (candidate.Year != 0 && candidate.Year != date.Year()) || (candidate.IDs["stash"] != row.ID && candidate.IDs["stash"] != "stash:"+row.ID) {
				continue
			}
			title := row.Code
			if title == "" {
				title = row.Title
			}
			if strings.TrimSpace(title) == "" {
				continue
			}
			nfo, err := xml.Marshal(orphanNFO{Title: title, Year: date.Year(), Premiered: row.Date, ID: orphanNFOID{Type: "stash", Value: "stash:" + row.ID}})
			if err != nil {
				return nil, err
			}
			var owned *orphanMarker
			if existing {
				owned = &marker
			}
			digest := sha256.Sum256(nfo)
			reserve := func(videoID, nfoID string) error {
				marker = orphanMarker{Hash: fmt.Sprintf("%x", digest), Scene: row.ID, Video: videoID, NFO: nfoID, Attempts: marker.Attempts + 1, Next: time.Now().Add(6 * time.Hour)}
				markers[markerKey] = marker
				return save()
			}
			published, err := orphanPublishNFO(file, lib.Paths, nfo, owned, reserve)
			if err != nil {
				return nil, err
			}
			if !published {
				continue
			}
			if existing {
				if err = reserve(marker.Video, marker.NFO); err != nil {
					return nil, err
				}
			} else {
				pending++
			}
			// Revalidate the exact video and owned NFO after durable state I/O.
			valid, err := orphanPublishNFO(file, lib.Paths, nfo, &marker, nil)
			if err != nil {
				return nil, err
			}
			if !valid {
				continue
			}
			attempted++
			var response struct {
				Status  string `json:"status"`
				Mode    string `json:"mode"`
				Library string `json:"library_id"`
			}
			err = siloRequest(ctx, base, key, http.MethodPost, "/api/v2/scan", map[string]string{"library_id": lib.ID, "path": file.Path}, &response)
			if err != nil || response.Status != "accepted" || response.Mode != "file" || response.Library != lib.ID {
				scanFailed++
				continue // Uncertain admission is retried only after the durable cooldown.
			}
			marker.Admitted = true
			marker.AdmittedAt = time.Now()
			markers[markerKey] = marker
			if err = save(); err != nil {
				return nil, err
			}
			admitted++
		}
	}
	return map[string]any{"status": "pending", "admitted": admitted, "pending": pending, "scan_failed": scanFailed, "queue_retried": queueRetried}, nil
}
