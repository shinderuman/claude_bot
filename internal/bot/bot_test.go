package bot

import (
	"testing"

	"claude_bot/internal/config"
	"claude_bot/internal/mastodon"

	gomastodon "github.com/mattn/go-mastodon"
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

func TestShouldHandleBroadcastCommand(t *testing.T) {
	tests := []struct {
		name             string
		broadcastCommand string
		content          string
		want             bool
	}{
		{
			name:             "Exact match (ignored as empty body)",
			broadcastCommand: "!all",
			content:          "!all",
			want:             false,
		},
		{
			name:             "Prefix match with no separator (ignored)",
			broadcastCommand: "!all",
			content:          "!allfoo",
			want:             false,
		},
		{
			name:             "Valid command with space separator",
			broadcastCommand: "!all",
			content:          "!all hello",
			want:             true,
		},
		{
			name:             "Valid command with newline separator",
			broadcastCommand: "!all",
			content:          "!all\nhello",
			want:             true,
		},
		{
			name:             "Valid command with HTML (stripped)",
			broadcastCommand: "!all",
			content:          "<p>!all hello</p>",
			want:             true,
		},
		{
			name:             "Command in middle (ignored)",
			broadcastCommand: "!all",
			content:          "hello !all",
			want:             false,
		},
		{
			name:             "Empty command config (ignored)",
			broadcastCommand: "",
			content:          "!all hello",
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				BroadcastCommand: tt.broadcastCommand,
			}
			bot := &Bot{
				config:         cfg,
				mastodonClient: &mastodon.Client{}, // Mock client (StripHTML is safe to call)
			}

			// Mock a status with HTML content if needed, but StripHTML handles plain text too
			// or we simulate what ExtractUserMessage/internal logic sees.
			// Bot.shouldHandleBroadcastCommand takes a status.
			status := &gomastodon.Status{
				Content: tt.content,
			}

			if got := bot.shouldHandleBroadcastCommand(status); got != tt.want {
				t.Errorf("shouldHandleBroadcastCommand() = %v, want %v (content: %q)", got, tt.want, tt.content)
			}
		})
	}
}

func TestIsBroadcastCommand(t *testing.T) {
	tests := []struct {
		name             string
		broadcastCommand string
		content          string
		want             bool
	}{
		{
			name:             "Exact match (ignored as empty body)",
			broadcastCommand: "!all",
			content:          "!all",
			want:             false,
		},
		{
			name:             "Prefix match with no separator (ignored)",
			broadcastCommand: "!all",
			content:          "!allfoo",
			want:             false,
		},
		{
			name:             "Valid command with space separator",
			broadcastCommand: "!all",
			content:          "!all hello",
			want:             true,
		},
		{
			name:             "Valid command with newline separator",
			broadcastCommand: "!all",
			content:          "!all\nhello",
			want:             true,
		},
		{
			name:             "Command in middle (ignored)",
			broadcastCommand: "!all",
			content:          "hello !all",
			want:             false,
		},
		{
			name:             "Empty command config (ignored)",
			broadcastCommand: "",
			content:          "!all hello",
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				BroadcastCommand: tt.broadcastCommand,
			}
			bot := &Bot{
				config: cfg,
			}

			if got := bot.isBroadcastCommand(tt.content); got != tt.want {
				t.Errorf("isBroadcastCommand() = %v, want %v (content: %q)", got, tt.want, tt.content)
			}
		})
	}
}
