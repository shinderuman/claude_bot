package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

const structuredResultToolName = "submit_json_result"

func (c *Client) GenerateStructuredContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64, schema *provider.StructuredSchema) (string, string, error) {
	params := anthropic.MessageNewParams{
		Model:       anthropic.Model(c.config.AnthropicModel),
		MaxTokens:   maxTokens,
		Messages:    convertMessages(messages, images),
		Temperature: anthropic.Float(temperature),
		Tools: []anthropic.ToolUnionParam{
			{
				OfTool: &anthropic.ToolParam{
					Name:        structuredResultToolName,
					Description: anthropic.String("Return the requested structured result in the result field."),
					InputSchema: anthropic.ToolInputSchemaParam{
						Properties: map[string]any{"result": schema},
						Required:   []string{"result"},
					},
				},
			},
		},
		ToolChoice: anthropic.ToolChoiceUnionParam{
			OfAuto: &anthropic.ToolChoiceAutoParam{},
		},
	}
	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{{Type: "text", Text: systemPrompt}}
	}

	pc := &provider.PayloadCapture{}
	ctx = context.WithValue(ctx, provider.CaptureKey, pc)
	msg, err := c.client.Messages.New(ctx, params)
	payload := string(pc.Body)
	if err != nil {
		if isStructuredOutputUnsupported(err) {
			return c.GenerateContent(ctx, messages, systemPrompt, maxTokens, images, temperature)
		}
		return "", payload, err
	}

	result, structuredErr := extractStructuredResult(msg)
	if structuredErr == nil {
		return result, payload, nil
	}
	if text := extractResponseText(msg); text != "" {
		return text, payload, nil
	}
	return "", payload, structuredErr
}

func extractStructuredResult(msg *anthropic.Message) (string, error) {
	for _, content := range msg.Content {
		if content.Name != structuredResultToolName || len(content.Input) == 0 {
			continue
		}

		var input struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(content.Input, &input); err != nil {
			return "", fmt.Errorf("decode structured tool input: %w", err)
		}
		if len(input.Result) == 0 || bytes.Equal(bytes.TrimSpace(input.Result), []byte("null")) {
			return "", fmt.Errorf("structured tool returned no result")
		}
		return string(input.Result), nil
	}
	return "", fmt.Errorf("structured tool call was not returned")
}

func isStructuredOutputUnsupported(err error) bool {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || (apiErr.StatusCode != http.StatusBadRequest && apiErr.StatusCode != http.StatusNotFound) {
		return false
	}
	message := strings.ToLower(apiErr.Error())
	return strings.Contains(message, "tool") || strings.Contains(message, "function")
}
