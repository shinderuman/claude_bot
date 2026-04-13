package anthropic

import (
	"context"
	"errors"
	"log"
	"net/http"

	"claude_bot/internal/config"
	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type Client struct {
	client anthropic.Client
	config *config.Config
}

func NewClient(cfg *config.Config) provider.Provider {
	httpClient := &http.Client{
		Transport: &provider.PayloadCaptureTransport{
			Base: http.DefaultTransport,
		},
	}

	opts := []option.RequestOption{
		option.WithHeader("Authorization", "Bearer "+cfg.AnthropicAuthToken),
		option.WithHTTPClient(httpClient),
	}
	if cfg.AnthropicBaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.AnthropicBaseURL))
	}
	return &Client{
		client: anthropic.NewClient(opts...),
		config: cfg,
	}
}

func (c *Client) GenerateContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64) (string, string, error) {
	params := anthropic.MessageNewParams{
		Model:       anthropic.Model(c.config.AnthropicModel),
		MaxTokens:   maxTokens,
		Messages:    convertMessages(messages, images),
		Temperature: anthropic.Float(temperature),
	}

	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Type: "text", Text: systemPrompt},
		}
	}

	// Payloadキャプチャの準備
	pc := &provider.PayloadCapture{}
	ctx = context.WithValue(ctx, provider.CaptureKey, pc)

	msg, err := c.client.Messages.New(ctx, params)

	// キャプチャされたPayloadを取得
	payload := string(pc.Body)

	if err != nil {
		log.Printf("Anthropic API呼び出しエラー: %v", err)
		return "", payload, err
	}

	return extractResponseText(msg), payload, nil
}

func (c *Client) IsRetryable(err error) bool {
	var aerr *anthropic.Error
	if errors.As(err, &aerr) {
		return aerr.StatusCode == http.StatusTooManyRequests || aerr.StatusCode >= http.StatusInternalServerError
	}
	return false
}

func (c *Client) IsBadRequest(err error) bool {
	var aerr *anthropic.Error
	if errors.As(err, &aerr) {
		return aerr.StatusCode == http.StatusBadRequest
	}
	return false
}

func extractResponseText(msg *anthropic.Message) string {
	if len(msg.Content) > 0 {
		return msg.Content[0].Text
	}
	return ""
}

func convertMessages(messages []model.Message, currentImages []model.Image) []anthropic.MessageParam {
	result := make([]anthropic.MessageParam, len(messages))
	for i, msg := range messages {
		if msg.Role == model.RoleAssistant {
			result[i] = anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content))
		} else {
			// 最後のユーザーメッセージに画像を添付
			if i == len(messages)-1 && len(currentImages) > 0 {
				content := []anthropic.ContentBlockParamUnion{
					anthropic.NewTextBlock(msg.Content),
				}
				for _, img := range currentImages {
					content = append(content, anthropic.NewImageBlockBase64(img.MediaType, img.Data))
				}
				result[i] = anthropic.NewUserMessage(content...)
			} else {
				result[i] = anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content))
			}
		}
	}
	return result
}
