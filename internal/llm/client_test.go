package llm

import (
	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type TestError struct {
	StatusCode int
	Message    string
}

func (e *TestError) Error() string {
	return fmt.Sprintf("%d: %s", e.StatusCode, e.Message)
}

// MockProvider for testing
type MockProvider struct {
	GenerateContentFunc func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64) (string, string, error)
}

func (m *MockProvider) GenerateContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64) (string, string, error) {
	if m.GenerateContentFunc != nil {
		return m.GenerateContentFunc(ctx, messages, systemPrompt, maxTokens, images, temperature)
	}
	return "mock response", "{}", nil
}

func (m *MockProvider) IsRetryable(err error) bool {
	var te *TestError
	if errors.As(err, &te) {
		return te.StatusCode == http.StatusTooManyRequests || te.StatusCode >= http.StatusInternalServerError
	}
	return false
}

func (m *MockProvider) IsBadRequest(err error) bool {
	var te *TestError
	if errors.As(err, &te) {
		return te.StatusCode == http.StatusBadRequest
	}
	return false
}

func TestClient_ConcurrencyLimit(t *testing.T) {
	// Configure max concurrency = 2
	cfg := &config.Config{
		LLMMaxConcurrency: 2,
		LLMMaxRetries:     5,
		LLMProvider:       "mock",
	}

	// Create client with mock provider manually since NewClient switch doesn't support "mock"
	client := &Client{
		provider:  &MockProvider{},
		config:    cfg,
		semaphore: make(chan struct{}, cfg.LLMMaxConcurrency),
	}

	// Override provider specifically for this test to track active calls
	var activeCalls int32
	var maxActiveCalls int32

	// Identify calls using a sleep to simulate duration
	mockP := &MockProvider{
		GenerateContentFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64) (string, string, error) {
			current := atomic.AddInt32(&activeCalls, 1)

			// Record peak concurrency
			max := atomic.LoadInt32(&maxActiveCalls)
			if current > max {
				atomic.StoreInt32(&maxActiveCalls, current)
			}

			// Simulate processing time
			time.Sleep(100 * time.Millisecond)

			atomic.AddInt32(&activeCalls, -1)
			return "response", "payload", nil
		},
	}
	client.provider = mockP

	// Launch 10 concurrent requests
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.GenerateText(context.Background(), nil, "", 100, nil, 0.0)
		}()
	}
	wg.Wait()

	peak := atomic.LoadInt32(&maxActiveCalls)
	if peak > 2 {
		t.Errorf("Expected max active calls <= 2, got %d", peak)
	}
}

func TestClient_RetryLogic(t *testing.T) {
	cfg := &config.Config{
		LLMMaxConcurrency: 1,
		LLMMaxRetries:     5,
	}

	client := &Client{
		config:    cfg,
		semaphore: make(chan struct{}, 1),
	}

	callCount := 0
	mockP := &MockProvider{
		GenerateContentFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64) (string, string, error) {
			callCount++
			if callCount <= 2 {
				return "", "{}", &TestError{StatusCode: http.StatusTooManyRequests, Message: "Too Many Requests"}
			}
			return "success", "{}", nil
		},
	}
	client.provider = mockP

	start := time.Now()
	resp := client.GenerateText(context.Background(), nil, "", 100, nil, 0.0)
	elapsed := time.Since(start)

	if resp != "success" {
		t.Errorf("Expected 'success', got %q", resp)
	}

	if callCount != 3 {
		t.Errorf("Expected 3 calls (2 failures + 1 success), got %d", callCount)
	}

	// Expect delay: 1s + 2s = 3s minimum
	if elapsed < 3*time.Second {
		t.Errorf("Expected delay >= 3s, got %v", elapsed)
	}
}

func TestClient_adjustForGemma(t *testing.T) {
	tests := []struct {
		name           string
		provider       string
		modelName      string
		messages       []model.Message
		systemPrompt   string
		expectSystem   string
		expectFirstMsg string // Check logic integration
	}{
		{
			name:      "Should apply workaround: System prompt merged into user message",
			provider:  config.LLMProviderClaude,
			modelName: "google/gemma-3n-e2b-it:free",
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
			},
			systemPrompt:   "You are a helpful assistant.",
			expectSystem:   "", // Should be cleared
			expectFirstMsg: "System Instructions:\nYou are a helpful assistant.\n\nUser Message:\nHello",
		},
		{
			name:      "Should NOT apply workaround: Returns original messages",
			provider:  config.LLMProviderClaude,
			modelName: "claude-3-5-sonnet",
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
			},
			systemPrompt:   "System",
			expectSystem:   "System",
			expectFirstMsg: "Hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				LLMProvider:    tt.provider,
				AnthropicModel: tt.modelName,
			}
			client := &Client{config: cfg}

			outMsgs, outSystem := client.adjustForGemma(tt.messages, tt.systemPrompt)

			if outSystem != tt.expectSystem {
				t.Errorf("adjustForGemma() systemPrompt = %v, want %v", outSystem, tt.expectSystem)
			}

			if len(tt.messages) > 0 && len(outMsgs) > 0 {
				if outMsgs[0].Content != tt.expectFirstMsg {
					t.Errorf("adjustForGemma() first message content = %v, want %v", outMsgs[0].Content, tt.expectFirstMsg)
				}
			}
		})
	}
}

func TestClient_shouldApplyGemmaWorkaround(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		modelName    string
		messages     []model.Message
		systemPrompt string
		want         bool
	}{
		{
			name:      "Should apply: Claude + Gemma + SystemPrompt + UserMsg",
			provider:  config.LLMProviderClaude,
			modelName: "google/gemma-3n-e2b-it:free",
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
			},
			systemPrompt: "System",
			want:         true,
		},
		{
			name:      "Should NOT apply: Non-Claude provider",
			provider:  config.LLMProviderGemini,
			modelName: "google/gemma-3n-e2b-it:free",
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
			},
			systemPrompt: "System",
			want:         false,
		},
		{
			name:      "Should NOT apply: Non-Gemma model",
			provider:  config.LLMProviderClaude,
			modelName: "claude-3-5-sonnet",
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
			},
			systemPrompt: "System",
			want:         false,
		},
		{
			name:      "Should NOT apply: Empty system prompt",
			provider:  config.LLMProviderClaude,
			modelName: "google/gemma-3n-e2b-it:free",
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
			},
			systemPrompt: "",
			want:         false,
		},
		{
			name:         "Should NOT apply: Empty messages",
			provider:     config.LLMProviderClaude,
			modelName:    "google/gemma-3n-e2b-it:free",
			messages:     []model.Message{},
			systemPrompt: "System",
			want:         false,
		},
		{
			name:      "Should NOT apply: First message not User",
			provider:  config.LLMProviderClaude,
			modelName: "google/gemma-3n-e2b-it:free",
			messages: []model.Message{
				{Role: model.RoleAssistant, Content: "Hi"},
			},
			systemPrompt: "System",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				LLMProvider:    tt.provider,
				AnthropicModel: tt.modelName,
			}
			client := &Client{config: cfg}

			got := client.shouldApplyGemmaWorkaround(tt.messages, tt.systemPrompt)
			if got != tt.want {
				t.Errorf("shouldApplyGemmaWorkaround() = %v, want %v", got, tt.want)
			}
		})
	}
}
