package mastodon

import (
	"strings"
	"testing"

	"claude_bot/internal/config"

	"github.com/mattn/go-mastodon"
)

// NOTE: これらのテストは、facts/service.go にある既存ロジックと100%一致することを確認するためのものです。
// 集約（移動）した際、これらのテストをそのまま通るように実装します。

func TestFormatProfileText(t *testing.T) {
	const DisclaimerText = "\n\n※このアカウントの投稿には事実に基づく内容が含まれることもありますが、すべての正確性は保証できません。"
	const MaxMastodonProfileChars = 500

	c := &Client{}

	tests := []struct {
		name    string
		input   string
		wantEnd string
		wantLen int
	}{
		{
			name:    "Short text",
			input:   "Short profile.",
			wantEnd: DisclaimerText,
		},
		{
			name:    "Text with excessive newlines",
			input:   "Line 1\n\n\nLine 2",
			wantEnd: DisclaimerText,
		},
		{
			name:    "Long text truncation",
			input:   strings.Repeat("あ", 600),
			wantEnd: DisclaimerText,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.FormatProfileText(tt.input)

			if len([]rune(got)) > MaxMastodonProfileChars {
				t.Errorf("Length %d > %d", len([]rune(got)), MaxMastodonProfileChars)
			}
			if !strings.HasSuffix(got, tt.wantEnd) {
				t.Errorf("Disclaimer missing. Got suffix: %s", got)
			}
		})
	}
}

func TestBuildProfileFields(t *testing.T) {
	c := &Client{}
	cfg := &config.Config{
		AllowRemoteUsers: true,
		Timezone:         "Asia/Tokyo",
	}
	authKey := "test-auth-key"
	existingFields := []mastodon.Field{
		{Name: "Existing", Value: "Val"},
	}

	got := c.BuildProfileFields(cfg, existingFields, authKey)

	// フィールド数の確認 (Existing + SystemID + Status + Updated = 4)
	if len(got) != 4 {
		t.Errorf("Expected 4 fields, got %d", len(got))
	}

	// 順序の確認
	if got[0].Name != "Existing" {
		t.Errorf("First field should be preserved. Got %s", got[0].Name)
	}
	if got[1].Name != ProfileFieldSystemID {
		t.Errorf("Second field should be SystemID. Got %s", got[1].Name)
	}
	if got[2].Name != ProfileFieldMentionStatus {
		t.Errorf("Third field should be MentionStatus. Got %s", got[2].Name)
	}
	if got[3].Name != ProfileFieldLastUpdated {
		t.Errorf("Fourth field should be LastUpdated. Got %s", got[3].Name)
	}
}

func TestExtractCleanProfileNote(t *testing.T) {
	const DisclaimerText = "\n\n※このアカウントの投稿には事実に基づく内容が含まれることもありますが、すべての正確性は保証できません。"
	c := &Client{}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Note with disclaimer",
			input: "This is a profile." + DisclaimerText,
			want:  "This is a profile.",
		},
		{
			name:  "Note with HTML and disclaimer",
			input: "<p>This is a <b>profile</b>.</p>" + DisclaimerText,
			want:  "This is a profile.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.ExtractCleanProfileNote(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
