package recommendations

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLunaStructuredResponseUsageAndNoTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		if b["model"] != "gpt-6-luna" || b["store"] != false || b["service_tier"] != "default" || b["tools"] != nil {
			t.Error("wrong API configuration")
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"completed","usage":{"input_tokens":1000,"output_tokens":100,"input_tokens_details":{"cached_tokens":600,"cache_write_tokens":200}},"output":[{"content":[{"type":"output_text","text":"{\"collections\":{\"for-you\":{\"a\":20,\"b\":90}}}"}]}]}`))
	}))
	defer server.Close()
	report := LibraryReport{Collections: []Collection{{Kind: "for-you", Candidates: []Pick{{ID: "a"}, {ID: "b"}}}}}
	usage, err := (Luna{Key: "secret", HTTP: server.Client(), Endpoint: server.URL}).Organize(t.Context(), &report, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Input != 1000 || usage.Output != 100 || usage.Cached != 600 || usage.CacheWrites != 200 || usage.Cost != EstimatedCost(1000, 100, 600, 200) || report.Collections[0].Candidates[0].ID != "b" {
		t.Fatal(usage, report)
	}
}

func TestPrioritiesRejectUnknownOrMissingCandidatesBeforeReordering(t *testing.T) {
	r := LibraryReport{Collections: []Collection{{Kind: "for-you", Candidates: []Pick{{ID: "a"}, {ID: "b"}}}, {Kind: "recent", Candidates: []Pick{{ID: "c"}}}}}
	if err := applyPriorities(&r, []byte(`{"collections":{"for-you":{"a":10,"b":90},"recent":{"wrong":50}}}`)); err == nil {
		t.Fatal("unknown ID accepted")
	}
	if r.Collections[0].Candidates[0].ID != "a" {
		t.Fatal("partial invalid response mutated order")
	}
	if err := applyPriorities(&r, []byte(`{"collections":{"for-you":{"a":10,"b":90},"recent":{"c":50}}}`)); err != nil {
		t.Fatal(err)
	}
	if r.Collections[0].Candidates[0].ID != "b" {
		t.Fatal("priority ignored")
	}
}

func TestPrioritiesConsumeCompleteStructuredObjectWithExtraTextSegment(t *testing.T) {
	r := LibraryReport{Collections: []Collection{{Kind: "for-you", Candidates: []Pick{{ID: "a"}, {ID: "b"}}}}}
	if err := applyPriorities(&r, []byte(`{"collections":{"for-you":{"a":10,"b":90}}}ignored extra segment`)); err != nil {
		t.Fatal(err)
	}
	if r.Collections[0].Candidates[0].ID != "b" {
		t.Fatal("priority ignored")
	}
}

func TestCachePricingAndFullSchemaReservation(t *testing.T) {
	// 200 ordinary + 600 cached + 200 cache writes; 100 output tokens.
	want := (200*.10 + 600*.01 + 200*.125 + 100*.50) / 1e6
	if got := EstimatedCost(1000, 100, 600, 200); got != want {
		t.Fatalf("%g != %g", got, want)
	}
	r := LibraryReport{Collections: []Collection{{Kind: "for-you"}}}
	for i := 0; i < 500; i++ {
		r.Collections[0].Candidates = append(r.Collections[0].Candidates, Pick{ID: fmt.Sprint(i), Reasons: []string{"Strong explicit feedback"}})
	}
	b := LunaRequest(r, "none", 16000)
	if len(b) > 240000 || len(b) <= len(LunaInput(r)) {
		t.Fatal("request bound/schema missing")
	}
	if ReserveCost(b, 16000) <= EstimatedCost(len(b), 16000, 0, len(b)) {
		t.Fatal("framing not reserved")
	}
	if math.Abs(EstimatedCost(300000, 100, 0, 0)-(300000*.10*2+100*.50*1.5)/1e6) > 1e-12 {
		t.Fatal("long context price incorrect")
	}
}

func TestLunaFullThousandCandidatePoolFitsAndRetainsEvidence(t *testing.T) {
	r := LibraryReport{FeedbackScenes: 2705, Collections: []Collection{{Kind: "for-you"}}}
	rating := 90
	for i := 0; i < 1000; i++ {
		r.Collections[0].Candidates = append(r.Collections[0].Candidates, Pick{ID: fmt.Sprint(30000 + i), Score: 4.123456789012345 + float64(i)/1000, Confidence: "medium", Rating: &rating, Studio: fmt.Sprint(i % 40), PerformerIDs: []string{fmt.Sprint(i % 300), fmt.Sprint(i % 400)}, Reasons: []string{"No recorded play", "Scene rating 90/100", "Favourited performer", "Related metadata has positive feedback", "Matches one of several performers supported by positive feedback"}})
	}
	var input struct {
		Columns     []string `json:"columns"`
		Evidence    []string `json:"evidence_dictionary"`
		Collections []struct {
			Candidates [][]json.RawMessage `json:"candidates"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(LunaInput(r), &input); err != nil {
		t.Fatal(err)
	}
	if len(input.Collections[0].Candidates) != 1000 || len(input.Evidence) != 5 {
		t.Fatal("shortlist or evidence lost")
	}
	for i, row := range input.Collections[0].Candidates {
		var id string
		var score float64
		var refs []int
		var cast []string
		var actualRating int
		json.Unmarshal(row[0], &id)
		json.Unmarshal(row[1], &score)
		json.Unmarshal(row[3], &actualRating)
		json.Unmarshal(row[5], &cast)
		json.Unmarshal(row[6], &refs)
		p := r.Collections[0].Candidates[i]
		if id != p.ID || score != p.Score || actualRating != rating || len(cast) != len(p.PerformerIDs) || len(refs) != len(p.Reasons) {
			t.Fatal("candidate data changed")
		}
		for j, ref := range refs {
			if input.Evidence[ref] != p.Reasons[j] {
				t.Fatal("evidence changed")
			}
		}
	}
	request := LunaRequest(r, "none", 16000)
	t.Logf("1,000-candidate request: %d bytes", len(request))
	if len(request) > 240000 {
		t.Fatal("full pool exceeds request limit")
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		scores := map[string]float64{}
		for _, p := range r.Collections[0].Candidates {
			scores[p.ID] = 50
		}
		raw, _ := json.Marshal(map[string]any{"collections": map[string]any{"for-you": scores}})
		json.NewEncoder(w).Encode(map[string]any{"status": "completed", "usage": map[string]int{"input_tokens": 30000, "output_tokens": 6000}, "output": []any{map[string]any{"content": []any{map[string]string{"type": "output_text", "text": string(raw)}}}}})
	}))
	defer server.Close()
	u, err := (Luna{Key: "secret", HTTP: server.Client(), Endpoint: server.URL}).Organize(t.Context(), &r, 16000)
	if err != nil || calls != 1 || u.Requests != 1 {
		t.Fatalf("ranking skipped: %v, calls=%d", err, calls)
	}
	for _, p := range r.Collections[0].Candidates {
		if p.LunaPriority == nil {
			t.Fatal("candidate was not ranked")
		}
	}
}

func TestLunaSchemaUsesBoundedIntegersAndReportsIncompleteCause(t *testing.T) {
	r := LibraryReport{Collections: []Collection{{Kind: "for-you", Candidates: []Pick{{ID: "1"}}}}}
	var request map[string]any
	json.Unmarshal(LunaRequest(r, "none", 16000), &request)
	schema := request["text"].(map[string]any)["format"].(map[string]any)["schema"].(map[string]any)
	collection := schema["properties"].(map[string]any)["collections"].(map[string]any)["properties"].(map[string]any)["for-you"].(map[string]any)
	priority := collection["properties"].(map[string]any)["1"].(map[string]any)
	if priority["type"] != "integer" || priority["minimum"] != float64(0) || priority["maximum"] != float64(100) {
		t.Fatal("unbounded priorities")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":1000,"output_tokens":16000}}`))
	}))
	defer server.Close()
	u, e := (Luna{Key: "secret", HTTP: server.Client(), Endpoint: server.URL}).Organize(t.Context(), &r, 16000)
	if e == nil || !strings.Contains(e.Error(), "max_output_tokens") || u.Output != 16000 || r.Collections[0].Candidates[0].LunaPriority != nil {
		t.Fatal("incomplete response mishandled", u, e)
	}
}
