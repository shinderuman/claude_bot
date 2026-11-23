package discovery

import (
	"context"
	"log"

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
		log.Printf("Peer認証キー生成失敗: %v", err)
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
	registryPath, err := util.DataFilePath(RegistryFileName)
	if err != nil {
		return err
	}
	registry, err := readRegistry(registryPath)
	if err != nil {
		return err
	}

	for _, node := range registry.Nodes {
		username := node.Username
		if username == pd.myUsername {
			continue
		}

		account, err := pd.mastodonClient.GetAccountByUsername(ctx, username)
		if err != nil {
			log.Printf("Peerアカウント取得失敗: %s、%v", username, err)
			continue
		}

		if !pd.IsPeer(account) {
			log.Printf("Peer認証失敗: %s、ハッシュ不一致", username)
			continue
		}

		if onPeerFound != nil {
			onPeerFound(ctx, account)
		}

		isFollowing, err := pd.mastodonClient.IsFollowing(ctx, string(account.ID))
		if err != nil {
			log.Printf("Peerのフォロー確認失敗: %s、%v", username, err)
			continue
		}

		if !isFollowing {
			log.Printf("新規Peer発見・自動フォロー開始: %s", username)
			if err := pd.mastodonClient.FollowAccount(ctx, string(account.ID)); err != nil {
				log.Printf("Peer自動フォロー失敗: %s、%v", username, err)
			}
		}
	}

	return nil
}
