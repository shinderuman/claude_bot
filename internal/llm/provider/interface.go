package provider

import (
	"context"

	"claude_bot/internal/model"
)

type Provider interface {
	// GenerateContent はLLMにテキスト生成をリクエストします
	GenerateContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image) (string, error)
}
