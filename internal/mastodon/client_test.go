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
