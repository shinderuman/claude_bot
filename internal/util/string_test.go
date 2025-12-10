package util

import "testing"

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
			if got := ExtractIDFromURL(tt.url); got != tt.want {
				t.Errorf("ExtractIDFromURL() = %v, want %v", got, tt.want)
			}
		})
	}
}
