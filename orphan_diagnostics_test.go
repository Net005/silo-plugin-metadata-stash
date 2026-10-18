package main

import (
	"testing"
)

// The server's compact failure diagnostic omits candidate years and may retain
// the metadata provider's linked backend ID alongside its Stash identity.
func TestOrphanRepairAcceptsSparseDiagnosticWithLinkedBackendIdentity(t *testing.T) {
	f := newOrphanFixture(t)
	f.alter = func(path string, body map[string]any) {
		if path != "/api/v2/libraries/7/metadata-match-queue" {
			return
		}
		candidate := body["movies"].([]any)[0].(map[string]any)["failure_detail"].(map[string]any)["decision"].(map[string]any)["top_candidates"].([]any)[0].(map[string]any)
		delete(candidate, "year")
		candidate["provider_ids"] = map[string]string{"stash": "stash:42", "javbeacon": "900"}
	}
	summary, err := f.task.repairUnassociatedFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if summary["admitted"] != 1 || f.scans != 1 {
		t.Fatalf("verified sparse diagnostic not repaired: summary=%v scans=%d", summary, f.scans)
	}
}
