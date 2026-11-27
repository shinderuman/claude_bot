package gemini

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
	httptransport "google.golang.org/api/transport/http"
)

func TestGenerateStructuredContentKeepsOriginalErrorResponse(t *testing.T) {
	for _, tt := range []struct{ name, body, wantBody, wantError string }{
		{
			name: "text without a function call",
			body: `{
  "candidates": [{
    "content": {"parts": [{"text": "先頭  \n"}, {"text": "末尾"}]},
    "finishReason": "MAX_TOKENS"
  }],
  "modelVersion": "test-version"
}` + "\n",
			wantBody:  `{"candidates":[{"content":{"parts":[{"text":"先頭  \n"},{"text":"末尾"}]},"finishReason":"MAX_TOKENS"}],"modelVersion":"test-version"}`,
			wantError: "function call was not returned",
		},
		{
			name:      "function call without result",
			body:      `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"submit_json_result","args":{"unexpected":"元の引数"}}}]}}]}`,
			wantError: "function returned no result",
		},
		{
			name:      "blocked before extraction",
			body:      `{"promptFeedback":{"blockReason":"SAFETY"},"responseId":"test-id"}`,
			wantError: "blocked: prompt:",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("key") != "test-key" {
					t.Error("Gemini API key was not sent")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			apiKey := option.WithAPIKey("test-key")
			httpClient, _, err := httptransport.NewClient(t.Context(), apiKey)
			if err != nil {
				t.Fatal(err)
			}
			httpClient.Transport = structuredResponseTransport{httpClient.Transport}
			sdkClient, err := genai.NewClient(t.Context(), apiKey, option.WithEndpoint(server.URL), option.WithHTTPClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}
			defer sdkClient.Close()
			client := &Client{model: sdkClient.GenerativeModel("gemini-test")}
			result, _, err := client.GenerateStructuredContent(t.Context(), []model.Message{{Role: model.RoleUser, Content: "test"}},
				"system", 100, nil, 0, &provider.StructuredSchema{Type: "object"})
			wantBody := tt.body
			if tt.wantBody != "" {
				wantBody = tt.wantBody
			}
			if result != "" || err == nil || !strings.Contains(err.Error(), tt.wantError) || !strings.HasSuffix(err.Error(), "\n応答JSON: "+wantBody) {
				t.Fatalf("got %q, %v, want %q with JSON %q", result, err, tt.wantError, wantBody)
			}
			if tt.name != "blocked before extraction" && !errors.Is(err, provider.ErrInvalidStructuredOutput) {
				t.Fatalf("error = %v, want invalid structured output", err)
			}
		})
	}
}

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

func TestExtractStructuredResultRejectsTextResponse(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []genai.Part{genai.Text(`{"intent":"chat"}`)}},
		}},
	}

	if _, err := extractStructuredResult(resp); err == nil {
		t.Fatal("text was accepted as a tool response")
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
