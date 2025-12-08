package llm

import (
	"testing"
)

func TestRepairJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Valid JSON array",
			input: `[{"key":"value"}]`,
			want:  `[{"key":"value"}]`,
		},
		{
			name:  "Truncated JSON array (simple)",
			input: `[{"key":"value"},{"key":"val`,
			want:  `[{"key":"value"}]`,
		},
		{
			name:  "Truncated JSON array (nested)",
			input: `[{"a":1},{"b":2},{"c":`,
			want:  `[{"a":1},{"b":2}]`,
		},
		{
			name:  "Truncated after comma",
			input: `[{"a":1},`,
			want:  `[{"a":1}]`,
		},
		{
			name:  "No valid objects",
			input: `[{"key":"val`,
			want:  `[]`, // or maybe just empty string? current impl returns "[]"
		},
		{
			name:  "Not an array",
			input: `{"key":"value"}`,
			want:  `{"key":"value"}`,
		},
		{
			name: "Complex real-world like input",
			input: `
[
  {"target":"A", "val":1},
  {"target":"B", "val":2},
  {"target":"C", "val":
`,
			want: `[
  {"target":"A", "val":1},
  {"target":"B", "val":2}]`, // Note: whitespaces are preserved in prefix
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairJSON(tt.input)
			// Normalize logic for comparison could be complex due to whitespace,
			// but RepairJSON implementation is simple string manipulation.
			// Let's check exact match first.
			if got != tt.want {
				// 厳密な一致が難しい場合（改行など）、意味的なチェックが必要かもしれないが
				// 今回の実装は単純切り出しなので完全一致するはず
				t.Errorf("RepairJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}
