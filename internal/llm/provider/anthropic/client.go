package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

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
		Timeout: 60 * time.Second,
	}

	opts := []option.RequestOption{
		option.WithHeader("Authorization", "Bearer "+cfg.AnthropicAuthToken),
		option.WithHTTPClient(httpClient),
		option.WithMaxRetries(0),
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

	pc := &provider.PayloadCapture{}
	ctx = context.WithValue(ctx, provider.CaptureKey, pc)

	msg, err := c.client.Messages.New(ctx, params)

	payload := string(pc.Body)

	if err != nil {
		return "", payload, c.classifyRateLimit(err)
	}

	return extractResponseText(msg), payload, nil
}

func (c *Client) IsRetryable(err error) bool {
	var aerr *anthropic.Error
	if errors.As(err, &aerr) {
		return aerr.StatusCode >= http.StatusInternalServerError
	}
	return false
}

func (c *Client) classifyRateLimit(err error) error {
	var apiErr *anthropic.Error
	if c.config.AnthropicBaseURL != zAIAnthropicBaseURL || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTooManyRequests {
		return err
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(apiErr.RawJSON()), &body) != nil {
		return err
	}
	switch body.Error.Code {
	case "1302", "1305":
		retryAfter := apiErr.Response.Header.Get("Retry-After")
		delay, parseErr := time.ParseDuration(retryAfter + "s")
		if parseErr != nil {
			if retryAt, dateErr := http.ParseTime(retryAfter); dateErr == nil {
				delay = time.Until(retryAt)
			}
		}
		return &provider.TemporaryRateLimitError{Err: err, RetryAfter: delay}
	default:
		return err
	}
}

func (c *Client) IsBadRequest(err error) bool {
	var aerr *anthropic.Error
	if errors.As(err, &aerr) {
		return aerr.StatusCode == http.StatusBadRequest
	}
	return false
}

func (c *Client) IsRateLimited(err error) bool {
	var aerr *anthropic.Error
	if errors.As(err, &aerr) {
		return aerr.StatusCode == http.StatusTooManyRequests
	}
	return false
}

func extractResponseText(msg *anthropic.Message) string {
	for _, content := range msg.Content {
		if content.Text != "" {
			return content.Text
		}
	}
	return ""
}

func convertMessages(messages []model.Message, currentImages []model.Image) []anthropic.MessageParam {
	result := make([]anthropic.MessageParam, len(messages))
	for i, msg := range messages {
		if msg.Role == model.RoleAssistant {
			result[i] = anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content))
		} else {
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
