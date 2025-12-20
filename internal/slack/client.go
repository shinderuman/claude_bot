package slack

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// Variables for testability
var (
	maxBackoffDelay = 1 * time.Minute
	maxTotalTimeout = 10 * time.Minute
	initialBackoff  = 1 * time.Second
)

type Client struct {
	client         *slack.Client
	channelID      string
	errorChannelID string
	botUsername    string
	enabled        bool
}

// NewClient creates a new Slack client
func NewClient(token, channelID, errorChannelID, botUsername string) *Client {
	if token == "" {
		return &Client{
			enabled: false,
		}
	}

	return &Client{
		client:         slack.New(token),
		channelID:      channelID,
		errorChannelID: errorChannelID,
		botUsername:    botUsername,
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

	// BotUsernameを付与
	if c.botUsername != "" {
		message = fmt.Sprintf("[%s] %s", c.botUsername, message)
	}

	return c.postMessageWithRetry(ctx, channelID, message)
}

// postMessageWithRetry handles the actual sending with exponential backoff for rate limits
func (c *Client) postMessageWithRetry(ctx context.Context, channelID, message string) error {
	startTime := time.Now()
	backoff := initialBackoff

	for {
		// MsgOptionText の第二引数は escape (true/false)
		_, _, err := c.client.PostMessageContext(ctx, channelID, slack.MsgOptionText(message, false))
		if err == nil {
			return nil
		}

		// 429以外のエラーは即時終了
		if !isRateLimitedError(err) {
			log.Printf("Slack投稿エラー: %v", err)
			return err
		}

		// タイムアウト判定
		if shouldStopRetry(startTime) {
			return nil // Silent failure on timeout
		}

		// バックオフ待機
		sleepDuration := min(backoff, maxBackoffDelay)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleepDuration):
			// 次回のバックオフ時間を計算
			backoff = min(time.Duration(float64(backoff)*2), maxBackoffDelay)
		}
	}
}

// shouldStopRetry checks if we should stop retrying due to timeout
func shouldStopRetry(startTime time.Time) bool {
	// Check total timeout
	if time.Since(startTime) > maxTotalTimeout {
		return true
	}
	return false
}

func isRateLimitedError(err error) bool {
	if err == nil {
		return false
	}
	// slack-go often returns a specific error type or string for rate limits
	if rateLimitedError, ok := err.(*slack.RateLimitedError); ok {
		return rateLimitedError.Retryable()
	}
	// Fallback check for error message string just in case
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "ratelimited") || strings.Contains(msg, "rate limit") || strings.Contains(msg, "too many requests") || strings.Contains(msg, "429")
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
