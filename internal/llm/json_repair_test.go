package llm

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRepairJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		exact bool
	}{
		// Basic Structure
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
			want:  `[{"key":"value"}]`,
			exact: true,
		},

		// Array Truncation
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

		// Structural Repairs
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
			name:  "Object with unclosed array (reported error)",
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
			name:  "Extra closing brace in array (reported error)",
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

		// Malformed Content & Preprocessing
		{
			name: "Unescaped newline in value",
			input: `[{"key":"val
ue"}]`,
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
			name:  "Full-width colon and brackets for value",
			input: `[{"key"："「value」"}]`,
			want:  `[{"key":"「value」"}]`,
			exact: false,
		},
		{
			name:  "Invalid character '}' after object key",
			input: `[{"key":"value"},{"key"："valid"}]`,
			want:  `[{"key":"value"},{"key":"valid"}]`,
			exact: true,
		},
		{
			name:  "Merged key-value",
			input: `[{"target":"user","target_username":"unknown","key":"attribute","value自称メイドキャラクター"}]`,
			want:  `[{"target":"user","target_username":"unknown","key":"attribute","value":"自称メイドキャラクター"}]`,
			exact: true,
		},
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
			exact: true,
		},
		{
			name:  "Missing closing quote before comma",
			input: `{"key":"value,"next":1}`,
			want:  `{"key":"value","next":1}`,
			exact: true,
		},

		// Regression Tests for Reported Bugs
		{
			name:  "Escaped Single Quote",
			input: `[{"target":"__general__","target_username":"GameSpark","key":"release","value":"Epic Gamesストアでサバイバルホラー『Sorry We\'re Closed』が..."}]`,
			want:  `[{"target":"__general__","target_username":"GameSpark","key":"release","value":"Epic Gamesストアでサバイバルホラー『Sorry We're Closed』が..."}]`,
			exact: true,
		},
		{
			name: "Complex unescaped quotes in value",
			input: `[
  {"target":"__general__","target_username":"PlayStation Blog","key":"release","value":"「DualSense® ワイヤレスコントローラー "原神" リミテッドエディション」が..."},
  {"target":"__general__","target_username":"PlayStation Blog","key":"news","value":"PlayStation®5での「原神」新バージョン「Luna III」がリリース。"}
]`,
			want:  "", // Just check validity
			exact: false,
		},
		{
			name:  "MissingComma",
			input: `[{"target":"__general__","key":"event","target":"__general__" "event":"1月21日までの開催"}]`,
			want:  "",
			exact: false,
		},
		{
			name:  "UnexpectedColon",
			input: `[{"target":"mesugakiroid","key":"value":" Pint Outlook","value": "HEJE"}]`,
			want:  "",
			exact: false,
		},
		{
			name:  "KeyEqualsValue",
			input: `[{"target":"__general__","key"="economic_indicators","value":"..."}]`,
			want:  "",
			exact: false,
		},
		{
			name:  "SemicolonSeparator",
			input: `[{"key":"val1"}; {"key":"val2"}]`,
			want:  "",
			exact: false,
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
			name:  "MissingCommaBetweenObjects",
			input: `[{"a":1} {"b":2}]`,
			want:  `[{"a":1},{"b":2}]`,
			exact: true,
		},
		{
			// Regression test: fixDanglingKey must not delete valid parts of array literals
			name:  "DanglingKey False Positive (Regression)",
			input: `{"safe": ["a", "b"]}`,
			want:  `{"safe": ["a", "b"]}`,
			exact: true,
		},
		{
			name:  "Truncated during string",
			input: `{"key": "truncate`,
			want:  `{"key": "truncate"}`, // Should close the string and the object
			exact: true,
		},

		// Production Error Cases
		{
			name:  "Error 1: Garbage key after object in array",
			input: `[{"key":"value"}col:es]`,
			want:  "", // Validity check only
			exact: false,
		},
		{
			name:  "Error 2: Hex Escape",
			input: `{"text": "Val\x27ue"}`,
			want:  `{"text": "Val'ue"}`,
			exact: false,
		},
		{
			name:  "Error 3: Unquoted value in array",
			input: `[xyzzy]`,
			want:  `["xyzzy"]`,
			exact: true,
		},
		{
			name:  "Error 4: Object with values but no keys",
			input: `[{"__general__","topic","news","content"}]`,
			want:  `["__general__","topic","news","content"]`,
			exact: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairJSON(tt.input)

			// 1. Validity Check
			if tt.want != "" && !tt.exact {
				var v1, v2 interface{}
				if err := json.Unmarshal([]byte(got), &v1); err != nil {
					t.Errorf("Repaired JSON is invalid: %v\nInput: %s\nGot: %s", err, tt.input, got)
				}
				if err := json.Unmarshal([]byte(tt.want), &v2); err != nil {
					t.Fatalf("Test setup error: 'want' JSON is invalid: %v", err)
				}
				if !reflect.DeepEqual(v1, v2) {
					t.Errorf("RepairJSON produced semantically different JSON.\nInput: %s\nGot: %s\nWant: %s", tt.input, got, tt.want)
				}
			} else if tt.want == "" {
				var v interface{}
				if err := json.Unmarshal([]byte(got), &v); err != nil {
					t.Errorf("Repaired JSON is invalid: %v\nInput: %s\nGot: %s", err, tt.input, got)
				}
			}

			if tt.exact {
				if got != tt.want {
					t.Errorf("RepairJSON() mismatch\nGot:  %s\nWant: %s", got, tt.want)
				}
			}
		})
	}
}

func TestRepairHelpers(t *testing.T) {
	t.Run("repairDoubleArray", func(t *testing.T) {
		if got := repairDoubleArray(`[[{"a":1}]]`); got != `[{"a":1}]` {
			t.Errorf("got %q", got)
		}
		if got := repairDoubleArray(`[{"a":1}]`); got != `[{"a":1}]` {
			t.Errorf("got %q", got)
		}
	})

	t.Run("repairTruncatedArray", func(t *testing.T) {
		if got := repairTruncatedArray(`[{"a":1},`); got != `[{"a":1}]` {
			t.Errorf("got %q", got)
		}
	})
}

func TestPreprocessRules(t *testing.T) {
	t.Run("addQuotesToKeys", func(t *testing.T) {
		input := `{"key: "value", "foo": "bar"}`
		want := `{"key": "value", "foo": "bar"}`
		if got := addQuotesToKeys(input); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
		// Check protocol guard
		inputUrl := `{"url": "http://example.com"}`
		if got := addQuotesToKeys(inputUrl); got != inputUrl {
			t.Errorf("Should not touch protocols. got %q, want %q", got, inputUrl)
		}
	})

	t.Run("replaceOpeningJapaneseQuote", func(t *testing.T) {
		input := `{"key": 「value"}`
		want := `{"key": "value"}`
		if got := replaceOpeningJapaneseQuote(input); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("replaceClosingJapaneseQuote", func(t *testing.T) {
		input := `{"key": "value」}`
		want := `{"key": "value"}`
		if got := replaceClosingJapaneseQuote(input); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("fixMissingCommaQuotes", func(t *testing.T) {
		// Note: The regex matches `:"...","...":` context
		input := `{"k":"value,"next":1}`
		want := `{"k":"value","next":1}`
		if got := fixMissingCommaQuotes(input); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("fixGarbageQuotes", func(t *testing.T) {
		input := `{"key":"value""}`
		want := `{"key":"value"}`
		if got := fixGarbageQuotes(input); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("fixMergedKeyValue", func(t *testing.T) {
		// Matches ([,{]\s*)"(value)([^":,]+)"
		input := `{"key":"value1", "valueContent"}`
		want := `{"key":"value1", "value":"Content"}`
		if got := fixMergedKeyValue(input); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("fixMissingOpeningQuotes", func(t *testing.T) {
		// Matches (:\s*)([^"\[\{\]\}\s0-9\-tfn])
		input := `{"key": 『value』}`
		want := `{"key": "『value』}`
		if got := fixMissingOpeningQuotes(input); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestStructuralRepairer(t *testing.T) {
	t.Run("HandleBackslash", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			inString bool
			wantBuf  string
			wantEsc  bool
		}{
			{"Ignore outside string", `\t`, false, "", false},
			{"Escape inside string", `\t`, true, `\`, true},
			{"Escaped delimiter", `\"}`, true, `"`, false},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				r := &structuralRepairer{
					runes:    []rune(tt.input),
					inString: tt.inString,
				}
				r.handleBackslash()
				if r.sb.String() != tt.wantBuf {
					t.Errorf("got %q, want %q", r.sb.String(), tt.wantBuf)
				}
				if r.escaped != tt.wantEsc {
					t.Errorf("escaped %v, want %v", r.escaped, tt.wantEsc)
				}
			})
		}
	})

	t.Run("HandleQuote", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			inString bool
			wantBuf  string
			wantIn   bool
		}{
			{"Start string", `"foo"`, false, `"`, true},
			{"End string", `",`, true, `"`, false},
			{"Internal quote", `"foo`, true, `\"`, true},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				r := &structuralRepairer{
					runes:    []rune(tt.input),
					inString: tt.inString,
				}
				r.handleQuote()
				if r.sb.String() != tt.wantBuf {
					t.Errorf("got %q, want %q", r.sb.String(), tt.wantBuf)
				}
				if r.inString != tt.wantIn {
					t.Errorf("inString %v, want %v", r.inString, tt.wantIn)
				}
			})
		}
	})
}

func FuzzRepairJSON(f *testing.F) {
	seeds := []string{
		`{"key": "value"}`,
		`[{"key": "value"}]`,
		`{"key": "val\"ue"}`,
		`{"key": "val\\ue"}`,
		`[{"key": "value",}]`,
		`{"key": "value"`,
		`[{"key": "value"`,
		`[[{"key": "value"}]]`,
		`{"key": "value""}`,
		`{"key": "value」}`,
		`{"key"："value"}`,
		`{"key": "value\"}`,
		`\`,
		`"`,
		`{`,
		`[`,
		`}`,
		`]`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("RepairJSON panicked: %v", r)
			}
		}()

		repaired := RepairJSON(input)
		if json.Valid([]byte(input)) {
			if !json.Valid([]byte(repaired)) {
				t.Errorf("Broken valid JSON.\nInput: %q\nOutput: %q", input, repaired)
			}
		}
	})
}

func FuzzStructuralRepair(f *testing.F) {
	seeds := []string{
		`{"key":"value"}`,
		`{"key":"val\"ue"}`,
		`{"key":"value\"}`,
		`{{{`,
		`}}}`,
		`"`,
		`\"`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

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

func TestUnmarshalWithRepair(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		target    interface{}
		check     func(*testing.T, interface{})
		wantError bool
	}{
		{
			name:   "Single object to slice",
			input:  `{"key": "value"}`,
			target: &[]map[string]interface{}{},
			check: func(t *testing.T, v interface{}) {
				res := *(v.(*[]map[string]interface{}))
				if len(res) != 1 {
					t.Errorf("Expected 1 item, got %d", len(res))
				} else if res[0]["key"] != "value" {
					t.Errorf("Expected key=value, got %v", res[0])
				}
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := UnmarshalWithRepair(tt.input, tt.target, "Test")
			if (err != nil) != tt.wantError {
				t.Errorf("UnmarshalWithRepair() error = %v, wantError %v", err, tt.wantError)
				return
			}
			if !tt.wantError && tt.check != nil {
				tt.check(t, tt.target)
			}
		})
	}
}

func TestMaskStrings(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantMasked    string
		wantOriginals []string
	}{
		{
			name:          "Basic string",
			input:         `{"key": "value"}`,
			wantMasked:    `{"__STR_0__": "__STR_1__"}`,
			wantOriginals: []string{`"key"`, `"value"`},
		},
		{
			name:          "Escaped quotes",
			input:         `{"text": "end of list, \"item\"}"}`,
			wantMasked:    `{"__STR_0__": "__STR_1__"}`,
			wantOriginals: []string{`"text"`, `"end of list, \"item\"}"`},
		},
		{
			name:          "Empty string",
			input:         `""`,
			wantMasked:    `"__STR_0__"`,
			wantOriginals: []string{`""`},
		},
		{
			name:          "Multiple escaped chars",
			input:         `"a\"b\\c"`,
			wantMasked:    `"__STR_0__"`,
			wantOriginals: []string{`"a\"b\\c"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMasked, gotOriginals := maskStrings(tt.input)

			if gotMasked != tt.wantMasked {
				t.Errorf("maskStrings() masked mismatch\nGot:  %s\nWant: %s", gotMasked, tt.wantMasked)
			}
			if !reflect.DeepEqual(gotOriginals, tt.wantOriginals) {
				t.Errorf("maskStrings() originals mismatch\nGot:  %v\nWant: %v", gotOriginals, tt.wantOriginals)
			}

			// Verify unmasking restores original
			gotRestored := unmaskStrings(gotMasked, gotOriginals)
			if gotRestored != tt.input {
				t.Errorf("unmaskStrings() failed to restore input\nGot:  %s\nWant: %s", gotRestored, tt.input)
			}
		})
	}
}

func TestRepairJSON_Safety(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "Valid string with equals sign",
			input: `{"content": "formula: a=b"}`,
		},
		{
			name:  "Valid string with colon",
			input: `{"title": "Project: Omega"}`,
		},
		{
			name:  "Valid string with unquoted keys pattern",
			input: `{"text": "example: key: value"}`,
		},
		{
			name:  "Valid string with comma and quote",
			input: `{"quote": "Hello, \"World\""}`,
		},
		{
			name:  "Valid string with Japanese quotes",
			input: `{"dialogue": "「こんにちは」"}`,
		},
		{
			name:  "Valid string with truncated array pattern",
			input: `{"text": "This is not an array [ ..."}`,
		},
		{
			name:  "Valid nested array",
			input: `{"list": ["item1", "item2"]}`,
		},
		{
			name:  "Valid array of objects",
			input: `[{"a":1}, {"b":2}]`,
		},
		{
			name:  "String looking like dangling key",
			input: `{"text": "end of list, \"item\"}"}`,
		},
		{
			name:  "URL in string",
			input: `{"url": "https://example.com/path?query=1"}`,
		},
		{
			name:  "Valid string with unicode escapes",
			input: `{"char": "\u3042"}`,
		},
		{
			name:  "Valid string with backslashes",
			input: `{"path": "C:\\Windows\\System32"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairJSON(tt.input)
			if !json.Valid([]byte(got)) {
				t.Errorf("RepairJSON broke valid JSON.\nInput: %s\nOutput: %s", tt.input, got)
				return
			}
			if !json.Valid([]byte(got)) {
				t.Errorf("RepairJSON broke valid JSON.\nInput: %s\nOutput: %s", tt.input, got)
				return
			}

			var v1, v2 interface{}
			if err := json.Unmarshal([]byte(tt.input), &v1); err != nil {
				t.Fatalf("Test setup error: input is not valid JSON: %s", tt.input)
			}
			if err := json.Unmarshal([]byte(got), &v2); err != nil {
				t.Errorf("Output is not valid JSON: %s", got)
				return
			}
			if !reflect.DeepEqual(v1, v2) {
				t.Errorf("RepairJSON altered content semantically.\nInput: %s\nGot: %s", tt.input, got)
			}
		})
	}
}
