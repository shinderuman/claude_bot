package bot

import (
	"testing"
)

func TestExtractIDFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://mastodon.social/@user/123456789", "123456789"},
		{"https://example.com/users/test/statuses/111", "111"},
		{"https://mastodon.social/@user/abc", ""},
		{"https://mastodon.social/@user/123a456", ""},
		{"invalid-url", ""},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := extractIDFromURL(tt.url); got != tt.want {
				t.Errorf("extractIDFromURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassifyIntent_RelativeDates(t *testing.T) {
	// 実際のLLMをモックする必要があるが、ここではプロンプトの生成ロジックと
	// 想定されるJSON出力に対するパースロジックをテストするのではなく、
	// 実際のLLMの挙動をシミュレートする単体テストは難しい。
	// 代わりに、bot.goのclassifyIntentが返す値を想定したロジックテストを行うべきだが、
	// ここでは意図判定のプロンプトが適切かどうかが問題。
	// プロンプトの修正が必要。
}
