package llm

import (
	"context"
	"fmt"
	"log"
	"time"
)

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
	baseDelay := 1 * time.Second

	for i := 0; i <= maxRetries; i++ {
		content, payload, err = operation()
		if err == nil {
			return content, payload, nil
		}

		isRetryable := c.provider.IsRetryable(err)

		if !isRetryable {
			return "", payload, err
		}

		if i < maxRetries {
			delay := baseDelay * (1 << i)
			log.Printf("LLM生成エラー (5xx) - リトライ %d/%d 待機: %v. エラー: %v", i+1, maxRetries, delay, err)

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
