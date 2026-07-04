package provider

import (
	"context"

	"claude_bot/internal/model"
)

type Provider interface {
	GenerateContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64) (string, string, error)

	IsRetryable(err error) bool
	IsBadRequest(err error) bool
	IsRateLimited(err error) bool
}
