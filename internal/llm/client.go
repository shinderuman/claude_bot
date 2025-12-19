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
		log.Fatalf("エラー: 未知のプロバイダー '%s' が指定されました。'claude' または 'gemini' を指定してください。", cfg.LLMProvider)
	}

	return &Client{
		provider: p,
		config:   cfg,
	}
}

func (c *Client) GenerateResponse(ctx context.Context, session *model.Session, conversation *model.Conversation, relevantFacts, botProfile string, currentImages []model.Image) string {
	var sessionSummary string
	if session != nil {
		sessionSummary = session.Summary
	}
	systemPrompt := BuildSystemPrompt(c.config, sessionSummary, relevantFacts, botProfile, true)

	return c.GenerateText(ctx, conversation.Messages, systemPrompt, c.config.MaxResponseTokens, currentImages)
}

func (c *Client) GenerateSummary(ctx context.Context, messages []model.Message, summary string) string {
	systemPrompt := BuildSystemPrompt(c.config, summary, "", "", false)
	return c.GenerateText(ctx, messages, systemPrompt, c.config.MaxSummaryTokens, nil)
}

// GenerateText calls the configured LLM provider to generate text content
func (c *Client) GenerateText(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image) string {
	content, err := c.provider.GenerateContent(ctx, messages, systemPrompt, maxTokens, currentImages)
	if err != nil {
		log.Printf("LLM生成エラー: %v", err)
		if errorNotifier != nil {
			go errorNotifier("LLM生成エラー", err.Error())
		}
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
		return "{}" // デフォルトは空オブジェクト
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
