package llm

import (
	"context"
	"fmt"

	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
)

func (c *Client) GenerateJSON(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, operation string, result any) error {
	schema := structuredOutputSchema(systemPrompt)
	if schema == nil {
		return fmt.Errorf("%sのJSONスキーマがありません", operation)
	}
	_, err := c.generate(ctx, messages, systemPrompt, maxTokens, nil, TemperatureSystem, operation, schema, result)
	return err
}

func structuredOutputSchema(systemPrompt string) *provider.StructuredSchema {
	switch systemPrompt {
	case prompts.Messages.System.FactExtraction:
		return arraySchema(objectSchema(
			map[string]*provider.StructuredSchema{
				"target":          stringSchema(),
				"target_username": stringSchema(),
				"author":          stringSchema(),
				"author_username": stringSchema(),
				"key":             stringSchema(),
				"value":           stringSchema(),
			},
			"target", "key", "value",
		))
	case prompts.Messages.System.FactQuery:
		return objectSchema(
			map[string]*provider.StructuredSchema{
				"target_candidates": arraySchema(stringSchema()),
				"keys":              arraySchema(stringSchema()),
			},
			"target_candidates", "keys",
		)
	case prompts.Messages.System.IntentClassification:
		intent := stringSchema()
		intent.Enum = []string{
			string(model.IntentChat),
			string(model.IntentImageGeneration),
			string(model.IntentAnalysis),
			string(model.IntentDailySummary),
			string(model.IntentFollowRequest),
		}
		return objectSchema(
			map[string]*provider.StructuredSchema{
				"intent":        intent,
				"image_prompt":  stringSchema(),
				"analysis_urls": arraySchema(stringSchema()),
				"target_date":   stringSchema(),
			},
			"intent", "image_prompt", "analysis_urls", "target_date",
		)
	case prompts.Messages.System.ImageGeneration:
		return objectSchema(
			map[string]*provider.StructuredSchema{"svg": stringSchema()},
			"svg",
		)
	default:
		return nil
	}
}

func stringSchema() *provider.StructuredSchema {
	return &provider.StructuredSchema{Type: "string"}
}

func arraySchema(items *provider.StructuredSchema) *provider.StructuredSchema {
	return &provider.StructuredSchema{Type: "array", Items: items}
}

func objectSchema(properties map[string]*provider.StructuredSchema, required ...string) *provider.StructuredSchema {
	return &provider.StructuredSchema{
		Type:       "object",
		Properties: properties,
		Required:   required,
	}
}
