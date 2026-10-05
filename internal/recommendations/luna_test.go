package recommendations

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
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
