package slack

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

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

func (c *Client) PostMessage(ctx context.Context, message string) error {
	return c.PostMessageToChannel(ctx, c.channelID, message)
}

func (c *Client) PostErrorMessage(ctx context.Context, message string) error {
	return c.PostMessageToChannel(ctx, c.errorChannelID, message)
}

func (c *Client) PostMessageToChannel(ctx context.Context, channelID, message string) error {
	if !c.enabled {
		log.Printf("Slack通知スキップ (未設定)")
		return nil
	}

	if message == "" {
		return nil
	}

	if c.botUsername != "" {
		message = fmt.Sprintf("[%s] %s", c.botUsername, message)
	}

	return c.postMessageWithRetry(ctx, channelID, message)
}

func (c *Client) postMessageWithRetry(ctx context.Context, channelID, message string) error {
	startTime := time.Now()
	backoff := initialBackoff

	for {
		_, _, err := c.client.PostMessageContext(ctx, channelID, slack.MsgOptionText(message, false))
		if err == nil {
			return nil
		}

		if !isRateLimitedError(err) {
			log.Printf("Slack投稿エラー: %v", err)
			return err
		}

		if shouldStopRetry(startTime) {
			return nil
		}

		sleepDuration := min(backoff, maxBackoffDelay)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleepDuration):
			backoff = min(time.Duration(float64(backoff)*2), maxBackoffDelay)
		}
	}
}

func shouldStopRetry(startTime time.Time) bool {
	if time.Since(startTime) > maxTotalTimeout {
		return true
	}
	return false
}

func isRateLimitedError(err error) bool {
	if err == nil {
		return false
	}
	if rateLimitedError, ok := err.(*slack.RateLimitedError); ok {
		return rateLimitedError.Retryable()
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "ratelimited") || strings.Contains(msg, "rate limit") || strings.Contains(msg, "too many requests") || strings.Contains(msg, "429")
}

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

func (c *Client) PostErrorMessageAsync(ctx context.Context, message string) {
	if !c.enabled {
		return
	}
	go func() {
		if err := c.PostErrorMessage(ctx, message); err != nil {
			log.Printf("SlackAsyncErrorPostError: %v", err)
		}
	}()
}
