package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"

	"github.com/google/generative-ai-go/genai"
)

const structuredResultToolName = "submit_json_result"

type structuredResponseKey struct{}

type structuredResponseTransport struct{ http.RoundTripper }

func (t structuredResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.RoundTripper.RoundTrip(req)
	if body, ok := req.Context().Value(structuredResponseKey{}).(*bytes.Buffer); ok {
		body.Reset()
		if err == nil && response.StatusCode == http.StatusOK {
			response.Body = struct {
				io.Reader
				io.Closer
			}{io.TeeReader(response.Body, body), response.Body}
		}
	}
	return response, err
}

func (c *Client) SupportsStructuredOutput() bool { return true }

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

	var responseBody bytes.Buffer
	responseCtx := context.WithValue(ctx, structuredResponseKey{}, &responseBody)
	resp, err := requestModel.GenerateContent(responseCtx, parts...)
	var result string
	if err == nil {
		result, err = extractStructuredResult(resp)
	}
	if err != nil && responseBody.Len() > 0 {
		body := responseBody.Bytes()
		var compactJSON bytes.Buffer
		if json.Compact(&compactJSON, body) == nil {
			body = compactJSON.Bytes()
		}
		err = fmt.Errorf("%w\n応答JSON: %s", err, body)
	}
	return result, "", err
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
		return "", fmt.Errorf("%w: function call was not returned", provider.ErrInvalidStructuredOutput)
	}

	for _, part := range resp.Candidates[0].Content.Parts {
		call, ok := part.(genai.FunctionCall)
		if !ok || call.Name != structuredResultToolName {
			continue
		}
		result, ok := call.Args["result"]
		if !ok || result == nil {
			return "", fmt.Errorf("%w: function returned no result", provider.ErrInvalidStructuredOutput)
		}
		data, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("%w: encode structured function result: %v", provider.ErrInvalidStructuredOutput, err)
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return "", fmt.Errorf("%w: function returned no result", provider.ErrInvalidStructuredOutput)
		}
		return string(data), nil
	}
	return "", fmt.Errorf("%w: function call was not returned", provider.ErrInvalidStructuredOutput)
}
