package recommendations

import (
	"encoding/json"
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
		if b["model"] != "gpt-6-luna" || b["store"] != false || b["tools"] != nil {
			t.Error("wrong API configuration")
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"completed","usage":{"input_tokens":1000,"output_tokens":100},"output":[{"content":[{"type":"output_text","text":"{\"collections\":{\"for-you\":{\"a\":20,\"b\":90}}}"}]}]}`))
	}))
	defer server.Close()
	report := LibraryReport{Collections: []Collection{{Kind: "for-you", Candidates: []Pick{{ID: "a"}, {ID: "b"}}}}}
	usage, err := (Luna{Key: "secret", HTTP: server.Client(), Endpoint: server.URL}).Organize(t.Context(), &report, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Input != 1000 || usage.Output != 100 || report.Collections[0].Candidates[0].ID != "b" {
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
