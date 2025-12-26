package mastodon

import (
	"strings"
	"testing"
)

func TestSplitResponse(t *testing.T) {
	mention := "@user "

	tests := []struct {
		name     string
		response string
		want     int
	}{
		{
			name:     "短い応答",
			response: "こんにちは",
			want:     1,
		},
		{
			name:     "480文字以内",
			response: strings.Repeat("あ", 470),
			want:     1,
		},
		{
			name:     "改行で分割可能",
			response: strings.Repeat("あ", 470) + "\n" + strings.Repeat("い", 100),
			want:     2,
		},
		{
			name:     "改行なしで長い",
			response: strings.Repeat("あ", 1000),
			want:     3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := splitResponse(tt.response, mention, 480)
			if len(parts) != tt.want {
				t.Errorf("splitResponse() = %d parts, want %d parts", len(parts), tt.want)
			}

			for i, part := range parts {
				contentLen := len([]rune(mention + part))
				if contentLen > 500 {
					t.Errorf("part %d length = %d, exceeds 500 characters", i, contentLen)
				}
			}
		})
	}
}

func TestFindLastNewline(t *testing.T) {
	tests := []struct {
		name  string
		runes []rune
		start int
		end   int
		want  int
	}{
		{
			name:  "改行あり",
			runes: []rune("あいう\nえお"),
			start: 0,
			end:   5,
			want:  3,
		},
		{
			name:  "改行なし",
			runes: []rune("あいうえお"),
			start: 0,
			end:   5,
			want:  -1,
		},
		{
			name:  "複数改行",
			runes: []rune("あ\nい\nう"),
			start: 0,
			end:   5,
			want:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findLastNewline(tt.runes, tt.start, tt.end)
			if got != tt.want {
				t.Errorf("findLastNewline() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestStripHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Simple P tag",
			input: "<p>Hello</p>",
			want:  "Hello",
		},
		{
			name:  "With BR",
			input: "<p>Line1<br />Line2</p>",
			want:  "Line1\nLine2",
		},
		{
			name:  "Complex",
			input: "<p>Link: <a href=\"example.com\">Example</a></p>",
			want:  "Link: Example",
		},
		{
			name:  "No HTML",
			input: "Plain Text",
			want:  "Plain Text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTML(tt.input)
			if got != tt.want {
				t.Errorf("stripHTML() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncateText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		limit int
		want  string
	}{
		{
			name:  "Within limit",
			input: "abc",
			limit: 5,
			want:  "abc",
		},
		{
			name:  "Exceed limit (simple)",
			input: "abcde",
			limit: 3,
			want:  "abc", // "abc" (3 chars)
		},
		{
			name:  "Exceed limit (cut at period)",
			input: "あいうえお。かきくけこ。",
			limit: 8,
			want:  "あいうえお。", // 6 chars (including period)
		},
		{
			name:  "Cut at newline",
			input: "Line1\nLine2",
			limit: 8,
			want:  "Line1\n", // "Line1\nLi" -> cut at \n
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateText(tt.input, tt.limit)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
