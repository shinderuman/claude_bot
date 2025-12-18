package slack

import (
	"context"
	"log"

	"github.com/slack-go/slack"
)

type Client struct {
	client    *slack.Client
	channelID string
	enabled   bool
}

// NewClient creates a new Slack client
func NewClient(token, channelID string) *Client {
	if token == "" || channelID == "" {
		return &Client{
			enabled: false,
		}
	}

	return &Client{
		client:    slack.New(token),
		channelID: channelID,
		enabled:   true,
	}
}

// PostMessage sends a message to the configured Slack channel
func (c *Client) PostMessage(ctx context.Context, message string) error {
	if !c.enabled {
		log.Printf("Slack通知スキップ (未設定)")
		return nil
	}

	// メッセージが空の場合は送信しない
	if message == "" {
		return nil
	}

	// MsgOptionText の第二引数は escape (true/false)
	_, _, err := c.client.PostMessageContext(ctx, c.channelID, slack.MsgOptionText(message, false))
	if err != nil {
		log.Printf("Slack投稿エラー: %v", err)
		return err
	}

	return nil
}
