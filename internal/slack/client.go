package slack

import (
	"context"
	"log"

	"github.com/slack-go/slack"
)

type Client struct {
	client         *slack.Client
	channelID      string
	errorChannelID string
	enabled        bool
}

// NewClient creates a new Slack client
func NewClient(token, channelID, errorChannelID string) *Client {
	if token == "" {
		return &Client{
			enabled: false,
		}
	}

	return &Client{
		client:         slack.New(token),
		channelID:      channelID,
		errorChannelID: errorChannelID,
		enabled:        true,
	}
}

// PostMessage sends a message to the configured Slack channel
func (c *Client) PostMessage(ctx context.Context, message string) error {
	return c.PostMessageToChannel(ctx, c.channelID, message)
}

// PostErrorMessage sends a message to the configured Error Slack channel
func (c *Client) PostErrorMessage(ctx context.Context, message string) error {
	return c.PostMessageToChannel(ctx, c.errorChannelID, message)
}

// PostMessageToChannel sends a message to a specific Slack channel
func (c *Client) PostMessageToChannel(ctx context.Context, channelID, message string) error {
	if !c.enabled {
		log.Printf("Slack通知スキップ (未設定)")
		return nil
	}

	// メッセージが空の場合は送信しない
	if message == "" {
		return nil
	}

	// MsgOptionText の第二引数は escape (true/false)
	_, _, err := c.client.PostMessageContext(ctx, channelID, slack.MsgOptionText(message, false))
	if err != nil {
		log.Printf("Slack投稿エラー: %v", err)
		return err
	}

	return nil
}

// PostMessageAsync sends a message asynchronously to the configured Slack channel
func (c *Client) PostMessageAsync(ctx context.Context, message string) {
	if !c.enabled {
		return
	}
	go func() {
		if err := c.PostMessage(ctx, message); err != nil {
			log.Printf("SlackAsyncPostError: %v", err)
		}
	}()
}
