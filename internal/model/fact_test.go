package model

import (
	"encoding/json"
	"testing"
)

func TestFactValueUsesString(t *testing.T) {
	var fact Fact
	if err := json.Unmarshal([]byte(`{"key":"hobby","value":"coding"}`), &fact); err != nil {
		t.Fatal(err)
	}
	if fact.Value != "coding" {
		t.Fatalf("value = %q", fact.Value)
	}
	for _, input := range []string{`{"value":true}`, `{"value":25}`, `{"value":["coding"]}`, `{"value":{"hobby":"coding"}}`} {
		if err := json.Unmarshal([]byte(input), &fact); err == nil {
			t.Fatalf("accepted non-string value: %s", input)
		}
	}
}
