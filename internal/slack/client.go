package slack

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

const (
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
		log.Printf("Slack通知なし: 未設定")
		return nil
	}

	if message == "" {
		return nil
	}

	if c.botUsername != "" {
		message = fmt.Sprintf("[%s] %s", c.botUsername, message)
	}

	err := c.postMessageWithRetry(ctx, channelID, message)
	if err != nil {
		log.Printf("Slack通知失敗: %v", err)
	}
	return err
}

func (c *Client) postMessageWithRetry(ctx context.Context, channelID, message string) error {
	ctx, cancel := context.WithTimeout(ctx, maxTotalTimeout)
	defer cancel()
	backoff := initialBackoff

	for {
		_, _, err := c.client.PostMessageContext(ctx, channelID, slack.MsgOptionText(message, false))
		if err == nil {
			return nil
		}

		if !isRateLimitedError(err) {
			return err
		}

		delay := backoff
		var limited *slack.RateLimitedError
		if errors.As(err, &limited) {
			delay = max(delay, limited.RetryAfter)
		}
		if deadline, ok := ctx.Deadline(); ok && delay >= time.Until(deadline) {
			return fmt.Errorf("Slack再試行の待機時間が期限を超えます: %w", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			backoff = min(backoff*2, maxBackoffDelay)
		}
	}
}

func isRateLimitedError(err error) bool {
	if err == nil {
		return false
	}
	var rateLimited *slack.RateLimitedError
	if errors.As(err, &rateLimited) {
		return true
	}
	var status slack.StatusCodeError
	if errors.As(err, &status) {
		return status.Code == http.StatusTooManyRequests
	}
	msg := strings.ToLower(err.Error())
	return msg == "ratelimited"
}

func (c *Client) PostMessageAsync(ctx context.Context, message string) {
	if !c.enabled {
		return
	}
	go func() {
		_ = c.PostMessage(ctx, message)
	}()
}

func (c *Client) PostErrorMessageAsync(ctx context.Context, message string) {
	if !c.enabled {
		return
	}
	go func() {
		_ = c.PostErrorMessage(ctx, message)
	}()
}
