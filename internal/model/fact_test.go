package model

import (
	"encoding/json"
	"testing"
)

func TestFactUnmarshal(t *testing.T) {
	tests := []struct {
		name     string
		jsonStr  string
		wantKey  string
		wantType string // "string", "bool", "float64"
	}{
		{
			name: "String Value",
			jsonStr: `[
				{
					"target": "user1",
					"key": "hobby",
					"value": "coding"
				}
			]`,
			wantKey:  "hobby",
			wantType: "string",
		},
		{
			name: "Boolean Value",
			jsonStr: `[
				{
					"target": "user1",
					"key": "is_active",
					"value": true
				}
			]`,
			wantKey:  "is_active",
			wantType: "bool",
		},
		{
			name: "Number Value",
			jsonStr: `[
				{
					"target": "user1",
					"key": "age",
					"value": 25
				}
			]`,
			wantKey:  "age",
			wantType: "float64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var facts []Fact
			if err := json.Unmarshal([]byte(tt.jsonStr), &facts); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}

			if len(facts) != 1 {
				t.Fatalf("Expected 1 fact, got %d", len(facts))
			}

			fact := facts[0]
			if fact.Key != tt.wantKey {
				t.Errorf("Expected key %s, got %s", tt.wantKey, fact.Key)
			}

			switch v := fact.Value.(type) {
			case string:
				if tt.wantType != "string" {
					t.Errorf("Expected type %s, got string", tt.wantType)
				}
			case bool:
				if tt.wantType != "bool" {
					t.Errorf("Expected type %s, got bool", tt.wantType)
				}
			case float64:
				if tt.wantType != "float64" {
					t.Errorf("Expected type %s, got float64", tt.wantType)
				}
			default:
				t.Errorf("Unexpected type: %T", v)
			}
		})
	}
}
