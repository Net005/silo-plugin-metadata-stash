package main

import "testing"

func TestSearchResultPublishesReleaseCodeAlias(t *testing.T) {
	r := searchResult(scene{ID: "42", Code: "EXAMPLE-001", Title: "EXAMPLE-001 A descriptive scene title", Date: "2024-02-29"})
	if r.Title != "EXAMPLE-001 A descriptive scene title" {
		t.Fatal("display title changed")
	}
	aliases := r.GetTitleAliases()
	if len(aliases) != 1 || aliases[0].Title != "EXAMPLE-001" {
		t.Fatalf("release code missing from aliases: %v", aliases)
	}
}
func TestSearchResultDoesNotInventReleaseCode(t *testing.T) {
	for _, s := range []scene{{ID: "42", Title: "A title"}, {ID: "42", Code: "  ", Title: "A title"}} {
		if len(searchResult(s).GetTitleAliases()) != 0 {
			t.Fatal("invented release code alias")
		}
	}
}
