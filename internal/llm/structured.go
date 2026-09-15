package llm

import "claude_bot/internal/llm/provider"

func structuredOutputSchema(systemPrompt string) *provider.StructuredSchema {
	switch systemPrompt {
	case Messages.System.FactExtraction:
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
	case Messages.System.FactQuery:
		return objectSchema(
			map[string]*provider.StructuredSchema{
				"target_candidates": arraySchema(stringSchema()),
				"keys":              arraySchema(stringSchema()),
			},
			"target_candidates", "keys",
		)
	case Messages.System.IntentClassification:
		intent := stringSchema()
		intent.Enum = []string{"chat", "image_generation", "analysis", "daily_summary", "follow_request"}
		return objectSchema(
			map[string]*provider.StructuredSchema{
				"intent":        intent,
				"image_prompt":  stringSchema(),
				"analysis_urls": arraySchema(stringSchema()),
				"target_date":   stringSchema(),
			},
			"intent", "image_prompt", "analysis_urls", "target_date",
		)
	case Messages.System.ImageGeneration:
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
