package llm

import (
	"context"
	"log"
	"strings"

	"claude_bot/internal/config"
	"claude_bot/internal/llm/provider"
	"claude_bot/internal/llm/provider/anthropic"
	"claude_bot/internal/llm/provider/gemini"
	"claude_bot/internal/model"
)

type Client struct {
	provider provider.Provider
	config   *config.Config
}

func NewClient(cfg *config.Config) *Client {
	var p provider.Provider

	switch cfg.LLMProvider {
	case "claude":
		p = anthropic.NewClient(cfg)
	case "gemini":
		p = gemini.NewClient(cfg)
	default:

		// デフォルトはClaude（後方互換性のため、または明示的なエラーにしても良い）
		log.Printf("警告: 未知のプロバイダー '%s' が指定されました。Claudeを使用します。", cfg.LLMProvider)
		p = anthropic.NewClient(cfg)
	}

	return &Client{
		provider: p,
		config:   cfg,
	}
}

func (c *Client) GenerateResponse(ctx context.Context, session *model.Session, conversation *model.Conversation, relevantFacts string, currentImages []model.Image) string {
	var sessionSummary string
	if session != nil {
		sessionSummary = session.Summary
	}
	systemPrompt := BuildSystemPrompt(c.config.CharacterPrompt, sessionSummary, relevantFacts, true)
	return c.CallClaudeAPI(ctx, conversation.Messages, systemPrompt, c.config.MaxResponseTokens, currentImages)
}

func (c *Client) CallClaudeAPIForSummary(ctx context.Context, messages []model.Message, summary string) string {
	systemPrompt := BuildSystemPrompt(c.config.CharacterPrompt, summary, "", false)
	return c.CallClaudeAPI(ctx, messages, systemPrompt, c.config.MaxSummaryTokens, nil)
}

// CallClaudeAPI は互換性のために名前を残しているが、実際にはProviderを呼び出す
func (c *Client) CallClaudeAPI(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image) string {
	content, err := c.provider.GenerateContent(ctx, messages, systemPrompt, maxTokens, currentImages)
	if err != nil {
		log.Printf("LLM生成エラー: %v", err)
		return ""
	}
	return content
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
