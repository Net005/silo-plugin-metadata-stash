package recommendations

import (
	"sort"
	"time"
)

type Evaluation struct {
	Status             string `json:"status"`
	HeldOutDays        int    `json:"held_out_days"`
	Targets            int    `json:"targets"`
	Picks              int    `json:"picks"`
	Hits               int    `json:"hits"`
	StudioBaselineHits int    `json:"studio_baseline_hits"`
	Note               string `json:"note"`
}

// Evaluate holds out the last 90 days of explicit O events. Current ratings and
// favourites have no edit timestamps, so neither is used in this retrospective
// test. It measures event retrieval, not a claim of subjective accuracy.
func Evaluate(rows []Scene, o Options, now time.Time) Evaluation {
	e := Evaluation{Status: "insufficient_feedback", HeldOutDays: 90, Note: "Retrospective explicit-O retrieval; current ratings/favourites excluded to avoid temporal leakage. Not a measured enjoyment probability."}
	cutoff := now.AddDate(0, 0, -90)
	train := []Scene{}
	targets := map[string]bool{}
	studio := map[string]int{}
	for _, s := range rows {
		s.Performers = append([]Entity(nil), s.Performers...)
		s.Rating = nil
		s.SiloPlayed = false
		for i := range s.Performers {
			s.Performers[i].Favorite = false
		}
		plays := []string{}
		events := []string{}
		future := false
		for _, v := range s.PlayHistory {
			t := eventTime(v)
			if !t.IsZero() && t.Before(cutoff) {
				plays = append(plays, v)
			}
		}
		for _, v := range s.OHistory {
			t := eventTime(v)
			if t.IsZero() {
				continue
			}
			if t.Before(cutoff) {
				events = append(events, v)
			} else if !t.After(now) {
				future = true
			}
		}
		s.PlayHistory = plays
		s.OHistory = events
		s.Plays = len(plays)
		s.O = len(events)
		s.LastPlayed = ""
		if len(plays) > 0 {
			s.LastPlayed = plays[len(plays)-1]
		}
		if future && s.Plays == 0 && s.O == 0 {
			targets[s.ID] = true
		}
		if s.O > 0 && s.Studio != nil {
			studio[s.Studio.ID]++
		}
		train = append(train, s)
	}
	e.Targets = len(targets)
	if e.Targets < 5 {
		return e
	}
	o.Kinds = []string{"for-you"}
	o.Retain = 0
	o.CrossLibrary = false
	r := Build("evaluation", train, nil, o, nil, nil, nil, nil, cutoff)
	if len(r.Collections) == 0 {
		return e
	}
	e.Picks = len(r.Collections[0].Picks)
	for _, p := range r.Collections[0].Picks {
		if targets[p.ID] {
			e.Hits++
		}
	}
	unseen := []Scene{}
	for _, s := range train {
		if !played(s) && s.O == 0 {
			unseen = append(unseen, s)
		}
	}
	sort.Slice(unseen, func(i, j int) bool {
		a, b := 0, 0
		if unseen[i].Studio != nil {
			a = studio[unseen[i].Studio.ID]
		}
		if unseen[j].Studio != nil {
			b = studio[unseen[j].Studio.ID]
		}
		if a == b {
			return unseen[i].ID < unseen[j].ID
		}
		return a > b
	})
	for _, s := range unseen[:min(o.Count, len(unseen))] {
		if targets[s.ID] {
			e.StudioBaselineHits++
		}
	}
	e.Status = "complete"
	return e
}
