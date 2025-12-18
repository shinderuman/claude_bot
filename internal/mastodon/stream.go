package mastodon

import (
	"context"
	"log"

	gomastodon "github.com/mattn/go-mastodon"
)

// StreamUser はホームタイムラインのストリーミングを開始し、イベントをチャネルに送信します
func (c *Client) StreamUser(ctx context.Context, eventChan chan<- gomastodon.Event) {
	events, err := c.client.StreamingUser(ctx)
	if err != nil {
		log.Printf("ユーザーストリーミング接続エラー: %v", err)
		return
	}

	log.Println("ユーザーストリーミング接続成功")

	for event := range events {
		eventChan <- event
	}

	log.Println("ユーザーストリーミング接続が切断されました")
}

// StreamPublic は連合タイムラインのストリーミングを開始し、イベントをチャネルに送信します
func (c *Client) StreamPublic(ctx context.Context, eventChan chan<- gomastodon.Event) {
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
func ExtractStatusFromEvent(event gomastodon.Event) *gomastodon.Status {
	switch e := event.(type) {
	case *gomastodon.UpdateEvent:
		return e.Status
	case *gomastodon.NotificationEvent:
		return e.Notification.Status
	default:
		return nil
	}
}
