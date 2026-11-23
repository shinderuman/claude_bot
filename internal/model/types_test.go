package model

import (
	"encoding/json"
	"testing"
)

func TestSearchQueryUsesArrayFields(t *testing.T) {
	var query SearchQuery
	if err := json.Unmarshal([]byte(`{"target_candidates":["user"],"keys":["hobby"]}`), &query); err != nil {
		t.Fatal(err)
	}
	if len(query.TargetCandidates) != 1 || query.TargetCandidates[0] != "user" ||
		len(query.Keys) != 1 || query.Keys[0] != "hobby" {
		t.Fatalf("query = %+v", query)
	}
	for _, input := range []string{
		`{"target_candidates":"user","keys":["hobby"]}`,
		`{"target_candidates":["user"],"keys":"hobby"}`,
	} {
		if err := json.Unmarshal([]byte(input), &query); err == nil {
			t.Fatalf("accepted scalar field: %s", input)
		}
	}
}
