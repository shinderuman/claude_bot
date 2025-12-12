package mastodon

import (
	"testing"

	"github.com/mattn/go-mastodon"
)

func TestShouldCollectFactsFromStatus(t *testing.T) {
	tests := []struct {
		name   string
		status *mastodon.Status
		want   bool
	}{
		{
			name: "Public + Human + URL -> Allow",
			status: &mastodon.Status{
				Visibility: "public",
				Account:    mastodon.Account{Bot: false},
				Content:    "Check this out: https://example.com/news",
			},
			want: true,
		},
		{
			name: "Public + Bot + URL -> Allow",
			status: &mastodon.Status{
				Visibility: "public",
				Account:    mastodon.Account{Bot: true},
				Content:    "New update: https://example.com/update",
			},
			want: true,
		},
		{
			name: "Unlisted + Human + URL -> Block (Policy: Humans in unlisted ignored)",
			status: &mastodon.Status{
				Visibility: "unlisted",
				Account:    mastodon.Account{Bot: false},
				Content:    "Secret link: https://example.com/secret",
			},
			want: false,
		},
		{
			name: "Unlisted + Bot + URL -> Allow (Policy: Bots in unlisted allowed)",
			status: &mastodon.Status{
				Visibility: "unlisted",
				Account:    mastodon.Account{Bot: true},
				Content:    "Automated alert: https://example.com/alert",
			},
			want: true,
		},
		{
			name: "Private + Human + URL -> Block",
			status: &mastodon.Status{
				Visibility: "private",
				Account:    mastodon.Account{Bot: false},
				Content:    "Private: https://example.com/private",
			},
			want: false,
		},
		{
			name: "Direct + Human + URL -> Block",
			status: &mastodon.Status{
				Visibility: "direct",
				Account:    mastodon.Account{Bot: false},
				Content:    "DM: https://example.com/dm",
			},
			want: false,
		},
		{
			name: "Public + Human + No URL -> Block",
			status: &mastodon.Status{
				Visibility: "public",
				Account:    mastodon.Account{Bot: false},
				Content:    "Just talking",
			},
			want: false,
		},
		{
			name: "Public + Human + URL + Mention -> Block (Mentions excluded from fact collection)",
			status: &mastodon.Status{
				Visibility: "public",
				Account:    mastodon.Account{Bot: false},
				Content:    "@bot Hey check https://example.com",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldCollectFactsFromStatus(tt.status)
			if got != tt.want {
				t.Errorf("ShouldCollectFactsFromStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}
