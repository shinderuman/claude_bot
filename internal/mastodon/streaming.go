package mastodon

import (
	"context"
	"log"
	"strings"

	"github.com/mattn/go-mastodon"
)

// StreamPublic は連合タイムラインのストリーミングを開始し、イベントをチャネルに送信します
func (c *Client) StreamPublic(ctx context.Context, eventChan chan<- mastodon.Event) {
	events, err := c.client.StreamingPublic(ctx, false) // false = 連合タイムライン
	if err != nil {
		log.Printf("連合ストリーミング接続エラー: %v", err)
		return
	}

	log.Println("連合ストリーミング接続成功")

	for event := range events {
		eventChan <- event
	}

	log.Println("連合ストリーミング接続が切断されました")
}

// ExtractStatusFromEvent はイベントから Status を抽出します
func ExtractStatusFromEvent(event mastodon.Event) *mastodon.Status {
	switch e := event.(type) {
	case *mastodon.UpdateEvent:
		return e.Status
	case *mastodon.NotificationEvent:
		return e.Notification.Status
	default:
		return nil
	}
}

// ShouldCollectFactsFromStatus はファクト収集対象の投稿かを判定します
// - 本文に実際のURLを含む(http://またはhttps://)
// - メンションを含まない
func ShouldCollectFactsFromStatus(status *mastodon.Status) bool {
	if status == nil {
		return false
	}

	content := string(status.Content)

	// メンションを含む投稿は除外
	if strings.Contains(content, "@") {
		return false
	}

	// 本文にURLパターンが含まれるかチェック
	// MediaAttachmentsやCardだけでは不十分(ハッシュタグなどもCardになるため)
	// 実際のhttp://またはhttps://を含む投稿のみ対象
	return strings.Contains(content, "http://") || strings.Contains(content, "https://")
}
