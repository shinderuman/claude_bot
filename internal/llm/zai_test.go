package llm

import (
	"net/http"
	"testing"
	"time"

	"claude_bot/internal/config"
)

func TestZAIRateLimitRetry(t *testing.T) {
	for _, tt := range []struct {
		name       string
		code       string
		retryAfter string
		wantCalls  int
		wantWait   time.Duration
	}{
		{name: "request limit", code: "1302", retryAfter: "2", wantCalls: 3, wantWait: 4 * time.Second},
		{name: "temporary overload", code: "1305", wantCalls: 3, wantWait: 3 * time.Second},
		{name: "wait exceeds retry budget", code: "1302", retryAfter: "61", wantCalls: 1},
		{name: "five-hour limit", code: "1316", retryAfter: "1", wantCalls: 1},
		{name: "five-hour limit with monthly cap", code: "1318", wantCalls: 1},
		{name: "five-hour limit with no extra usage", code: "1320", wantCalls: 1},
		{name: "usage quota", code: "1308", wantCalls: 1},
		{name: "unknown", code: "1999", wantCalls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			stubDefaultTransport(t, func(req *http.Request) (*http.Response, error) {
				calls++
				resp := stubResponse(req, http.StatusTooManyRequests, "application/json",
					`{"type":"error","error":{"code":"`+tt.code+`","message":"rate limited"}}`)
				resp.Header.Set("Retry-After", tt.retryAfter)
				return resp, nil
			})
			client, err := NewClient(&config.Config{
				LLMProvider: config.LLMProviderClaude, LLMMaxConcurrency: 1, LLMMaxRetries: 2,
				AnthropicBaseURL: "https://api.z.ai/api/anthropic", AnthropicAuthToken: "test-token", AnthropicModel: "glm-test",
			})
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			client.GenerateText(t.Context(), nil, "", 100, nil, 0, "テスト生成")
			if calls != tt.wantCalls || time.Since(start) < tt.wantWait {
				t.Fatalf("requests = %d, elapsed = %s, want %d requests and at least %s", calls, time.Since(start), tt.wantCalls, tt.wantWait)
			}
		})
	}
}

func TestZAITemporaryLimitDoesNotSuppressQuotaAlert(t *testing.T) {
	codes := []string{"1302", "1305", "1316", "1316"}
	var calls int
	stubDefaultTransport(t, func(req *http.Request) (*http.Response, error) {
		code := codes[calls]
		calls++
		return stubResponse(req, http.StatusTooManyRequests, "application/json",
			`{"type":"error","error":{"code":"`+code+`","message":"rate limited"}}`), nil
	})
	client, err := NewClient(&config.Config{
		LLMProvider: config.LLMProviderClaude, LLMMaxConcurrency: 1, RateLimitNotifyIntervalMinutes: 60,
		AnthropicBaseURL: "https://api.z.ai/api/anthropic", AnthropicAuthToken: "test-token", AnthropicModel: "glm-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, wantAlert := range []bool{true, false, true, false} {
		_, err := client.generate(t.Context(), nil, "", 100, nil, 0, "テスト生成", nil, nil)
		if err == nil {
			t.Fatalf("request with code %s succeeded", codes[i])
		}
		if alert := client.shouldNotifyRateLimit(err); alert != wantAlert {
			t.Fatalf("%s alert = %v, want %v", codes[i], alert, wantAlert)
		}
	}
}
