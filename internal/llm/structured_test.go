package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"claude_bot/internal/config"
	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"
)

func (m *MockProvider) GenerateStructuredContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64, schema *provider.StructuredSchema) (string, string, error) {
	return m.GenerateContent(ctx, messages, systemPrompt, maxTokens, images, temperature)
}

type structuredProviderMock struct {
	mu              sync.Mutex
	structuredCalls int
	contentCalls    int
	structured      string
	structuredErr   error
	content         string
}

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
		content:    "legacy",
	}
	client := structuredTestClient(p)

	got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, Messages.System.FactQuery, 100, nil, TemperatureSystem)
	if got != p.structured {
		t.Fatalf("GenerateText() = %q, want %q", got, p.structured)
	}
	if p.structuredCalls != 1 || p.contentCalls != 0 {
		t.Fatalf("calls = structured:%d content:%d, want 1/0", p.structuredCalls, p.contentCalls)
	}
}

func TestGenerateTextFallbacksAfterStructuredError(t *testing.T) {
	p := &structuredProviderMock{
		structuredErr: errors.New("tool call missing"),
		content:       `{"target_candidates":[],"keys":[]}`,
	}
	client := structuredTestClient(p)

	got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, Messages.System.FactQuery, 100, nil, TemperatureSystem)
	if got != p.content {
		t.Fatalf("GenerateText() = %q, want %q (fallback content)", got, p.content)
	}
	if p.structuredCalls != 1 || p.contentCalls != 1 {
		t.Fatalf("calls = structured:%d content:%d, want 1/1 (with fallback)", p.structuredCalls, p.contentCalls)
	}
}

func TestGenerateTextSkipsUnsupportedToolsOnLaterRequests(t *testing.T) {
	p := &structuredProviderMock{
		structuredErr: errors.New("tool call missing"),
		content:       "text result",
	}
	client := structuredTestClient(p)

	for _, prompt := range []string{Messages.System.FactQuery, Messages.System.IntentClassification, Messages.System.FactExtraction} {
		got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, prompt, 100, nil, TemperatureSystem)
		if got != p.content {
			t.Fatalf("GenerateText() = %q, want %q", got, p.content)
		}
	}
	if p.structuredCalls != 1 || p.contentCalls != 3 {
		t.Fatalf("calls = structured:%d content:%d, want 1/3", p.structuredCalls, p.contentCalls)
	}
}

func TestGenerateTextKeepsUsingSupportedTools(t *testing.T) {
	p := &structuredProviderMock{structured: `{"target_candidates":[],"keys":[]}`}
	client := structuredTestClient(p)

	for i := 0; i < 3; i++ {
		got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, Messages.System.FactQuery, 100, nil, TemperatureSystem)
		if got != p.structured {
			t.Fatalf("GenerateText() = %q, want %q", got, p.structured)
		}
	}
	if p.structuredCalls != 3 || p.contentCalls != 0 {
		t.Fatalf("calls = structured:%d content:%d, want 3/0", p.structuredCalls, p.contentCalls)
	}
}

func TestGenerateTextStopsUsingToolsAfterLaterFailure(t *testing.T) {
	p := &structuredProviderMock{
		structured: `{"target_candidates":[],"keys":[]}`,
		content:    "text result",
	}
	client := structuredTestClient(p)
	got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, Messages.System.FactQuery, 100, nil, TemperatureSystem)
	if got != p.structured {
		t.Fatalf("first GenerateText() = %q, want %q", got, p.structured)
	}

	p.structuredErr = errors.New("structured tool call was not returned")
	for _, prompt := range []string{Messages.System.IntentClassification, Messages.System.FactQuery, Messages.System.FactExtraction} {
		got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, prompt, 100, nil, TemperatureSystem)
		if got != p.content {
			t.Fatalf("GenerateText() = %q, want %q", got, p.content)
		}
	}
	if p.structuredCalls != 2 || p.contentCalls != 3 {
		t.Fatalf("calls = structured:%d content:%d, want 2/3", p.structuredCalls, p.contentCalls)
	}
}

func TestGenerateTextStopsSendingToolsAfterTextOnlyAPIResponse(t *testing.T) {
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

	client := NewClient(&config.Config{
		LLMProvider: config.LLMProviderClaude, LLMMaxConcurrency: 1,
		AnthropicBaseURL: server.URL, AnthropicAuthToken: "test-token", AnthropicModel: "test-model",
	})
	for i := 0; i < 4; i++ {
		got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, Messages.System.FactQuery, 100, nil, TemperatureSystem)
		if got != result {
			t.Fatalf("GenerateText() = %q, want %q", got, result)
		}
	}
	if tools, texts := toolRequests.Load(), textRequests.Load(); tools != 2 || texts != 3 {
		t.Fatalf("API requests = tools:%d text:%d, want 2/3", tools, texts)
	}
}

func TestGenerateTextChecksUnsupportedToolsOnceConcurrently(t *testing.T) {
	p := &structuredProviderMock{structuredErr: errors.New("tool call missing"), content: "text result"}
	client := structuredTestClient(p)
	const requests = 8
	client.semaphore = make(chan struct{}, requests)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}}, Messages.System.FactQuery, 100, nil, TemperatureSystem)
			if got != p.content {
				t.Errorf("GenerateText() = %q, want %q", got, p.content)
			}
		}()
	}
	close(start)
	wg.Wait()
	if p.structuredCalls != 1 || p.contentCalls != requests {
		t.Fatalf("calls = structured:%d content:%d, want 1/%d", p.structuredCalls, p.contentCalls, requests)
	}
}

func TestGenerateTextUsesNormalPathForChat(t *testing.T) {
	p := &structuredProviderMock{content: "chat"}
	client := structuredTestClient(p)

	got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "hello"}}, Messages.System.Base, 100, nil, 0.5)
	if got != "chat" {
		t.Fatalf("GenerateText() = %q, want chat", got)
	}
	if p.structuredCalls != 0 || p.contentCalls != 1 {
		t.Fatalf("calls = structured:%d content:%d, want 0/1", p.structuredCalls, p.contentCalls)
	}
}

func TestStructuredOutputSchemas(t *testing.T) {
	for _, prompt := range []string{
		Messages.System.FactExtraction,
		Messages.System.FactQuery,
		Messages.System.IntentClassification,
		Messages.System.ImageGeneration,
	} {
		if schema := structuredOutputSchema(prompt); schema == nil {
			t.Fatalf("structuredOutputSchema(%q) = nil", prompt)
		}
	}
	if schema := structuredOutputSchema(Messages.System.Base); schema != nil {
		t.Fatalf("structuredOutputSchema(normal chat) = %#v, want nil", schema)
	}
}
