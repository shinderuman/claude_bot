package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStringArray_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    StringArray
		wantErr bool
	}{
		{
			name: "Array of strings",
			json: `["foo", "bar"]`,
			want: StringArray{"foo", "bar"},
		},
		{
			name: "Single string",
			json: `"baz"`,
			want: StringArray{"baz"},
		},
		{
			name: "Empty array",
			json: `[]`,
			want: StringArray{},
		},
		{
			name:    "Invalid type (number)",
			json:    `123`,
			wantErr: true,
		},
		{
			name:    "Invalid type (object)",
			json:    `{"a": 1}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sa StringArray
			err := json.Unmarshal([]byte(tt.json), &sa)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(sa, tt.want) {
				t.Errorf("UnmarshalJSON() got = %v, want %v", sa, tt.want)
			}
		})
	}
}

func TestSearchQuery_Unmarshal(t *testing.T) {
	// Test actual struct usage
	jsonStr := `{
		"target_candidates": "one_target",
		"keys": ["key1", "key2"]
	}`

	var q SearchQuery
	if err := json.Unmarshal([]byte(jsonStr), &q); err != nil {
		t.Fatalf("Failed to unmarshal SearchQuery: %v", err)
	}

	wantTargets := StringArray{"one_target"}
	if !reflect.DeepEqual(q.TargetCandidates, wantTargets) {
		t.Errorf("TargetCandidates = %v, want %v", q.TargetCandidates, wantTargets)
	}

	wantKeys := StringArray{"key1", "key2"}
	if !reflect.DeepEqual(q.Keys, wantKeys) {
		t.Errorf("Keys = %v, want %v", q.Keys, wantKeys)
	}
}
