package gemini

import (
	"testing"

	"claude_bot/internal/llm/provider"

	"github.com/google/generative-ai-go/genai"
)

func TestExtractStructuredResult(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []genai.Part{
				genai.FunctionCall{
					Name: structuredResultToolName,
					Args: map[string]any{"result": map[string]any{"intent": "chat"}},
				},
			}},
		}},
	}

	got, err := extractStructuredResult(resp)
	if err != nil {
		t.Fatalf("extractStructuredResult() error = %v", err)
	}
	if got != `{"intent":"chat"}` {
		t.Fatalf("extractStructuredResult() = %s", got)
	}
}

func TestExtractStructuredResultRejectsMissingResult(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []genai.Part{
				genai.FunctionCall{Name: structuredResultToolName, Args: map[string]any{"result": nil}},
			}},
		}},
	}

	if _, err := extractStructuredResult(resp); err == nil {
		t.Fatal("extractStructuredResult() accepted nil result")
	}
}

func TestToGeminiSchema(t *testing.T) {
	schema := &provider.StructuredSchema{
		Type: "object",
		Properties: map[string]*provider.StructuredSchema{
			"values": {
				Type:  "array",
				Items: &provider.StructuredSchema{Type: "string"},
			},
		},
		Required: []string{"values"},
	}

	got := toGeminiSchema(schema)
	if got.Type != genai.TypeObject || got.Properties["values"].Type != genai.TypeArray || got.Properties["values"].Items.Type != genai.TypeString {
		t.Fatalf("toGeminiSchema() = %#v", got)
	}
}
