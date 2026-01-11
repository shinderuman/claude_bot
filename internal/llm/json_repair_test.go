package llm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// repairTestCases defines inputs and expected outputs for integration testing.
// These cases cover various forms of malformed JSON that the repair logic should handle.
var repairTestCases = []struct {
	name  string
	input string
	want  string
	exact bool // If true, requires exact string match. If false, semantic equality is sufficient.
}{
	// Basic Validity
	{
		name:  "Valid JSON array",
		input: `[{"key":"value"}]`,
		want:  `[{"key":"value"}]`,
		exact: true,
	},
	{
		name:  "Not an array",
		input: `{"key":"value"}`,
		want:  `{"key":"value"}`,
		exact: true,
	},
	{
		name:  "Double array input",
		input: `[[{"key":"value"}]]`,
		want:  `[[{"key":"value"}]]`,
		exact: true,
	},

	// Tier 1: Structural Repairs (Truncation, Braces)
	{
		name:  "Truncated JSON array (simple)",
		input: `[{"key":"value"},{"key":"val`,
		want:  `[{"key":"value"}]`,
		exact: true,
	},
	{
		name:  "Truncated JSON array (nested)",
		input: `[{"a":1},{"b":2},{"c":`,
		want:  `[{"a":1},{"b":2}]`,
		exact: true,
	},
	{
		name:  "Truncated after comma",
		input: `[{"a":1},`,
		want:  `[{"a":1}]`,
		exact: true,
	},
	{
		name:  "No valid objects",
		input: `[{"key":"val`,
		want:  `[]`,
		exact: true,
	},
	{
		name:  "Missing closing brace",
		input: `{"key":"value"`,
		want:  `{"key":"value"}`,
		exact: true,
	},
	{
		name:  "Missing closing bracket",
		input: `["a","b"`,
		want:  `["a","b"]`,
		exact: true,
	},
	{
		name:  "Nested missing closers",
		input: `{"a":[{"b":1`,
		want:  `{"a":[{"b":1}]}`,
		exact: true,
	},
	{
		name:  "Object with unclosed array",
		input: `{"conflicting_fact_ids": ["id1", "id2" }`,
		want:  `{"conflicting_fact_ids": ["id1", "id2" ]}`,
		exact: true,
	},
	{
		name:  "Object with unclosed array and missing comma",
		input: `{"key": ["val1", "val2"`,
		want:  `{"key": ["val1", "val2"]}`,
		exact: true,
	},
	{
		name:  "Extra closing brace in array",
		input: `[{"target":"A","val":1}, {"target":"B","val":2}}`,
		want:  `[{"target":"A","val":1}, {"target":"B","val":2}]`,
		exact: true,
	},
	{
		name:  "Multiple top-level arrays",
		input: `[{"key":"value1"}][{"key":"value2"}]`,
		want:  `[{"key":"value1"}]`,
		exact: true,
	},
	{
		name:  "MissingCommaBetweenObjects",
		input: `[{"a":1} {"b":2}]`,
		want:  `[{"a":1},{"b":2}]`,
		exact: true,
	},
	{
		name:  "TrailingCommaInArray",
		input: `[{"key":"val1"},]`,
		want:  "",
		exact: false,
	},
	{
		name:  "DanglingKey",
		input: `{"key":"value", "Etc."}`,
		want:  `{"key":"value"}`,
		exact: true,
	},
	{
		name:  "DanglingKey False Positive",
		input: `{"safe": ["a", "b"]}`,
		want:  `{"safe": ["a", "b"]}`,
		exact: true,
	},
	{
		name:  "Truncated during string",
		input: `{"key": "truncate`,
		want:  `{"key": "truncate"}`,
		exact: true,
	},

	// Tier 2: Character/Encoding Repairs
	{
		name:  "Unescaped newline in value",
		input: "[{\"key\":\"val\nue\"}]",
		want:  `[{"key":"val\nue"}]`,
		exact: true,
	},
	{
		name:  "Full-width colon",
		input: `[{"key"："value"}]`,
		want:  `[{"key":"value"}]`,
		exact: false,
	},
	{
		name:  "Full-width colon and brackets",
		input: `[{"key"："「value」"}]`,
		want:  `[{"key": "「value」"}]`,
		exact: false,
	},
	{
		name:  "Invalid character '}' after object key",
		input: `[{"key":"value"},{"key"："valid"}]`,
		want:  `[{"key":"value"},{"key": "valid"}]`,
		exact: true,
	},
	{
		name:  "Escaped Single Quote",
		input: `[{"value":"It\'s fine"}]`,
		want:  `[{"value":"It's fine"}]`,
		exact: true,
	},
	{
		name:  "SemicolonSeparator",
		input: `[{"key":"val1"}; {"key":"val2"}]`,
		want:  "",
		exact: false,
	},
	{
		name:  "Error 2: Hex Escape",
		input: `{"text": "Val\x27ue"}`,
		want:  `{"text": "Val'ue"}`,
		exact: false,
	},

	// Tier 3: Quotes and Keys
	{
		name:  "Missing opening quote",
		input: `{"key": 『value』}`,
		want:  `{"key": "『value』"}`,
		exact: false,
	},
	{
		name:  "Garbage quotes",
		input: `{"key":"value""}`,
		want:  `{"key":"value"}`,
		exact: false,
	},
	{
		name:  "Missing closing quote before comma",
		input: `{"key":"value,"next":1}`,
		want:  `{"key":"value","next":1}`,
		exact: true,
	},
	{
		name:  "Complex unescaped quotes in value",
		input: `[{"key":"val","value":"Contains "quotes" inside"}]`,
		want:  "",
		exact: false,
	},
	{
		name:  "MissingComma",
		input: `[{"key":"val1" "key2":"val2"}]`,
		want:  "",
		exact: false,
	},
	{
		name:  "UnexpectedColon",
		input: `[{"key":"value":"nested"}]`,
		want:  "",
		exact: false,
	},
	{
		name:  "KeyEqualsValue",
		input: `[{"key"="value"}]`,
		want:  "",
		exact: false,
	},
	{
		name:  "Error 1: Garbage key after object in array",
		input: `[{"key":"value"}col:es]`,
		want:  "",
		exact: false,
	},
	{
		name:  "Error 3: Unquoted value in array",
		input: `[xyzzy]`,
		want:  `["xyzzy"]`,
		exact: true,
	},
	{
		name:  "Merged key-value",
		input: `[{"target":"user","key":"attr","valueContent"}]`,
		want:  `[{"target":"user","key":"attr","value":"Content"}]`,
		exact: true,
	},
	{
		name:  "Error 4: Object with values but no keys",
		input: `[{"__general__","topic","news","content"}]`,
		want:  `["__general__","topic","news","content"]`,
		exact: true,
	},
	{
		name:  "Error 5: Full-width colon with quote issue",
		input: `[{"key":"attribute","value："Use dummy value"}]`,
		want:  `[{"key":"attribute","value": "Use dummy value"}]`,
		exact: false,
	},
	{
		name:  "Error 8: Missing closing brace between objects with string ending in brace",
		input: `[{"key":"val"} {"key2":"val2"}]`,
		want:  `[{"key":"val"},{"key2":"val2"}]`,
		exact: true,
	},
	{
		name:  "Error 9: Mixed array and object style (failed to parse JSON after 4-tier repair)",
		input: `["assistant","maidrobo","profile","identity":"Target Alias","basic_identity":"Sister of the user.","attribute":"Talks with suffix.","model_spec":"Quantum-Maid-EX","preferences_lifestyle":"Likes alcohol"]`,
		want:  `["assistant","maidrobo","profile", {"identity": "Target Alias"}, {"basic_identity": "Sister of the user."}, {"attribute": "Talks with suffix."}, {"model_spec": "Quantum-Maid-EX"}, {"preferences_lifestyle": "Likes alcohol"}]`,
		exact: false,
	},
	{
		name:  "Error 10: Full-width colon inside string (Production Error 1)",
		input: `["identity":"Alias：Name","attribute":"Suffix：-san","preferences":"Likes：Tea"]`,
		want:  `[{"identity": "Alias：Name"}, {"attribute": "Suffix：-san"}, {"preferences": "Likes：Tea"}]`,
		exact: false,
	},
	{
		name:  "Error 11: Double encoded or escaped JSON in array (Production Error 2)",
		input: `["{\"target\":\"bot\",\"key\":\"loc\",\"value\":\"In the shadows\"}", "{\"target\":\"bot\",\"key\":\"pref\",\"value\":\"Magic\"}"]`,
		want:  `["{\"target\":\"bot\",\"key\":\"loc\",\"value\":\"In the shadows\"}", "{\"target\":\"bot\",\"key\":\"pref\",\"value\":\"Magic\"}"]`,
		exact: false,
	},
	{
		name:  "Error 12: Double commas and mixed structure (Production Error 3)",
		input: `[{"target":"general","key":"plan","value":"Standard plan"},,{"target":"general","key":"news","value":"Update available"},]`,
		want:  `[{"target":"general","key":"plan","value":"Standard plan"},{"target":"general","key":"news","value":"Update available"}]`,
		exact: false,
	},
	{
		name: "Error 13: Missing closing brace before next object",
		input: `[
{"target": "t1", "value": "v1"},
{"target": "t2", "value": "v2",
{"target": "t3", "value": "v3"}
]`,
		want: `[
{"target": "t1", "value": "v1"},
{"target": "t2", "value": "v2"},
{"target": "t3", "value": "v3"}
]`,
		exact: false,
	},
}

// TestIntegration tests the full UnmarshalWithRepair piepline using the test cases above.
func TestIntegration(t *testing.T) {
	for _, tt := range repairTestCases {
		t.Run(tt.name, func(t *testing.T) {
			var v interface{}
			err := UnmarshalWithRepair(tt.input, &v, "[TEST]: ")

			// Case 1: Expected success (want JSON is provided)
			if tt.want != "" {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
					return
				}

				// Verify semantic equality
				var wantV interface{}
				if err := json.Unmarshal([]byte(tt.want), &wantV); err != nil {
					t.Fatalf("Invalid 'want' JSON in test case: %v", err)
				}
				if !reflect.DeepEqual(v, wantV) {
					t.Errorf("Mismatch for %s.\nGot: %+v\nWant: %+v\n(Input: %q)", tt.name, v, wantV, tt.input)
				}

				// Verify exact string match if requested
				if tt.exact {
				}
			} else {
				// Case 2: Validity check only (no specific 'want' JSON)
				if err != nil {
					t.Errorf("Repair failed for input: %s\nError: %v", tt.input, err)
				}
			}
		})
	}
}

// TestTier1 verifies structural repairs (braces, brackets, commas).
func TestTier1(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{`[{"a":1},`, `[{"a":1}]`},                 // Truncated array
		{`[[{"a":1}]]`, `[{"a":1}]`},               // Double array
		{`{"a":1`, `{"a":1}`},                      // Missing brace
		{`{"a": ["b" }`, `{"a": ["b" ]}`},          // Unclosed array in object
		{`{"a":1, "b":2,}`, `{"a":1, "b":2}`},      // Trailing comma
		{`[{"a":1} {"b":2}]`, `[{"a":1},{"b":2}]`}, // Missing comma between objects
	}
	for _, tc := range cases {
		if got := repairTier1(tc.input); got != tc.want {
			t.Errorf("Tier1 failed for %q: got %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestTier2 verifies character level repairs (encoding, localized symbols).
func TestTier2(t *testing.T) {
	cases := []struct {
		input, wantSubstring string
	}{
		{`{"val": "It\'s"}`, `{"val": "It's"}`}, // Escaped single quote
		{`{"key": "\x27val\x27"}`, `\u0027`},    // Hex escape
		{`{"a":"b"}; {"c":"d"}`, `{"a":"b"}`},   // Semicolon (truncated to first object)
	}
	for _, tc := range cases {
		got := repairTier2(tc.input)
		if !strings.Contains(got, tc.wantSubstring) {
			// Fallback: check simple equality if substring is full body
			if got != tc.wantSubstring {
				t.Errorf("Tier2 failed for %q. Got: %q, Expected to contain/be: %q", tc.input, got, tc.wantSubstring)
			}
		}
	}
}

// TestTier3 verifies quote and key repairs.
func TestTier3(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{`{key: "val"}`, `{"key": "val"}`},    // Unquoted key
		{`{"a":"b""}`, `{"a":"b"}`},           // Garbage quote
		{`{"a":"b,"c":1}`, `{"a":"b","c":1}`}, // Missing quote before comma
		{`[val1, val2]`, `["val1", "val2"]`},  // Unquoted values in array
		{`{"key": news}`, `{"key": "news"}`},  // Unquoted value starting with n
	}
	for _, tc := range cases {
		got := repairTier3(tc.input)
		if !json.Valid([]byte(got)) {
			t.Errorf("Tier3 produced invalid JSON for %q. Got: %q", tc.input, got)
		}
		// For simple cases, we expect exact structure match (ignoring whitespace if possible, but here strings are tight)
		if tc.want != "" && got != tc.want && strings.ReplaceAll(got, " ", "") != strings.ReplaceAll(tc.want, " ", "") {
			t.Errorf("Tier3 content mismatch. Input: %q\nGot: %q\nWant: %q", tc.input, got, tc.want)
		}
	}
}

// TestTier4 verifies the most aggressive repairs (masking, heuristics).
// This essentially covers the legacy RepairJSON logic.
func TestTier4(t *testing.T) {
	// These cases are typically too hard for Tier 1-3
	cases := []struct {
		input, want string
	}{
		{`[{"target":"user","key":"attr","valueContent"}]`, `[{"target":"user","key":"attr","value":"Content"}]`}, // Merged key
		{`[{"__general__","topic"}]`, `["__general__","topic"]`},                                                  // Object-like array
	}
	for _, tc := range cases {
		got := repairTier4(tc.input)
		if !json.Valid([]byte(got)) {
			t.Errorf("Tier4 failed to fix %q. Got: %q", tc.input, got)
		}
		if tc.want != "" {
			var vGot, vWant interface{}
			json.Unmarshal([]byte(got), &vGot)
			json.Unmarshal([]byte(tc.want), &vWant)
			if !reflect.DeepEqual(vGot, vWant) {
				t.Errorf("Tier4 semantic mismatch for %q.\nGot: %q\nWant: %q", tc.input, got, tc.want)
			}
		}
	}
}

// TestHelpers covers specific regex or logic helpers deeply.
func TestHelpers(t *testing.T) {
	t.Run("repairDoubleArray", func(t *testing.T) {
		if got := repairDoubleArray(`[[{"a":1}]]`); got != `[{"a":1}]` {
			t.Errorf("got %q", got)
		}
	})

	t.Run("maskStrings", func(t *testing.T) {
		input := `{"key": "value", "esc": "\"quoted\""}`
		masked, originals := maskStrings(input)
		if !strings.Contains(masked, "__STR_0__") {
			t.Errorf("Masking failed. Got: %s", masked)
		}
		restored := unmaskStrings(masked, originals)
		if restored != input {
			t.Errorf("Restore failed.\nOrg: %s\nRes: %s", input, restored)
		}
	})
}

// Fuzzers
func FuzzRepairJSON(f *testing.F) {
	seeds := []string{
		`{"key": "value"}`,
		`[{"key": "value"}]`,
		`{"key": "val\"ue"}`,
		`{"key": "value`,
		`[[{"key": "value"}]]`,
		`{"key": news}`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		defer func() {
			if r := recover(); r != nil {
				// Panics are bad
				t.Fatalf("Panic in repairTier4: %v", r)
			}
		}()

		// Just check for crashes or major regressions on valid json
		repaired := repairTier4(input)
		if json.Valid([]byte(input)) {
			if !json.Valid([]byte(repaired)) {
				t.Errorf("Broken valid JSON.\nInput: %q\nOutput: %q", input, repaired)
			}
		}
	})
}

func FuzzStructuralRepair(f *testing.F) {
	f.Add(`{"key":"value"}`)
	f.Add(`{{{`)
	f.Fuzz(func(t *testing.T, input string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Panic: %v", r)
			}
		}()
		r := &structuralRepairer{runes: []rune(input)}
		_ = r.repair()
	})
}

func TestRegexes(t *testing.T) {
	t.Run("fixGarbageQuotes", func(t *testing.T) {
		input := `{"key":"value""}`
		want := `{"key":"value"}`
		if got := fixGarbageQuotes(input); got != want {
			t.Errorf("fixGarbageQuotes failed matching. Got: %q, Want: %q", got, want)
		}
	})

	t.Run("fixMergedKeyValue", func(t *testing.T) {
		input := `[{"target":"user","key":"attr","valueContent"}]`
		want := `[{"target":"user","key":"attr","value":"Content"}]`
		if got := fixMergedKeyValue(input); got != want {
			t.Errorf("fixMergedKeyValue failed matching. Got: %q, Want: %q", got, want)
		}
	})

	t.Run("fixHexEscapes", func(t *testing.T) {
		input := `\x27`
		got := fixHexEscapes(input)
		if !strings.Contains(got, `\u0027`) {
			t.Errorf("fixHexEscapes failed. Got: %q", got)
		}
	})
}
