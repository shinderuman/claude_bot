package provider

import (
	"context"
	"time"

	"claude_bot/internal/model"
)

type TemporaryRateLimitError struct {
	Err        error
	RetryAfter time.Duration
}

func (e *TemporaryRateLimitError) Error() string { return e.Err.Error() }
func (e *TemporaryRateLimitError) Unwrap() error { return e.Err }

type Provider interface {
	SupportsStructuredOutput() bool
	GenerateContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64) (string, string, error)
	GenerateStructuredContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64, schema *StructuredSchema) (string, string, error)

	IsRetryable(err error) bool
	IsBadRequest(err error) bool
	IsRateLimited(err error) bool
}
