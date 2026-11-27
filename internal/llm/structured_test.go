package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"claude_bot/internal/config"
	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
)

func (m *MockProvider) SupportsStructuredOutput() bool { return true }

func (m *MockProvider) GenerateStructuredContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64, schema *provider.StructuredSchema) (string, string, error) {
	return m.GenerateContent(ctx, messages, systemPrompt, maxTokens, images, temperature)
}

type structuredProviderMock struct {
	textOnly        bool
	mu              sync.Mutex
	structuredCalls int
	contentCalls    int
	structured      string
	structuredErr   error
	content         string
}

func (m *structuredProviderMock) SupportsStructuredOutput() bool { return !m.textOnly }

func (m *structuredProviderMock) GenerateContent(context.Context, []model.Message, string, int64, []model.Image, float64) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contentCalls++
	return m.content, "{}", nil
}

func (m *structuredProviderMock) GenerateStructuredContent(context.Context, []model.Message, string, int64, []model.Image, float64, *provider.StructuredSchema) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.structuredCalls++
	return m.structured, "{}", m.structuredErr
}

func (m *structuredProviderMock) IsRetryable(error) bool   { return false }
func (m *structuredProviderMock) IsBadRequest(error) bool  { return false }
func (m *structuredProviderMock) IsRateLimited(error) bool { return false }

func structuredTestClient(p provider.Provider) *Client {
	return &Client{
		provider: p,
		config: &config.Config{
			LLMMaxConcurrency: 1,
			LLMMaxRetries:     0,
		},
		semaphore: make(chan struct{}, 1),
	}
}

func generateJSONForTest(ctx context.Context, client *Client, prompt string) string {
	var result json.RawMessage
	if err := client.GenerateJSON(ctx, []model.Message{{Role: model.RoleUser, Content: "query"}}, prompt, 100, "テスト生成", &result); err != nil {
		return ""
	}
	return string(result)
}

func TestGenerateJSONUsesStructuredOutputWithoutFallback(t *testing.T) {
	p := &structuredProviderMock{
		structured: `{"target_candidates":[],"keys":[]}`,
		content:    "text result",
	}
	client := structuredTestClient(p)

	got := generateJSONForTest(context.Background(), client, prompts.Messages.System.FactQuery)
	if got != p.structured {
		t.Fatalf("GenerateJSON() = %q, want %q", got, p.structured)
	}
	if p.structuredCalls != 1 || p.contentCalls != 0 {
		t.Fatalf("calls = structured:%d content:%d, want 1/0", p.structuredCalls, p.contentCalls)
	}
}

func TestGenerateJSONDoesNotResendInvalidStructuredResult(t *testing.T) {
	for _, test := range []struct {
		name   string
		result string
		err    error
	}{
		{name: "missing result", err: provider.ErrInvalidStructuredOutput},
		{name: "schema mismatch", result: `{"keys":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &structuredProviderMock{
				structured: test.result, structuredErr: test.err,
				content: `{"target_candidates":[],"keys":[]}`,
			}
			client := structuredTestClient(p)
			got := generateJSONForTest(t.Context(), client, prompts.Messages.System.FactQuery)
			if got != "" || p.structuredCalls != 1 || p.contentCalls != 0 {
				t.Fatalf("result = %q, requests = %d structured and %d text", got, p.structuredCalls, p.contentCalls)
			}
		})
	}
}

func TestTextOnlyProviderSendsOneRequestAndValidatesJSON(t *testing.T) {
	for _, test := range []struct {
		result  string
		wantErr string
	}{
		{result: `{"target_candidates":[],"keys":[]}`},
		{result: `{"target_candidates":[],"keys":"user"}`, wantErr: "keys: 配列が必要"},
		{result: `{"target_candidates":[],"keys":[1]}`, wantErr: "keys: [0]: 文字列が必要"},
		{result: `{"keys":[]}`, wantErr: "必須項目 target_candidates がない"},
		{result: `{"keys":[`, wantErr: "9バイト目: unexpected end"},
		{result: "```json\n" + `{"target_candidates":[],"keys":[]}` + "\n```", wantErr: "1バイト目"},
	} {
		p := &structuredProviderMock{textOnly: true, content: test.result}
		client := structuredTestClient(p)
		var result json.RawMessage
		err := client.GenerateJSON(t.Context(), nil, prompts.Messages.System.FactQuery, 100, "検索クエリ生成", &result)
		if test.wantErr == "" {
			if err != nil || string(result) != test.result {
				t.Errorf("result = %q, %v, want %q", result, err, test.result)
			}
		} else if !errors.Is(err, provider.ErrInvalidStructuredOutput) || !strings.Contains(err.Error(), test.wantErr) || len(result) != 0 {
			t.Errorf("result = %q, %v, want %q", result, err, test.wantErr)
		} else if !strings.HasSuffix(err.Error(), "\nJSON: "+test.result) {
			t.Errorf("error = %q, want the whole response %q", err, test.result)
		}
		if p.structuredCalls != 0 || p.contentCalls != 1 {
			t.Errorf("calls = %d structured and %d text, want 0 and 1", p.structuredCalls, p.contentCalls)
		}
	}
}

func TestGenerateJSONKeepsUsingSupportedTools(t *testing.T) {
	p := &structuredProviderMock{structured: `{"target_candidates":[],"keys":[]}`}
	client := structuredTestClient(p)

	for i := 0; i < 3; i++ {
		got := generateJSONForTest(context.Background(), client, prompts.Messages.System.FactQuery)
		if got != p.structured {
			t.Fatalf("GenerateJSON() = %q, want %q", got, p.structured)
		}
	}
	if p.structuredCalls != 3 || p.contentCalls != 0 {
		t.Fatalf("calls = structured:%d content:%d, want 3/0", p.structuredCalls, p.contentCalls)
	}
}

func TestGenerateJSONKeepsUsingToolsAfterInvalidResult(t *testing.T) {
	p := &structuredProviderMock{
		structured: `{"target_candidates":[],"keys":[]}`,
		content:    "text result",
	}
	client := structuredTestClient(p)
	got := generateJSONForTest(context.Background(), client, prompts.Messages.System.FactQuery)
	if got != p.structured {
		t.Fatalf("first GenerateJSON() = %q, want %q", got, p.structured)
	}

	p.structuredErr = provider.ErrInvalidStructuredOutput
	for _, prompt := range []string{prompts.Messages.System.IntentClassification, prompts.Messages.System.FactQuery, prompts.Messages.System.FactExtraction} {
		got := generateJSONForTest(context.Background(), client, prompt)
		if got != "" {
			t.Fatalf("GenerateJSON() = %q after an invalid result", got)
		}
	}
	if p.structuredCalls != 4 || p.contentCalls != 0 {
		t.Fatalf("calls = structured:%d content:%d, want 4/0", p.structuredCalls, p.contentCalls)
	}
}

func TestGenerateJSONRejectsTextInToolResponse(t *testing.T) {
	var toolRequests, textRequests atomic.Int32
	result := `{"target_candidates":[],"keys":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		content := map[string]any{"type": "text", "text": result}
		if len(request.Tools) > 0 {
			if toolRequests.Add(1) == 1 {
				content = map[string]any{
					"type": "tool_use", "id": "tool_test", "name": "submit_json_result",
					"input": map[string]any{"result": json.RawMessage(result)},
				}
			}
		} else {
			textRequests.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_test", "type": "message", "role": "assistant", "model": "test-model",
			"content": []any{content}, "stop_reason": "end_turn",
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client, err := NewClient(&config.Config{
		LLMProvider: config.LLMProviderClaude, LLMMaxConcurrency: 1,
		AnthropicBaseURL: server.URL, AnthropicAuthToken: "test-token", AnthropicModel: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		var got json.RawMessage
		err := client.GenerateJSON(t.Context(), []model.Message{{Role: model.RoleUser, Content: "query"}},
			prompts.Messages.System.FactQuery, 100, "検索クエリ生成", &got)
		if i == 0 {
			if err != nil || string(got) != result {
				t.Fatalf("GenerateJSON() = %q, %v, want %q", got, err, result)
			}
		} else if !errors.Is(err, provider.ErrInvalidStructuredOutput) || !strings.Contains(err.Error(), "\n応答JSON: ") || !strings.Contains(err.Error(), `"type":"text"`) {
			t.Fatalf("GenerateJSON() error = %v, want rejected text response in the error", err)
		}
	}
	if tools, texts := toolRequests.Load(), textRequests.Load(); tools != 4 || texts != 0 {
		t.Fatalf("API requests = tools:%d text:%d, want 4/0", tools, texts)
	}
}

func TestGenerateTextUsesNormalPathForChat(t *testing.T) {
	p := &structuredProviderMock{content: "chat"}
	client := structuredTestClient(p)

	got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "hello"}}, prompts.Messages.System.Base, 100, nil, 0.5, "返信生成")
	if got != "chat" {
		t.Fatalf("GenerateText() = %q, want chat", got)
	}
	if p.structuredCalls != 0 || p.contentCalls != 1 {
		t.Fatalf("calls = structured:%d content:%d, want 0/1", p.structuredCalls, p.contentCalls)
	}
}

func TestStructuredOutputSchemas(t *testing.T) {
	for _, prompt := range []string{
		prompts.Messages.System.FactExtraction,
		prompts.Messages.System.FactQuery,
		prompts.Messages.System.IntentClassification,
		prompts.Messages.System.ImageGeneration,
	} {
		if schema := structuredOutputSchema(prompt); schema == nil {
			t.Fatalf("structuredOutputSchema(%q) = nil", prompt)
		}
	}
	if schema := structuredOutputSchema(prompts.Messages.System.Base); schema != nil {
		t.Fatalf("structuredOutputSchema(normal chat) = %#v, want nil", schema)
	}
}

func TestStructuredRequestRetriesTransientFailureWithoutTextFallback(t *testing.T) {
	const result = `{"target_candidates":[],"keys":[]}`
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Tools) == 0 {
			t.Error("temporary API failure caused a text request")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error","message":"retry"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"msg_test","type":"message","role":"assistant","model":"test-model","content":[
			{"type":"tool_use","id":"tool_test","name":"submit_json_result","input":{"result":`+result+`}}
		],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()

	client, err := NewClient(&config.Config{
		LLMProvider: config.LLMProviderClaude, LLMMaxConcurrency: 1, LLMMaxRetries: 1,
		AnthropicBaseURL: server.URL, AnthropicAuthToken: "test-token", AnthropicModel: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}

	got := generateJSONForTest(t.Context(), client, prompts.Messages.System.FactQuery)
	if got != result || calls.Load() != 2 {
		t.Fatalf("GenerateJSON = %q, API requests = %d, want %q and 2 requests", got, calls.Load(), result)
	}
}
