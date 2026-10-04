package main

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	provider "github.com/Net005/silo-plugin-metadata-stash/internal/legacyprovider"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

type personJob struct{ name, id string }

type personQueue struct {
	once sync.Once
	mu   sync.Mutex
	seen map[string]time.Time
	jobs chan personJob
}

func performerImagePath(id string) string {
	if id == "" {
		return ""
	}
	return "stash://backend/api/v1/integrations/performers/" + url.PathEscape(id) + "/image"
}

func (s *metadataServer) queuePeople(row scene) {
	if s.runtime.legacy == nil || !s.runtime.legacy.Provider().Configured() {
		return
	}
	s.runtime.mu.RLock()
	siloURL, siloKey := s.runtime.siloBase, s.runtime.siloKey
	s.runtime.mu.RUnlock()
	if siloURL == "" || siloKey == "" {
		return
	}
	s.people.once.Do(func() {
		s.people.jobs = make(chan personJob, 1024)
		s.people.seen = map[string]time.Time{}
		for range 4 {
			go s.personWorker()
		}
	})
	for _, p := range row.Performers {
		if p.ID == "" || p.Name == "" {
			continue
		}
		s.people.mu.Lock()
		last := s.people.seen[p.ID]
		if time.Since(last) < 15*time.Minute {
			s.people.mu.Unlock()
			continue
		}
		s.people.seen[p.ID] = time.Now()
		s.people.mu.Unlock()
		select {
		case s.people.jobs <- personJob{name: p.Name, id: p.ID}:
		default:
			s.people.mu.Lock()
			delete(s.people.seen, p.ID)
			s.people.mu.Unlock()
		}
	}
}

func (s *metadataServer) personWorker() {
	for job := range s.people.jobs {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		bio, err := s.runtime.legacy.Provider().GetPerformerBio(ctx, job.id)
		if err == nil && bio != nil {
			s.runtime.mu.RLock()
			siloURL, siloKey := s.runtime.siloBase, s.runtime.siloKey
			s.runtime.mu.RUnlock()
			homepage := s.runtime.legacy.Provider().PublicURL("/api/v1/integrations/performers/" + url.PathEscape(job.id) + "/stash")
			client := provider.NewSiloClient(siloURL, siloKey)
		retries:
			for _, delay := range []time.Duration{3 * time.Second, 5 * time.Second, 15 * time.Second} {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					break retries
				}
				err = client.EnrichPerson(ctx, job.name, bio.Birthdate, homepage, bio.ProfileBio())
				if err == nil {
					break
				}
			}
		}
		if err != nil {
			s.people.mu.Lock()
			delete(s.people.seen, job.id)
			s.people.mu.Unlock()
		}
		cancel()
	}
}

func stashPersonID(ids *structpb.Struct) string {
	if ids == nil {
		return ""
	}
	values := ids.AsMap()
	for _, key := range []string{"stash", "plex", "plex_guid"} {
		value := text(values[key])
		if id, ok := strings.CutPrefix(value, "stash:"); ok && id != "" {
			return id
		}
		if key == "stash" && value != "" && !strings.Contains(value, ":") {
			return value
		}
	}
	return ""
}

func (s *metadataServer) GetPersonDetail(ctx context.Context, req *pluginv1.GetPersonDetailRequest) (*pluginv1.GetPersonDetailResponse, error) {
	id := stashPersonID(req.GetProviderIds())
	if id == "" || s.runtime.legacy == nil || !s.runtime.legacy.Provider().Configured() {
		return &pluginv1.GetPersonDetailResponse{}, nil
	}
	bio, err := s.runtime.legacy.Provider().GetPerformerBio(ctx, id)
	if err != nil {
		return nil, err
	}
	ids, _ := structpb.NewStruct(map[string]any{"stash": id, "plex": "stash:" + id})
	return &pluginv1.GetPersonDetailResponse{Person: &pluginv1.PersonDetailRecord{Name: bio.Name, Bio: bio.ProfileBio(), BirthDate: bio.Birthdate, DeathDate: bio.DeathDate, Homepage: s.runtime.legacy.Provider().PublicURL("/api/v1/integrations/performers/" + url.PathEscape(id) + "/stash"), PhotoPath: performerImagePath(id), ProviderIds: ids}}, nil
}
