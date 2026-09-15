package llm

import (
	"context"
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
		if schema := structuredOutputSchema(systemPrompt); schema != nil {
			structuredContent, structuredPayload, structuredErr := c.provider.GenerateStructuredContent(ctx, msgs, sysPrompt, maxTokens, currentImages, temperature, schema)
			if structuredErr == nil {
				return structuredContent, structuredPayload, nil
			}
			log.Printf("Function Call失敗、通常呼び出しにフォールバック: %v", structuredErr)
			return c.provider.GenerateContent(ctx, msgs, sysPrompt, maxTokens, currentImages, temperature)
		}
		return c.provider.GenerateContent(ctx, msgs, sysPrompt, maxTokens, currentImages, temperature)
	})

	if err != nil {
		c.reportGenerationError(err, payload)
		return ""
	}
	return content
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
