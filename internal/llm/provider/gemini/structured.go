package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"

	"github.com/google/generative-ai-go/genai"
)

const structuredResultToolName = "submit_json_result"

func (c *Client) GenerateStructuredContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64, schema *provider.StructuredSchema) (string, string, error) {
	requestModel := c.requestModel(systemPrompt, maxTokens, temperature)
	requestModel.Tools = []*genai.Tool{
		{
			FunctionDeclarations: []*genai.FunctionDeclaration{
				{
					Name:        structuredResultToolName,
					Description: "Return the requested structured result in the result field.",
					Parameters: &genai.Schema{
						Type:       genai.TypeObject,
						Properties: map[string]*genai.Schema{"result": toGeminiSchema(schema)},
						Required:   []string{"result"},
					},
				},
			},
		},
	}
	requestModel.ToolConfig = &genai.ToolConfig{
		FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode:                 genai.FunctionCallingAny,
			AllowedFunctionNames: []string{structuredResultToolName},
		},
	}

	parts, err := c.buildRequestParts(messages, images)
	if err != nil {
		return "", "", err
	}

	resp, err := requestModel.GenerateContent(ctx, parts...)
	if err != nil {
		return "", "", err
	}

	result, err := extractStructuredResult(resp)
	if err != nil {
		return "", "", err
	}
	return result, "", nil
}

func toGeminiSchema(schema *provider.StructuredSchema) *genai.Schema {
	if schema == nil {
		return nil
	}

	result := &genai.Schema{
		Type:     geminiSchemaType(schema.Type),
		Required: schema.Required,
		Enum:     schema.Enum,
	}
	if schema.Items != nil {
		result.Items = toGeminiSchema(schema.Items)
	}
	if len(schema.Properties) > 0 {
		result.Properties = make(map[string]*genai.Schema, len(schema.Properties))
		for name, property := range schema.Properties {
			result.Properties[name] = toGeminiSchema(property)
		}
	}
	return result
}

func geminiSchemaType(schemaType string) genai.Type {
	switch schemaType {
	case "object":
		return genai.TypeObject
	case "array":
		return genai.TypeArray
	case "string":
		return genai.TypeString
	default:
		return genai.TypeUnspecified
	}
}

func extractStructuredResult(resp *genai.GenerateContentResponse) (string, error) {
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return "", fmt.Errorf("structured function call was not returned")
	}

	for _, part := range resp.Candidates[0].Content.Parts {
		call, ok := part.(genai.FunctionCall)
		if !ok || call.Name != structuredResultToolName {
			continue
		}
		result, ok := call.Args["result"]
		if !ok || result == nil {
			return "", fmt.Errorf("structured function returned no result")
		}
		data, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("encode structured function result: %w", err)
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return "", fmt.Errorf("structured function returned no result")
		}
		return string(data), nil
	}
	return "", fmt.Errorf("structured function call was not returned")
}
