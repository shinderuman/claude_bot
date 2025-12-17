package discovery

import (
	"context"
	"encoding/json"
	"log"
	"os"

	"claude_bot/internal/mastodon"
	"claude_bot/internal/util"

	gomastodon "github.com/mattn/go-mastodon"
)

const (
	// PeerAuthFieldKey is the key name for the peer authentication hash in the profile fields
	PeerAuthFieldKey = "SystemID"
)

// PeerDiscoverer handles the discovery and authentication of peer bots
type PeerDiscoverer struct {
	mastodonClient *mastodon.Client
	myUsername     string
}

// NewPeerDiscoverer creates a new PeerDiscoverer
func NewPeerDiscoverer(client *mastodon.Client, myUsername string) *PeerDiscoverer {
	return &PeerDiscoverer{
		mastodonClient: client,
		myUsername:     myUsername,
	}
}

// IsPeer checks if the given account is a valid peer by verifying the auth key hash
func (pd *PeerDiscoverer) IsPeer(account *gomastodon.Account) bool {
	if account == nil {
		return false
	}

	authKey, err := util.GetPeerAuthKey()
	if err != nil {
		log.Printf("Peer認証キー生成エラー: %v", err)
		return false
	}

	for _, field := range account.Fields {
		if field.Name == PeerAuthFieldKey {
			if field.Value == authKey {
				return true
			}
		}
	}

	return false
}

// DiscoverPeersFromRegistry reads the cluster registry and follows unknown peers
func (pd *PeerDiscoverer) DiscoverPeersFromRegistry(ctx context.Context) error {
	registryPath := util.GetFilePath(RegistryFileName)
	data, err := os.ReadFile(registryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Registry doesn't exist yet, nothing to discover
		}
		return err
	}

	var registry struct {
		Nodes []struct {
			Username string `json:"username"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return err
	}

	for _, node := range registry.Nodes {
		username := node.Username
		// Skip self
		if username == pd.myUsername {
			continue
		}

		// Check if already following
		// Note: This requires resolving username to ID first, which we might not have efficiently.
		// So we'll fetch the account first.
		account, err := pd.mastodonClient.GetAccountByUsername(ctx, username)
		if err != nil {
			log.Printf("Peer探索: ユーザー %s の取得失敗: %v", username, err)
			continue
		}

		// Verify if it's a valid peer (has the correct hash)
		if !pd.IsPeer(account) {
			log.Printf("Peer探索: ユーザー %s は正規のPeerではありません (Hash不一致)", username)
			continue
		}

		// Check relationship
		isFollowing, err := pd.mastodonClient.IsFollowing(ctx, string(account.ID))
		if err != nil {
			log.Printf("Peer探索: フォロー状態確認失敗 (%s): %v", username, err)
			continue
		}

		if !isFollowing {
			log.Printf("Peer探索: 新しいPeerを発見しました: %s -> 自動フォローを実行します", username)
			if err := pd.mastodonClient.FollowAccount(ctx, string(account.ID)); err != nil {
				log.Printf("Peer探索: 自動フォロー失敗 (%s): %v", username, err)
			}
		}
	}

	return nil
}
