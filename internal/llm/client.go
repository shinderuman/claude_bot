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
	"claude_bot/internal/llm/provider/openai"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
)

const (
	TemperatureSystem = 0.0
	ModelKeywordGemma = "gemma"
)

type Client struct {
	provider  provider.Provider
	config    *config.Config
	semaphore chan struct{}

	rateLimitMu                 sync.Mutex
	lastRateLimitNotif          time.Time
	lastTemporaryRateLimitNotif time.Time
}

func NewClient(cfg *config.Config) (*Client, error) {
	if cfg.LLMMaxConcurrency <= 0 || cfg.LLMMaxRetries < 0 {
		return nil, fmt.Errorf("LLM_MAX_CONCURRENCY は1以上、LLM_MAX_RETRIES は0以上を指定してください")
	}
	var p provider.Provider
	var err error

	switch cfg.LLMProvider {
	case config.LLMProviderClaude:
		p = anthropic.NewClient(cfg)
	case config.LLMProviderGemini:
		p, err = gemini.NewClient(cfg)
	case config.LLMProviderOpenAI:
		p, err = openai.NewClient(cfg)
	default:
		return nil, fmt.Errorf("エラー: 未知のプロバイダー '%s' が指定されました。'%s'、'%s' または '%s' を指定してください。",
			cfg.LLMProvider, config.LLMProviderClaude, config.LLMProviderGemini, config.LLMProviderOpenAI)
	}

	if err != nil {
		return nil, err
	}
	return &Client{
		provider:  p,
		config:    cfg,
		semaphore: make(chan struct{}, cfg.LLMMaxConcurrency),
	}, nil
}

func (c *Client) GenerateResponse(ctx context.Context, session *model.Session, conversation *model.Conversation, relevantFacts, botProfile string, currentImages []model.Image) string {
	var sessionSummary string
	if session != nil {
		sessionSummary = session.Summary
	}
	systemPrompt := prompts.BuildSystemPrompt(c.config, sessionSummary, relevantFacts, botProfile, true, c.config.CharacterPriority)

	return c.GenerateText(ctx, conversation.Messages, systemPrompt, c.config.MaxResponseTokens, currentImages, c.config.LLMTemperature, "返信生成")
}

func (c *Client) GenerateSummary(ctx context.Context, messages []model.Message, summary string) string {
	systemPrompt := prompts.BuildSystemPrompt(c.config, summary, "", "", false, 0.0)
	return c.GenerateText(ctx, messages, systemPrompt, c.config.MaxSummaryTokens, nil, TemperatureSystem, "会話要約生成")
}

func (c *Client) GenerateText(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64, operation string) string {
	content, err := c.generate(ctx, messages, systemPrompt, maxTokens, currentImages, temperature, operation, nil, nil)
	if err != nil {
		log.Printf("%s失敗: %v", operation, err)
	}
	return content
}

func (c *Client) generate(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64, operation string, schema *provider.StructuredSchema, result any) (string, error) {
	select {
	case c.semaphore <- struct{}{}:
		defer func() { <-c.semaphore }()
	case <-ctx.Done():
		return "", fmt.Errorf("待機中のLLMリクエスト取消: %w", ctx.Err())
	}

	msgs, sysPrompt := c.adjustForGemma(messages, systemPrompt)
	content, payload, err := c.executeWithRetry(ctx, operation, func() (string, string, error) {
		if schema != nil && c.provider.SupportsStructuredOutput() {
			return c.provider.GenerateStructuredContent(ctx, msgs, sysPrompt, maxTokens, currentImages, temperature, schema)
		}
		return c.provider.GenerateContent(ctx, msgs, sysPrompt, maxTokens, currentImages, temperature)
	})
	if err == nil && schema != nil {
		if err = schema.Decode(content, result); err != nil && content != "" {
			err = fmt.Errorf("%w\nJSON: %s", err, content)
		}
	}
	if err != nil {
		c.notifyGenerationError(operation, err, payload)
	}
	return content, err
}

func (c *Client) adjustForGemma(messages []model.Message, systemPrompt string) ([]model.Message, string) {
	if !c.shouldApplyGemmaWorkaround(messages, systemPrompt) {
		return messages, systemPrompt
	}

	newMessages := make([]model.Message, len(messages))
	copy(newMessages, messages)
	newMessages[0].Content = prompts.BuildGemmaWrapperPrompt(systemPrompt, messages[0].Content)
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
