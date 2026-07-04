package llm

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/llm/provider"
	"claude_bot/internal/llm/provider/anthropic"
	"claude_bot/internal/llm/provider/gemini"
	"claude_bot/internal/model"
)

const (
	// TemperatureSystem is the temperature used for system tasks requiring accuracy
	TemperatureSystem = 0.0
	// ModelKeywordGemma identifies Gemma models
	ModelKeywordGemma = "gemma"
)

type Client struct {
	provider  provider.Provider
	config    *config.Config
	semaphore chan struct{}

	// 429通知の間引き状態（最終通知時刻）
	rateLimitMu        sync.Mutex
	lastRateLimitNotif time.Time
}

func NewClient(cfg *config.Config) *Client {
	var p provider.Provider

	switch cfg.LLMProvider {
	case config.LLMProviderClaude:
		p = anthropic.NewClient(cfg)
	case config.LLMProviderGemini:
		p = gemini.NewClient(cfg)
	default:
		log.Fatalf("エラー: 未知のプロバイダー '%s' が指定されました。'%s' または '%s' を指定してください。",
			cfg.LLMProvider, config.LLMProviderClaude, config.LLMProviderGemini)
	}

	return &Client{
		provider:  p,
		config:    cfg,
		semaphore: make(chan struct{}, cfg.LLMMaxConcurrency),
	}
}

func (c *Client) GenerateResponse(ctx context.Context, session *model.Session, conversation *model.Conversation, relevantFacts, botProfile string, currentImages []model.Image) string {
	var sessionSummary string
	if session != nil {
		sessionSummary = session.Summary
	}
	systemPrompt := BuildSystemPrompt(c.config, sessionSummary, relevantFacts, botProfile, true, c.config.CharacterPriority)

	return c.GenerateText(ctx, conversation.Messages, systemPrompt, c.config.MaxResponseTokens, currentImages, c.config.LLMTemperature)
}

func (c *Client) GenerateSummary(ctx context.Context, messages []model.Message, summary string) string {
	systemPrompt := BuildSystemPrompt(c.config, summary, "", "", false, 0.0)
	return c.GenerateText(ctx, messages, systemPrompt, c.config.MaxSummaryTokens, nil, TemperatureSystem)
}

// GenerateText calls the configured LLM provider to generate text content
func (c *Client) GenerateText(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
	// Semaphore acquisition to limit concurrency
	select {
	case c.semaphore <- struct{}{}:
		defer func() { <-c.semaphore }()
	case <-ctx.Done():
		log.Printf("LLM生成キャンセル (待機中): %v", ctx.Err())
		return ""
	}

	content, payload, err := c.executeWithRetry(ctx, func() (string, string, error) {
		msgs, sysPrompt := c.adjustForGemma(messages, systemPrompt)
		return c.provider.GenerateContent(ctx, msgs, sysPrompt, maxTokens, currentImages, temperature)
	})

	if err != nil {
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
		return ""
	}
	return content
}

// shouldNotifyRateLimit は429エラー時のSlack通知を間引くかを判定する。
// 429以外は常に通知。429の場合は設定間隔（RateLimitNotifyIntervalMinutes）を
// 経過している場合のみ通知し、lastRateLimitNotif を更新する。
// errorNotifier が非同期 goroutine で呼ばれるため、競合回避でロックする。
func (c *Client) shouldNotifyRateLimit(err error) bool {
	if !c.provider.IsRateLimited(err) {
		return true
	}

	interval := time.Duration(c.config.RateLimitNotifyIntervalMinutes) * time.Minute

	c.rateLimitMu.Lock()
	defer c.rateLimitMu.Unlock()

	// 間隔が0以下の場合は毎回通知
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


// executeWithRetry executes the given operation with exponential backoff retry logic
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

		// Check if error is retryable
		isRetryable := c.provider.IsRetryable(err)

		if !isRetryable {
			return "", payload, err
		}

		if i < maxRetries {
			delay := baseDelay * (1 << i)
			log.Printf("LLM生成エラー (429/5xx) - リトライ %d/%d 待機: %v. エラー: %v", i+1, maxRetries, delay, err)

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

func (c *Client) adjustForGemma(messages []model.Message, systemPrompt string) ([]model.Message, string) {
	if !c.shouldApplyGemmaWorkaround(messages, systemPrompt) {
		return messages, systemPrompt
	}

	newMessages := make([]model.Message, len(messages))
	copy(newMessages, messages)
	newMessages[0].Content = BuildGemmaWrapperPrompt(systemPrompt, messages[0].Content)
	return newMessages, ""
}

func (c *Client) shouldApplyGemmaWorkaround(messages []model.Message, systemPrompt string) bool {
	if c.config.LLMProvider != config.LLMProviderClaude {
		return false
	}
	if !strings.Contains(strings.ToLower(c.config.AnthropicModel), ModelKeywordGemma) {
		return false
	}
	if systemPrompt == "" {
		return false
	}
	if len(messages) == 0 {
		return false
	}
	if messages[0].Role != model.RoleUser && messages[0].Role != "" {
		return false
	}
	return true
}

func ExtractJSON(s string) string {
	// コードブロックの削除
	s = strings.ReplaceAll(s, "```json", "")
	s = strings.ReplaceAll(s, "```", "")

	// 最初に見つかった { または [ から、最後に見つかった } または ] までを抽出
	startObj := strings.Index(s, "{")
	startArr := strings.Index(s, "[")

	start := -1
	if startObj != -1 && startArr != -1 {
		start = min(startObj, startArr)
	} else if startObj != -1 {
		start = startObj
	} else if startArr != -1 {
		start = startArr
	}

	if start == -1 {
		return "{}" // デフォルトは空オブジェクト
	}

	endObj := strings.LastIndex(s, "}")
	endArr := strings.LastIndex(s, "]")

	end := -1
	if endObj != -1 && endArr != -1 {
		end = max(endObj, endArr)
	} else if endObj != -1 {
		end = endObj
	} else if endArr != -1 {
		end = endArr
	}

	if end == -1 || start > end {
		return "{}"
	}

	return s[start : end+1]
}
