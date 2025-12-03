package fetcher

import "testing"

func TestIsNoiseURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://mastodon.social/@shinderuman", true},
		{"https://mastodon.social/@shinderuman/", true},
		{"https://mastodon.social/@shinderuman/1234567890", false},
		{"https://mastodon.social/users/shinderuman", true},
		{"https://mastodon.social/tags/golang", true},
		{"https://example.com", true},
		{"https://example.com/", true},
		{"https://example.com/article", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := IsNoiseURL(tt.url); got != tt.want {
				t.Errorf("IsNoiseURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
