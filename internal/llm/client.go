package llm

import (
	"context"
	"log"
	"strings"
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
)

type Client struct {
	provider  provider.Provider
	config    *config.Config
	semaphore chan struct{}
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
	systemPrompt := BuildSystemPrompt(c.config, sessionSummary, relevantFacts, botProfile, true)

	return c.GenerateText(ctx, conversation.Messages, systemPrompt, c.config.MaxResponseTokens, currentImages, c.config.LLMTemperature)
}

func (c *Client) GenerateSummary(ctx context.Context, messages []model.Message, summary string) string {
	systemPrompt := BuildSystemPrompt(c.config, summary, "", "", false)
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

	content, err := c.executeWithRetry(ctx, func() (string, error) {
		return c.provider.GenerateContent(ctx, messages, systemPrompt, maxTokens, currentImages, temperature)
	})

	if err != nil {
		log.Printf("LLM生成エラー (最終): %v", err)
		if errorNotifier != nil {
			go errorNotifier("LLM生成エラー", err.Error())
		}
		return ""
	}
	return content
}

// executeWithRetry executes the given operation with exponential backoff retry logic
func (c *Client) executeWithRetry(ctx context.Context, operation func() (string, error)) (string, error) {
	var content string
	var err error
	maxRetries := c.config.LLMMaxRetries
	baseDelay := 1 * time.Second

	for i := 0; i <= maxRetries; i++ {
		content, err = operation()
		if err == nil {
			return content, nil
		}

		// Check if error is retryable
		isRetryable := c.provider.IsRetryable(err)

		if !isRetryable {
			return "", err
		}

		if i < maxRetries {
			delay := baseDelay * (1 << i)
			log.Printf("LLM生成エラー (429/5xx) - リトライ %d/%d 待機: %v. エラー: %v", i+1, maxRetries, delay, err)

			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	return "", err
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
