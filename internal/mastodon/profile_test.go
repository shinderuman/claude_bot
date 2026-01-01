package mastodon

import (
	"testing"

	"claude_bot/internal/config"

	"github.com/mattn/go-mastodon"
)

// NOTE: これらのテストは、facts/service.go にある既存ロジックと100%一致することを確認するためのものです。
// 集約（移動）した際、これらのテストをそのまま通るように実装します。

func TestFormatProfileBody(t *testing.T) {
	c := &Client{}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Short text",
			input: "Line 1\n\n\nLine 2",
			want:  "Line 1\nLine 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.FormatProfileBody(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildProfileFields(t *testing.T) {
	c := &Client{}
	cfg := &config.Config{
		AllowRemoteUsers: true,
		Timezone:         "Asia/Tokyo",
		LLMProvider:      "gemini",
		GeminiModel:      "gemini-1.5-pro",
	}
	authKey := "test-auth-key"
	existingFields := []mastodon.Field{
		{Name: "Existing", Value: "Val"},
	}

	got := c.BuildProfileFields(cfg, existingFields, authKey)

	// フィールド数の確認 (Existing + ModelName + SystemID + Status + Updated = 5)
	if len(got) != 5 {
		t.Errorf("Expected 5 fields, got %d", len(got))
	}

	// 順序の確認
	if got[0].Name != "Existing" {
		t.Errorf("First field should be preserved. Got %s", got[0].Name)
	}

	// SystemID Check
	if got[1].Name != ProfileFieldSystemID {
		t.Errorf("Second field should be SystemID. Got %s", got[1].Name)
	}

	// MentionStatus Check
	if got[2].Name != ProfileFieldMentionStatus {
		t.Errorf("Third field should be MentionStatus. Got %s", got[2].Name)
	}

	// LastUpdated Check
	if got[3].Name != ProfileFieldLastUpdated {
		t.Errorf("Fourth field should be LastUpdated. Got %s", got[3].Name)
	}

	// Model Name Check
	if got[4].Name != ProfileFieldModelName {
		t.Errorf("Fifth field should be ModelName. Got %s", got[4].Name)
	}
	if got[4].Value != "gemini-1.5-pro" {
		t.Errorf("ModelName value incorrect. Got %s", got[4].Value)
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
