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

const maxRetryDelay = time.Minute

var errorNotifier ErrorNotifierFunc

func SetErrorNotifier(notifier ErrorNotifierFunc) {
	errorNotifier = notifier
}

func (c *Client) notifyGenerationError(operation string, err error, payload string) {
	details := fmt.Sprintf("%s失敗: %v", operation, err)

	if payload != "" && c.provider.IsBadRequest(err) {
		log.Printf("LLMリクエスト: %s", payload)
		details = fmt.Sprintf("%s\n\n[Request Payload]\n%s", details, payload)
	}

	if errorNotifier != nil {
		if c.shouldNotifyRateLimit(err) {
			go errorNotifier("LLM生成エラー", details)
		} else {
			log.Printf("LLM 429通知省略: 通知間隔内")
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

	last := &c.lastRateLimitNotif
	var limited *provider.TemporaryRateLimitError
	if errors.As(err, &limited) {
		last = &c.lastTemporaryRateLimitNotif
	}
	if time.Since(*last) < interval {
		return false
	}

	*last = time.Now()
	return true
}

func (c *Client) executeWithRetry(ctx context.Context, operation string, request func() (string, string, error)) (string, string, error) {
	maxRetries := c.config.LLMMaxRetries
	backoff := time.Second

	for i := 0; ; i++ {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		content, payload, err := request()
		if err == nil {
			return content, payload, nil
		}

		if i >= maxRetries {
			return "", payload, err
		}

		delay := backoff
		var limited *provider.TemporaryRateLimitError
		switch {
		case errors.As(err, &limited):
			delay = max(delay, limited.RetryAfter)
			deadline, hasDeadline := ctx.Deadline()
			if delay > maxRetryDelay || (hasDeadline && delay >= time.Until(deadline)) {
				return "", payload, err
			}
		case errors.Is(err, provider.ErrResponseTooShort):
			delay = min(10*backoff, maxRetryDelay)
		case !c.provider.IsRetryable(err):
			return "", payload, err
		}
		backoff = min(2*backoff, maxRetryDelay)
		log.Printf("%s再試行: %d/%d回目、%v後、%v", operation, i+1, maxRetries, delay, err)

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", payload, ctx.Err()
		}
	}
}
