package discovery

import (
	"claude_bot/internal/mastodon"
	"testing"

	gomastodon "github.com/mattn/go-mastodon"
)

func TestIsPeer(t *testing.T) {
	// Setup
	authKey, err := GetPeerAuthKey()
	if err != nil {
		t.Fatalf("Failed to get auth key: %v", err)
	}

	client := &mastodon.Client{} // Mock client not needed for IsPeer
	pd := NewPeerDiscoverer(client, "bot_user")

	tests := []struct {
		name     string
		account  *gomastodon.Account
		expected bool
	}{
		{
			name: "Valid Peer",
			account: &gomastodon.Account{
				Fields: []gomastodon.Field{
					{Name: PeerAuthFieldKey, Value: authKey},
				},
			},
			expected: true,
		},
		{
			name: "Invalid Peer (Wrong Key)",
			account: &gomastodon.Account{
				Fields: []gomastodon.Field{
					{Name: PeerAuthFieldKey, Value: "wrong_key"},
				},
			},
			expected: false,
		},
		{
			name: "Invalid Peer (No Key)",
			account: &gomastodon.Account{
				Fields: []gomastodon.Field{
					{Name: "Other", Value: "Value"},
				},
			},
			expected: false,
		},
		{
			name:     "Nil Account",
			account:  nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pd.IsPeer(tt.account); got != tt.expected {
				t.Errorf("IsPeer() = %v, want %v", got, tt.expected)
			}
		})
	}
}
