package llm

import (
	"context"
	"errors"
	"testing"

	"claude_bot/internal/config"
	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"
)

func (m *MockProvider) GenerateStructuredContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64, schema *provider.StructuredSchema) (string, string, error) {
	return m.GenerateContent(ctx, messages, systemPrompt, maxTokens, images, temperature)
}

type structuredProviderMock struct {
	structuredCalls int
	contentCalls    int
	structured      string
	structuredErr   error
	content         string
}

func (m *structuredProviderMock) GenerateContent(context.Context, []model.Message, string, int64, []model.Image, float64) (string, string, error) {
	m.contentCalls++
	return m.content, "{}", nil
}

func (m *structuredProviderMock) GenerateStructuredContent(context.Context, []model.Message, string, int64, []model.Image, float64, *provider.StructuredSchema) (string, string, error) {
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
