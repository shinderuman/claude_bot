package gemini

import (
	"testing"

	"claude_bot/internal/llm/provider"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/googleapi"
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

func TestStructuredOutputUnsupported(t *testing.T) {
	client := &Client{}
	if !client.isStructuredOutputUnsupported(&googleapi.Error{Code: 400, Message: "function calling unsupported"}) {
		t.Fatal("function calling rejection was not detected")
	}
	if client.isStructuredOutputUnsupported(&googleapi.Error{Code: 400, Message: "sensitive request"}) {
		t.Fatal("unrelated bad request was treated as unsupported")
	}
	if client.isStructuredOutputUnsupported(&googleapi.Error{Code: 500, Message: "function calling failed"}) {
		t.Fatal("server error was treated as unsupported")
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
