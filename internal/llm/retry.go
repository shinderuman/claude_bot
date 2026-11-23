package llm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"claude_bot/internal/llm/provider"
)

type ErrorNotifierFunc func(message, details string)

var errorNotifier ErrorNotifierFunc

func SetErrorNotifier(notifier ErrorNotifierFunc) {
	errorNotifier = notifier
}

func (c *Client) reportGenerationError(err error, payload string) {
	log.Printf("LLM生成エラー (最終): %v", err)
	details := err.Error()

	if c.provider.IsBadRequest(err) {
		log.Printf("リクエスト詳細 [Request Payload]:\n%s", payload)
		details = fmt.Sprintf("%s\n\n[Request Payload]\n%s", details, payload)
	}

	if errorNotifier != nil {
		if c.shouldNotifyRateLimit(err) {
			go errorNotifier("LLM生成エラー", details)
		} else {
			log.Printf("LLM生成エラー (429) - 通知間引済み（前回通知から間隔内）")
		}
	}
}

func (c *Client) shouldNotifyRateLimit(err error) bool {
	if !c.provider.IsRateLimited(err) {
		return true
	}

	interval := time.Duration(c.config.RateLimitNotifyIntervalMinutes) * time.Minute

	c.rateLimitMu.Lock()
	defer c.rateLimitMu.Unlock()

	if interval <= 0 {
		c.lastRateLimitNotif = time.Now()
		return true
	}

	if time.Since(c.lastRateLimitNotif) < interval {
		return false
	}

	c.lastRateLimitNotif = time.Now()
	return true
}

func (c *Client) executeWithRetry(ctx context.Context, operation func() (string, string, error)) (string, string, error) {
	var content string
	var payload string
	var err error
	maxRetries := c.config.LLMMaxRetries
	backoff := time.Second

	for i := 0; i <= maxRetries; i++ {
		if err := ctx.Err(); err != nil {
			return "", payload, err
		}
		content, payload, err = operation()
		if err == nil {
			return content, payload, nil
		}

		short := errors.Is(err, provider.ErrResponseTooShort)
		isRetryable := short || c.provider.IsRetryable(err)

		if !isRetryable {
			return "", payload, err
		}

		if i < maxRetries {
			delay := backoff
			if short {
				delay = min(10*backoff, time.Minute)
			}
			backoff = min(2*backoff, time.Minute)
			log.Printf("LLM生成エラー - リトライ %d/%d 待機: %v. エラー: %v", i+1, maxRetries, delay, err)

			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return "", payload, ctx.Err()
			}
		}
	}
	return "", payload, err
}
