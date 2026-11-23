package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestGenerateTextUsesStructuredOutputWithoutFallback(t *testing.T) {
	p := &structuredProviderMock{
		structured: `{"target_candidates":[],"keys":[]}`,
		content:    "text result",
	}
	client := structuredTestClient(p)

	got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, prompts.Messages.System.FactQuery, 100, nil, TemperatureSystem)
	if got != p.structured {
		t.Fatalf("GenerateText() = %q, want %q", got, p.structured)
	}
	if p.structuredCalls != 1 || p.contentCalls != 0 {
		t.Fatalf("calls = structured:%d content:%d, want 1/0", p.structuredCalls, p.contentCalls)
	}
}

func TestGenerateTextDoesNotResendInvalidStructuredResult(t *testing.T) {
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
			got := client.GenerateText(t.Context(), []model.Message{{Role: model.RoleUser, Content: "query"}},
				prompts.Messages.System.FactQuery, 100, nil, TemperatureSystem)
			if got != "" || p.structuredCalls != 1 || p.contentCalls != 0 {
				t.Fatalf("result = %q, requests = %d structured and %d text", got, p.structuredCalls, p.contentCalls)
			}
		})
	}
}

func TestTextOnlyProviderSendsOneRequestAndValidatesJSON(t *testing.T) {
	for _, test := range []struct {
		result string
		want   string
	}{
		{result: `{"target_candidates":[],"keys":[]}`, want: `{"target_candidates":[],"keys":[]}`},
		{result: `{"target_candidates":"user","keys":[]}`},
		{result: "```json\n" + `{"target_candidates":[],"keys":[]}` + "\n```"},
	} {
		p := &structuredProviderMock{textOnly: true, content: test.result}
		client := structuredTestClient(p)
		got := client.GenerateText(t.Context(), []model.Message{{Role: model.RoleUser, Content: "query"}},
			prompts.Messages.System.FactQuery, 100, nil, TemperatureSystem)
		if got != test.want || p.structuredCalls != 0 || p.contentCalls != 1 {
			t.Fatalf("result = %q, want %q, calls = %d structured and %d text", got, test.want, p.structuredCalls, p.contentCalls)
		}
	}
}

func TestGenerateTextKeepsUsingSupportedTools(t *testing.T) {
	p := &structuredProviderMock{structured: `{"target_candidates":[],"keys":[]}`}
	client := structuredTestClient(p)

	for i := 0; i < 3; i++ {
		got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, prompts.Messages.System.FactQuery, 100, nil, TemperatureSystem)
		if got != p.structured {
			t.Fatalf("GenerateText() = %q, want %q", got, p.structured)
		}
	}
	if p.structuredCalls != 3 || p.contentCalls != 0 {
		t.Fatalf("calls = structured:%d content:%d, want 3/0", p.structuredCalls, p.contentCalls)
	}
}

func TestGenerateTextKeepsUsingToolsAfterInvalidResult(t *testing.T) {
	p := &structuredProviderMock{
		structured: `{"target_candidates":[],"keys":[]}`,
		content:    "text result",
	}
	client := structuredTestClient(p)
	got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, prompts.Messages.System.FactQuery, 100, nil, TemperatureSystem)
	if got != p.structured {
		t.Fatalf("first GenerateText() = %q, want %q", got, p.structured)
	}

	p.structuredErr = provider.ErrInvalidStructuredOutput
	for _, prompt := range []string{prompts.Messages.System.IntentClassification, prompts.Messages.System.FactQuery, prompts.Messages.System.FactExtraction} {
		got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, prompt, 100, nil, TemperatureSystem)
		if got != "" {
			t.Fatalf("GenerateText() = %q after an invalid result", got)
		}
	}
	if p.structuredCalls != 4 || p.contentCalls != 0 {
		t.Fatalf("calls = structured:%d content:%d, want 4/0", p.structuredCalls, p.contentCalls)
	}
}

func TestGenerateTextRejectsTextInToolResponse(t *testing.T) {
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
		got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, prompts.Messages.System.FactQuery, 100, nil, TemperatureSystem)
		want := ""
		if i == 0 {
			want = result
		}
		if got != want {
			t.Fatalf("GenerateText() = %q, want %q", got, want)
		}
	}
	if tools, texts := toolRequests.Load(), textRequests.Load(); tools != 4 || texts != 0 {
		t.Fatalf("API requests = tools:%d text:%d, want 4/0", tools, texts)
	}
}

func TestGenerateTextUsesNormalPathForChat(t *testing.T) {
	p := &structuredProviderMock{content: "chat"}
	client := structuredTestClient(p)

	got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "hello"}}, prompts.Messages.System.Base, 100, nil, 0.5)
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

	got := client.GenerateText(t.Context(), []model.Message{{Role: model.RoleUser, Content: "query"}}, prompts.Messages.System.FactQuery, 100, nil, TemperatureSystem)
	if got != result || calls.Load() != 2 {
		t.Fatalf("GenerateText = %q, API requests = %d, want %q and 2 requests", got, calls.Load(), result)
	}
}
