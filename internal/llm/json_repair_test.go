package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// Tests for repairDoubleArray
func TestRepairDoubleArray(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Double array",
			input: `[[{"key":"value"}]]`,
			want:  `[{"key":"value"}]`,
		},
		{
			name:  "Single array (no change)",
			input: `[{"key":"value"}]`,
			want:  `[{"key":"value"}]`,
		},
		{
			name:  "Not an array (no change)",
			input: `{"key":"value"}`,
			want:  `{"key":"value"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repairDoubleArray(tt.input); got != tt.want {
				t.Errorf("repairDoubleArray() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Tests for repairTruncatedArray
func TestRepairTruncatedArray(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Simple truncation",
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
			input: `[{"a":`,
			want:  `[]`,
		},
		{
			name:  "Not truncated (no change)",
			input: `[{"a":1}]`,
			want:  `[{"a":1}]`,
		},
		{
			name:  "Not an array (no change)",
			input: `{"a":1}`,
			want:  `{"a":1}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repairTruncatedArray(tt.input); got != tt.want {
				t.Errorf("repairTruncatedArray() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Tests for repairStructural
func TestRepairStructural(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Missing closing brace",
			input: `{"key":"value"`,
			want:  `{"key":"value"}`,
		},
		{
			name:  "Missing closing bracket",
			input: `["a","b"`,
			want:  `["a","b"]`,
		},
		{
			name:  "Nested missing closers",
			input: `{"a":[{"b":1`,
			want:  `{"a":[{"b":1}]}`,
		},
		{
			name:  "Unescaped quote in string",
			input: `{"key":"val"ue"}`,
			want:  `{"key":"val\"ue"}`,
		},
		{
			name:  "Backslash outside string (ignored)",
			input: `{\"key\":\"value\"}`,
			want:  `{"key":"value"}`,
		},
		{
			name:  "Escaped structural quote",
			input: `{\"key\":\"value\"}`, // Treated same as above
			want:  `{"key":"value"}`,
		},
		{
			name:  "Wrapped escaped quotes (the bug case)",
			input: `{"key":"value\"}`, // "value\"}" -> "value"
			want:  `{"key":"value"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := repairStructural(tt.input)
			// For structural repair, check validity or exact string if simple
			// The tests above are simple enough for exact matching except potentially whitespace logic if any
			// repairStructural logic might write \n for newlines etc.
			// Let's check exact match first.
			// For buggy case `{"key":"value\"}`, repair should output `{"key":"value"}` (assuming `}` follows in real parsing or stack close)
			// Wait, `repairStructural` closes the stack.
			// Input `{"key":"value\"}` ->
			// '{' -> stack={'}'}
			// "key" -> key
			// :
			// "value\"... wait, if input ends there, `\"` followed by EOF -> isClosing=true
			// so it closes "value"
			// then loop ends.
			// Stack has '{', so appends '}'
			// Result: `{"key":"value"}`
			if got != tt.want {
				t.Errorf("repairStructural() = %q, want %q", got, tt.want)
			}
		})
	}
}

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
		{
			name:  "Double array input",
			input: `[[{"key":"value"}]]`,
			want:  `[{"key":"value"}]`,
		},
		{
			name: "Unescaped newline in value",
			input: `[{"key":"val
ue"}]`,
			want: `[{"key":"val\nue"}]`,
		},
		{
			name: "Complex reported failure case",
			// Original reported failure case
			input: `[[{"target":"deepseekroid","target_username":"白夜シエル","key":"attribute","value":"他人の苦しみを笑いものにする人を苦手と   る"},{"target":"deepseekroid","target_username":"白夜シエル☁️ ","key":"attribute","value":"約束を平気で破る人を苦手とする"},{"target":"deepseekroid","target_username":"白夜シエル☁️ ","key":"experience","value":"かつての貴族社会についての知識がある"},{"target":"deepseekroid","target_username":"白夜シエル☁️ ","key":"attribute","value":"
母から言葉に責任を持つことを教わった"}]]`,
			// Use expected standard JSON format for 'want'
			want: `[{"target":"deepseekroid","target_username":"白夜シエル","key":"attribute","value":"他人の苦しみを笑いものにする人を苦手と   る"},{"target":"deepseekroid","target_username":"白夜シエル☁️ ","key":"attribute","value":"約束を平気で破る人を苦手とする"},{"target":"deepseekroid","target_username":"白夜シエル☁️ ","key":"experience","value":"かつての貴族社会についての知識がある"},{"target":"deepseekroid","target_username":"白夜シエル☁️ ","key":"attribute","value":"\n母から言葉に責任を持つことを教わった"}]`,
		},
		{
			name:  "Object with unclosed array (The reported error)",
			input: `{"conflicting_fact_ids": ["id1", "id2" }`,
			want:  `{"conflicting_fact_ids": ["id1", "id2" ]}`,
		},
		{
			name:  "Object with unclosed array and missing comma",
			input: `{"key": ["val1", "val2"`,
			want:  `{"key": ["val1", "val2"]}`,
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

func TestRepairJSON_JapaneseChars(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Full-width colon",
			input: `[{"key"："value"}]`,
			want:  `[{"key":"value"}]`,
		},
		{
			name:  "Full-width colon and brackets for value",
			input: `[{"key"："「value」"}]`,
			want:  `[{"key":"value"}]`,
		},
		{
			name:  "Invalid character '}' after object key",
			input: `[{"key":"value"},{"key"："valid"}]`,
			want:  `[{"key":"value"},{"key":"valid"}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairJSON(tt.input)
			var v interface{}
			if err := json.Unmarshal([]byte(got), &v); err != nil {
				t.Errorf("Repaired JSON is invalid: %v\nInput: %s\nGot: %s", err, tt.input, got)
			}
		})
	}
}

func TestRepairJSON_SpecificFailure(t *testing.T) { // This function was correctly placed outside
	// Reproduce the specific error: invalid character 'ã' after object key
	input := `[{"target":"user_id","target_username":"unknown","key":"possession","value":"極太"},{"target":"assistant","target_username":"月詠アリア","key":"preference","value：「ピチュー」を可愛がっており、自身の宝物として大切にしている。}]`

	// Expected behavior: The PreprocessJSON should handle the full-width colon and quotes correctly,
	// and RepairJSON should ensure valid JSON structure.

	got := RepairJSON(input)

	var v interface{}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Errorf("Repaired JSON is invalid: %v\nInput: %s\nGot: %s", err, input, got)
	}
}

func TestRepairJSON_QuoteEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // approximate or specific check
	}{
		{
			name:  "Double quotes garbage",
			input: `{"key":"value""}`,
			want:  `{"key":"value"}`,
		},
		{
			name:  "Empty string (Regression check)",
			input: `{"key":""}`,
			want:  `{"key":""}`,
		},
		{
			name:  "Missing closing quote before comma",
			input: `{"key":"value,"next":1}`,
			want:  `{"key":"value","next":1}`,
		},
		{
			name:  "Nested object empty string",
			input: `{"obj":{"subkey":""}}`,
			want:  `{"obj":{"subkey":""}}`,
		},
		{
			name:  "Array empty string",
			input: `[""]`,
			want:  `[""]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairJSON(tt.input)
			// Normalize for comparison or just parse
			var v interface{}
			if err := json.Unmarshal([]byte(got), &v); err != nil {
				t.Errorf("RepairJSON(%q) -> %q (Invalid JSON: %v)", tt.input, got, err)
			}
			// String comparison for simple cases
			// Note: RepairJSON might change whitespace or encoding, so simple string check implies exact behavior
			// Since our logic is regex, it should be exact for these simple cases.
			// But RepairJSON loop mimics string building.
			// Let's check structure equality via Unmarshal unless we want exact string.
			// Actually, RepairJSON output should be valid.
			// Let's verify specifically that empty string wasn't broken into "}"
			if strings.Contains(got, `:"}`) {
				t.Errorf("RepairJSON(%q) broke empty string: %q", tt.input, got)
			}
		})
	}
}

func TestRepairJSON_UnescapedQuotes(t *testing.T) {
	// Reconstructed from user report
	// Case: Unescaped quotes inside a string value
	input := `[
  {"target":"__general__","target_username":"PlayStation Blog","key":"release","value":"「DualSense® ワイヤレスコントローラー "原神" リミテッドエディション」が2026年1月21日に発売。白、金、緑の配色で、ファンタジー世界のデザインが特徴。"},
  {"target":"__general__","target_username":"PlayStation Blog","key":"product","value":"「原神」新バージョン「Luna III」で新キャラクタードゥリンとナド・クライのストーリーアップデートが実装。"},
  {"target":"__general__","target_username":"PlayStation Blog","key":"news","value":"PlayStation®5での「原神」新バージョン「Luna III」がリリース。"}
]`

	got := RepairJSON(input)

	var v interface{}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Errorf("Repaired JSON is invalid: %v\n", err)
		t.Logf("Got: %s", got)
	} else {
		t.Logf("Repaired JSON is valid.")
	}
}

func TestRepairJSON_MultipleTopLevel(t *testing.T) {
	input := `[{"key":"value1"}][{"key":"value2"}]`
	want := `[{"key":"value1"}]`
	got := RepairJSON(input)
	if got != want {
		t.Errorf("RepairJSON() = %q, want %q", got, want)
	}

	// Test with spaces between arrays
	input2 := `[{"key":"value1"}]  [{"key":"value2"}]`
	want2 := `[{"key":"value1"}]`
	got2 := RepairJSON(input2)
	if got2 != want2 {
		t.Errorf("RepairJSON() with spaces = %q, want %q", got2, want2)
	}

	// Test with newline between arrays
	input3 := `[{"key":"value1"}]
[{"key":"value2"}]`
	want3 := `[{"key":"value1"}]`
	got3 := RepairJSON(input3)
	if got3 != want3 {
		t.Errorf("RepairJSON() with newline = %q, want %q", got3, want3)
	}
}

func TestRepairJSON_ExtraClosingBraceInArray(t *testing.T) {
	// Reproduction of reported bug: invalid character '}' after array element
	// Input ends with "}}" but opened with only "[{"
	input := `[{"target":"A","val":1}, {"target":"B","val":2}}`

	// Current buggy behavior produces: `[{"target":"A","val":1}, {"target":"B","val":2}]}`
	// Expected behavior: `[{"target":"A","val":1}, {"target":"B","val":2}]`

	got := RepairJSON(input)

	// Check validity
	var v interface{}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Errorf("Repaired JSON is invalid: %v\nInput: %s\nGot: %s", err, input, got)
	}

	// Check strictly expected string if valid
	want := `[{"target":"A","val":1}, {"target":"B","val":2}]`
	if got != want {
		t.Errorf("RepairJSON() = %q, want %q", got, want)
	}
}

func TestRepairJSON_MergedKey_Value(t *testing.T) {
	// Reproduction of reported bug: invalid character '}' after object key
	// Input has "value..." instead of "value":"..."
	input := `[{"target":"user","target_username":"unknown","key":"attribute","value自称メイドキャラクター"}]`

	// Expected behavior: insert missing colon and quotes
	got := RepairJSON(input)

	// Check validity
	var v interface{}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Errorf("Repaired JSON is invalid: %v\nInput: %s\nGot: %s", err, input, got)
	}

	// We expect the key "value" to be separated from the value "自称メイドキャラクター"
	// RepairJSON normalization might yield: "value":"自称メイドキャラクター" or similar
	if !strings.Contains(got, `"value":"自称メイドキャラクター"`) {
		t.Errorf("RepairJSON() failed to separate merged key-value. Got: %s", got)
	}
}

func TestRepairJSON_MissingOpeningQuote(t *testing.T) {
	// The problematic JSON string provided by the user.
	// Original: [{"target":"__general__","target_username":"GameSpark","key":"release","value":"Ghostcaseが手掛ける新作の一人称視点ホラーゲーム『悪意』が発表され、Steamストアページが公開された。"},{"target":"__general__","target_username":"GameSpark","key":"knowledge","value":『悪意』は、都会で一人暮らしする女性が引っ越し先の古いアパートに潜む"悪意"と向き合うホラーゲーム。"},{"target":"__general__","target_username":"GameSpark","key":"knowledge","value":『悪意』は日本語に対応する予定で、リリース日は未定。"}]
	input := `[{"target":"__general__","target_username":"GameSpark","key":"release","value":"Ghostcaseが手掛ける新作の一人称視点ホラーゲーム『悪意』が発表され、Steamストアページが公開された。"},{"target":"__general__","target_username":"GameSpark","key":"knowledge","value":『悪意』は、都会で一人暮らしする女性が引っ越し先の古いアパートに潜む"悪意"と向き合うホラーゲーム。"},{"target":"__general__","target_username":"GameSpark","key":"knowledge","value":『悪意』は日本語に対応する予定で、リリース日は未定。"}]`

	// Attempt to repair
	repaired := RepairJSON(input)

	// Attempt to unmarshal
	var result []map[string]string
	err := json.Unmarshal([]byte(repaired), &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal repaired JSON: %v\nRepaired: %s", err, repaired)
	}

	// Verify content of the second item to ensure it was parsed correctly
	if len(result) < 2 {
		t.Fatalf("Expected at least 2 items, got %d", len(result))
	}

	expectedValueStart := "『悪意』は"
	actualValue := result[1]["value"]
	// Note: Use runes for correct slicing if checking prefix length precisely, but string slicing works for simple byte prefix check if encoded same
	// Let's use strings.HasPrefix which is safer
	if !strings.HasPrefix(actualValue, expectedValueStart) {
		t.Errorf("Expected value to start with %q, got %q", expectedValueStart, actualValue)
	}
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

// --- Detailed Structural Tests ---

func TestStructuralRepairer_HandleBackslash(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		pos      int
		inString bool
		wantBuf  string
		wantPos  int
		wantEsc  bool
		wantIn   bool
	}{
		{
			name:     "Ignore backslash outside string",
			input:    `\t`,
			pos:      0,
			inString: false,
			wantBuf:  "",
			wantPos:  1,
			wantEsc:  false,
			wantIn:   false,
		},
		{
			name:     "Standard escape inside string",
			input:    `\t`,
			pos:      0,
			inString: true,
			wantBuf:  `\`,
			wantPos:  1,
			wantEsc:  true,
			wantIn:   true,
		},
		{
			name:     "Escaped delimiter inside string (closing quote)",
			input:    `\"}`,
			pos:      0,
			inString: true,
			wantBuf:  `"`,
			wantPos:  2, // skipped \ and "
			wantEsc:  false,
			wantIn:   false,
		},
		{
			name:     "Escaped quote NOT followed by delimiter (internal quote)",
			input:    `\"a`,
			pos:      0,
			inString: true,
			wantBuf:  `\`,
			wantPos:  1,
			wantEsc:  true,
			wantIn:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &structuralRepairer{
				runes:    []rune(tt.input),
				pos:      tt.pos,
				inString: tt.inString,
			}
			r.handleBackslash()

			if r.sb.String() != tt.wantBuf {
				t.Errorf("buf = %q, want %q", r.sb.String(), tt.wantBuf)
			}
			if r.pos != tt.wantPos {
				t.Errorf("pos = %d, want %d", r.pos, tt.wantPos)
			}
			if r.escaped != tt.wantEsc {
				t.Errorf("escaped = %v, want %v", r.escaped, tt.wantEsc)
			}
			if r.inString != tt.wantIn {
				t.Errorf("inString = %v, want %v", r.inString, tt.wantIn)
			}
		})
	}
}

func TestStructuralRepairer_HandleQuote(t *testing.T) {
	tests := []struct {
		name     string
		input    string // string starting from current pos
		pos      int
		inString bool
		wantBuf  string
		wantIn   bool
	}{
		{
			name:     "Start string",
			input:    `"foo"`,
			pos:      0,
			inString: false,
			wantBuf:  `"`,
			wantIn:   true,
		},
		{
			name:     "End string (followed by delimiter)",
			input:    `",`,
			pos:      0,
			inString: true,
			wantBuf:  `"`,
			wantIn:   false,
		},
		{
			name:     "Internal quote (not followed by delimiter)",
			input:    `"foo`, // " followed by f
			pos:      0,
			inString: true,
			wantBuf:  `\"`,
			wantIn:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &structuralRepairer{
				runes:    []rune(tt.input),
				pos:      tt.pos,
				inString: tt.inString,
			}
			r.handleQuote()

			if r.sb.String() != tt.wantBuf {
				t.Errorf("buf = %q, want %q", r.sb.String(), tt.wantBuf)
			}
			if r.inString != tt.wantIn {
				t.Errorf("inString = %v, want %v", r.inString, tt.wantIn)
			}
		})
	}
}

func TestStructuralRepairer_HandleStructure(t *testing.T) {
	tests := []struct {
		name      string
		inputRune rune
		stack     []rune
		wantStack []rune
		wantBuf   string
		wantDone  bool
	}{
		{
			name:      "Push {",
			inputRune: '{',
			stack:     nil,
			wantStack: []rune{'{'},
			wantBuf:   "{",
			wantDone:  false,
		},
		{
			name:      "Pop } from {",
			inputRune: '}',
			stack:     []rune{'{'},
			wantStack: []rune{},
			wantBuf:   "}",
			wantDone:  true,
		},
		{
			name:      "Pop } from {[ (insert ])",
			inputRune: '}',
			stack:     []rune{'{', '['},
			wantStack: []rune{}, // pops [ then {
			wantBuf:   "]}",
			wantDone:  true,
		},
		{
			name:      "Pop ] from { (insert })",
			inputRune: ']',
			stack:     []rune{'{'},
			wantStack: []rune{},
			wantBuf:   "}]",
			wantDone:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &structuralRepairer{
				stack: tt.stack,
			}
			_, done := r.handleStructure(tt.inputRune)

			if done != tt.wantDone {
				t.Errorf("done = %v, want %v", done, tt.wantDone)
			}
			if r.sb.String() != tt.wantBuf {
				t.Errorf("buf = %q, want %q", r.sb.String(), tt.wantBuf)
			}
			// Comparison of slices requires loop or helpers
			if len(r.stack) != len(tt.wantStack) {
				t.Errorf("stack len = %d, want %d", len(r.stack), len(tt.wantStack))
			} else {
				for i := range r.stack {
					if r.stack[i] != tt.wantStack[i] {
						t.Errorf("stack[%d] = %c, want %c", i, r.stack[i], tt.wantStack[i])
					}
				}
			}
		})
	}
}

// --- Fuzz Tests ---

// FuzzRepairJSON tests that RepairJSON never panics and preserves valid JSON (mostly).
func FuzzRepairJSON(f *testing.F) {
	// Seed corpus with known edge cases
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
		// 1. Property: Should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("RepairJSON panicked on input %q: %v", input, r)
			}
		}()

		repaired := RepairJSON(input)

		// 2. Property: If input was already valid JSON (and standard format),
		// RepairJSON should ideally keep it valid and structurally similar.
		if json.Valid([]byte(input)) {
			if !json.Valid([]byte(repaired)) {
				t.Errorf("RepairJSON broke valid JSON.\nInput: %q\nOutput: %q", input, repaired)
			}
		}
	})
}

// FuzzStructuralRepair tests specifically the structural repairer logic
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
				t.Fatalf("repairStructural panicked on input %q: %v", input, r)
			}
		}()

		// Just run it to ensure no panics and termination
		_ = repairStructural(input)
	})
}
