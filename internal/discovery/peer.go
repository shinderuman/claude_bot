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

type PeerDiscoverer struct {
	mastodonClient *mastodon.Client
	myUsername     string
}

func NewPeerDiscoverer(client *mastodon.Client, myUsername string) *PeerDiscoverer {
	return &PeerDiscoverer{
		mastodonClient: client,
		myUsername:     myUsername,
	}
}

func (pd *PeerDiscoverer) IsPeer(account *gomastodon.Account) bool {
	if account == nil {
		return false
	}

	authKey, err := GetPeerAuthKey()
	if err != nil {
		log.Printf("Peer認証キー生成エラー: %v", err)
		return false
	}

	for _, field := range account.Fields {
		if field.Name == mastodon.ProfileFieldSystemID {
			if field.Value == authKey {
				return true
			}
		}
	}

	return false
}

func (pd *PeerDiscoverer) DiscoverPeersFromRegistry(ctx context.Context, onPeerFound func(ctx context.Context, account *gomastodon.Account)) error {
	registryPath := util.GetFilePath(RegistryFileName)
	data, err := os.ReadFile(registryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
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
		if username == pd.myUsername {
			continue
		}

		account, err := pd.mastodonClient.GetAccountByUsername(ctx, username)
		if err != nil {
			log.Printf("Peer探索: ユーザー %s の取得失敗: %v", username, err)
			continue
		}

		if !pd.IsPeer(account) {
			log.Printf("Peer探索: ユーザー %s は正規のPeerではありません (Hash不一致)", username)
			continue
		}

		if onPeerFound != nil {
			onPeerFound(ctx, account)
		}

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
