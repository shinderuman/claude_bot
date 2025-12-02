package llm

import (
	"context"
	"log"
	"strings"

	"claude_bot/internal/config"
	"claude_bot/internal/model"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	MaxResponseTokens = 1024 // 通常応答の最大トークン数
	MaxSummaryTokens  = 2048 // 要約生成の最大トークン数
)

type Client struct {
	client anthropic.Client
	config *config.Config
}

func NewClient(cfg *config.Config) *Client {
	opts := []option.RequestOption{option.WithAPIKey(cfg.AnthropicAuthToken)}
	if cfg.AnthropicBaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.AnthropicBaseURL))
	}
	return &Client{
		client: anthropic.NewClient(opts...),
		config: cfg,
	}
}

// BuildSystemPrompt is a function type for building system prompts
// This allows the bot package to inject its prompt builder
type SystemPromptBuilder func(characterPrompt, sessionSummary, relevantFacts string, includeCharacterPrompt bool) string

var systemPromptBuilder SystemPromptBuilder

// SetSystemPromptBuilder sets the system prompt builder function
func SetSystemPromptBuilder(builder SystemPromptBuilder) {
	systemPromptBuilder = builder
}

func (c *Client) GenerateResponse(ctx context.Context, session *model.Session, conversation *model.Conversation, relevantFacts string) string {
	var sessionSummary string
	if session != nil {
		sessionSummary = session.Summary
	}
	systemPrompt := systemPromptBuilder(c.config.CharacterPrompt, sessionSummary, relevantFacts, true)
	return c.CallClaudeAPI(ctx, conversation.Messages, systemPrompt, MaxResponseTokens)
}

func (c *Client) CallClaudeAPIForSummary(ctx context.Context, messages []model.Message, summary string) string {
	systemPrompt := systemPromptBuilder(c.config.CharacterPrompt, summary, "", false)
	return c.CallClaudeAPI(ctx, messages, systemPrompt, MaxSummaryTokens)
}

func (c *Client) CallClaudeAPI(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64) string {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.config.AnthropicModel),
		MaxTokens: maxTokens,
		Messages:  convertMessages(messages),
	}

	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Type: "text", Text: systemPrompt},
		}
	}

	msg, err := c.client.Messages.New(ctx, params)
	if err != nil {
		log.Printf("API呼び出しエラー: %v", err)
		return ""
	}

	return extractResponseText(msg)
}

func extractResponseText(msg *anthropic.Message) string {
	if len(msg.Content) > 0 {
		return msg.Content[0].Text
	}
	return ""
}

func convertMessages(messages []model.Message) []anthropic.MessageParam {
	result := make([]anthropic.MessageParam, len(messages))
	for i, msg := range messages {
		if msg.Role == "assistant" {
			result[i] = anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content))
		} else {
			result[i] = anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content))
		}
	}
	return result
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
		if startObj < startArr {
			start = startObj
		} else {
			start = startArr
		}
	} else if startObj != -1 {
		start = startObj
	} else if startArr != -1 {
		start = startArr
	}

	if start == -1 {
		return "{}" // デフォルトは空オブジェクト（文脈によるが安全策）
	}

	endObj := strings.LastIndex(s, "}")
	endArr := strings.LastIndex(s, "]")

	end := -1
	if endObj != -1 && endArr != -1 {
		if endObj > endArr {
			end = endObj
		} else {
			end = endArr
		}
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
